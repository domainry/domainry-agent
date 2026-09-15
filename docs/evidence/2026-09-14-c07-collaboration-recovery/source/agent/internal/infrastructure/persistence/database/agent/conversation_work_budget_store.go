package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

type conversationWorkReservation struct {
	persistence.ConversationWorkStepReservation
	OwnerKey       string    `json:"owner_key"`
	ConversationID string    `json:"conversation_id"`
	RunID          string    `json:"run_id"`
	Fence          int64     `json:"fence"`
	StartedAt      time.Time `json:"started_at"`
}

type conversationWorkLedger struct {
	Budget       sdk.ConversationWorkBudget             `json:"budget"`
	Usage        sdk.ConversationWorkUsage              `json:"usage"`
	Delegations  int                                    `json:"delegations"`
	Reservations map[string]conversationWorkReservation `json:"reservations,omitempty"`
	UpdatedAt    time.Time                              `json:"updated_at"`
}

func defaultConversationWorkBudget() sdk.ConversationWorkBudget {
	return sdk.ConversationWorkBudget{MaxInputTokens: 16 * 1024 * 1024, MaxOutputTokens: 1024 * 1024, MaxDurationSeconds: 6 * 60 * 60}
}

func validConversationWorkBudget(value sdk.ConversationWorkBudget) bool {
	if value.MaxInputTokens < 1 || value.MaxInputTokens > 1_000_000_000 || value.MaxOutputTokens < 1 || value.MaxOutputTokens > 100_000_000 || value.MaxDurationSeconds < 1 || value.MaxDurationSeconds > 30*24*60*60 {
		return false
	}
	if value.MaxModelCost == nil {
		return value.Currency == ""
	}
	return *value.MaxModelCost > 0 && !math.IsNaN(*value.MaxModelCost) && !math.IsInf(*value.MaxModelCost, 0) && len(value.Currency) > 0 && len(value.Currency) <= 16
}

func workLedgerScope(a sdk.ConversationAuthority, root string) query.Predicate {
	return query.And(query.Equal("runtime_id", a.RuntimeID), query.Equal("workspace_id", a.WorkspaceID), query.Equal("root_conversation_id", root))
}

func (s *ConversationStore) readConversationWorkLedger(ctx context.Context, db conversationDB, root string, a sdk.ConversationAuthority) (conversationWorkLedger, bool, error) {
	var out conversationWorkLedger
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationWorkBudgetTable).Columns("payload_json").Where(workLedgerScope(a, root)).Build()
	if err != nil {
		return out, false, err
	}
	var raw []byte
	err = db.QueryRowContext(ctx, q, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, false, err
	}
	if out.Reservations == nil {
		out.Reservations = map[string]conversationWorkReservation{}
	}
	return out, true, nil
}

func (s *ConversationStore) saveConversationWorkLedger(ctx context.Context, tx *sql.Tx, root string, a sdk.ConversationAuthority, ledger conversationWorkLedger, insert bool) error {
	ledger.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	if insert {
		q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationWorkBudgetTable).Columns("runtime_id", "workspace_id", "root_conversation_id", "updated_at", "payload_json").Values(a.RuntimeID, a.WorkspaceID, root, ledger.UpdatedAt.UnixMilli(), conversationJSON(ledger)).Build()
		return conversationExec(ctx, tx, q, args, err)
	}
	q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationWorkBudgetTable).Set("updated_at", ledger.UpdatedAt.UnixMilli()).Set("payload_json", conversationJSON(ledger)).Where(workLedgerScope(a, root)).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

func (s *ConversationStore) conversationWorkDelegationCount(ctx context.Context, db conversationDB, root string) (int, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDelegationTable).Projections(query.Project(query.CountAll())).Where(query.Equal("root_conversation_id", root)).Build()
	if err != nil {
		return 0, err
	}
	var count int
	err = db.QueryRowContext(ctx, q, args...).Scan(&count)
	return count, err
}

func (s *ConversationStore) admitConversationWorkBudget(ctx context.Context, tx *sql.Tx, root string, requested *sdk.ConversationWorkBudget, a sdk.ConversationAuthority) (sdk.ConversationWorkBudget, sdk.ConversationWorkUsage, error) {
	ledger, found, err := s.readConversationWorkLedger(ctx, tx, root, a)
	if err != nil {
		return sdk.ConversationWorkBudget{}, sdk.ConversationWorkUsage{}, err
	}
	if found {
		if requested != nil && conversationHash(*requested) != conversationHash(ledger.Budget) {
			return ledger.Budget, ledger.Usage, conversationError("conflict", "work_budget_changed")
		}
		if ledger.Usage.InputTokens >= ledger.Budget.MaxInputTokens || ledger.Usage.OutputTokens >= ledger.Budget.MaxOutputTokens || ledger.Usage.DurationMilliseconds >= ledger.Budget.MaxDurationSeconds*1000 || ledger.Budget.MaxModelCost != nil && ledger.Usage.ModelCost >= *ledger.Budget.MaxModelCost {
			return ledger.Budget, ledger.Usage, conversationError("rate_limited", "work_budget_exhausted")
		}
		if ledger.Delegations == 0 {
			ledger.Delegations, err = s.conversationWorkDelegationCount(ctx, tx, root)
			if err != nil {
				return ledger.Budget, ledger.Usage, err
			}
		}
		if ledger.Delegations >= 32 {
			return ledger.Budget, ledger.Usage, conversationError("rate_limited", "delegation_limit")
		}
		ledger.Delegations++
		return ledger.Budget, ledger.Usage, s.saveConversationWorkLedger(ctx, tx, root, a, ledger, false)
	}
	budget := defaultConversationWorkBudget()
	if requested != nil {
		budget = *requested
	}
	if !validConversationWorkBudget(budget) {
		return budget, sdk.ConversationWorkUsage{}, conversationError("bad_request", "work_budget_invalid")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	usage := sdk.ConversationWorkUsage{Currency: budget.Currency, CostKnown: true, StartedAt: now}
	ledger = conversationWorkLedger{Budget: budget, Usage: usage, Delegations: 1, Reservations: map[string]conversationWorkReservation{}, UpdatedAt: now}
	return budget, usage, s.saveConversationWorkLedger(ctx, tx, root, a, ledger, true)
}

func (s *ConversationStore) ConversationWorkBudget(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationWorkBudget, sdk.ConversationWorkUsage, error) {
	d, _, err := s.participantDelegation(ctx, s.store.Database(), id, a, "view")
	if err != nil {
		return sdk.ConversationWorkBudget{}, sdk.ConversationWorkUsage{}, err
	}
	ledger, found, err := s.readConversationWorkLedger(ctx, s.store.Database(), d.RootConversationID, a)
	if err != nil {
		return sdk.ConversationWorkBudget{}, sdk.ConversationWorkUsage{}, err
	}
	if !found {
		return sdk.ConversationWorkBudget{}, sdk.ConversationWorkUsage{}, conversationError("not_found", "work_budget_unavailable")
	}
	return ledger.Budget, ledger.Usage, nil
}

func workReservationKey(claim persistence.ConversationClaim, step int) string {
	return conversationHash([]any{claim.Run.ConversationID, claim.Run.ID, step})
}

func (s *ConversationStore) pruneConversationWorkReservations(ctx context.Context, tx *sql.Tx, ledger *conversationWorkLedger) error {
	now := time.Now().UnixMilli()
	for key, item := range ledger.Reservations {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_runs").Columns("status", "fence", "lease_expires_at").Where(query.And(query.Equal("owner_key", item.OwnerKey), query.Equal("conversation_id", item.ConversationID), query.Equal("run_id", item.RunID))).Build()
		if err != nil {
			return err
		}
		var status string
		var fence, expires int64
		err = tx.QueryRowContext(ctx, q, args...).Scan(&status, &fence, &expires)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (status != "running" || fence != item.Fence || expires <= now) {
			delete(ledger.Reservations, key)
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func workReservationCost(item persistence.ConversationWorkStepReservation, input, output int64) float64 {
	if item.Price == nil {
		return 0
	}
	return (float64(input)*item.Price.InputPerMillion + float64(output)*item.Price.OutputPerMillion) / 1_000_000
}

func (s *ConversationStore) workLedgerForClaim(ctx context.Context, tx *sql.Tx, claim persistence.ConversationClaim) (sdk.ConversationDelegation, conversationWorkLedger, error) {
	if claim.Run.BackgroundTask == nil || claim.Run.BackgroundTask.DelegationID == "" {
		return sdk.ConversationDelegation{}, conversationWorkLedger{}, conversationError("bad_request", "work_budget_unavailable")
	}
	d, err := s.conversationDelegation(ctx, tx, claim.Run.BackgroundTask.DelegationID, claim.Authority)
	if err != nil {
		return d, conversationWorkLedger{}, err
	}
	ledger, found, err := s.readConversationWorkLedger(ctx, tx, d.RootConversationID, claim.Authority)
	if err != nil {
		return d, ledger, err
	}
	if !found {
		budget := defaultConversationWorkBudget()
		if d.WorkBudget != nil {
			budget = *d.WorkBudget
		}
		count, countErr := s.conversationWorkDelegationCount(ctx, tx, d.RootConversationID)
		if countErr != nil {
			return d, ledger, countErr
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		ledger = conversationWorkLedger{Budget: budget, Usage: sdk.ConversationWorkUsage{Currency: budget.Currency, CostKnown: true, StartedAt: now}, Delegations: max(1, count), Reservations: map[string]conversationWorkReservation{}, UpdatedAt: now}
		err = s.saveConversationWorkLedger(ctx, tx, d.RootConversationID, claim.Authority, ledger, true)
	}
	return d, ledger, err
}

func (s *ConversationStore) ReserveConversationWorkStep(ctx context.Context, claim persistence.ConversationClaim, step int, requested persistence.ConversationWorkStepReservation) (persistence.ConversationWorkStepReservation, error) {
	if step < 0 || requested.InputTokenUpperBound < 1 || requested.MaxOutputTokens < 1 || requested.MaxDurationMilliseconds < 1 {
		return requested, conversationError("bad_request", "work_budget_reservation_invalid")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.claimed(ctx, tx, claim); err != nil {
			return err
		}
		if err := s.lockConversationWorkspaceCapacity(ctx, tx, claim.Authority); err != nil {
			return err
		}
		d, ledger, err := s.workLedgerForClaim(ctx, tx, claim)
		if err != nil {
			return err
		}
		if err = s.pruneConversationWorkReservations(ctx, tx, &ledger); err != nil {
			return err
		}
		key := workReservationKey(claim, step)
		if existing, ok := ledger.Reservations[key]; ok && existing.Fence == claim.Fence {
			requested = existing.ConversationWorkStepReservation
			return nil
		}
		reservedInput, reservedOutput, reservedDuration, reservedCost := int64(0), int64(0), int64(0), float64(0)
		for otherKey, item := range ledger.Reservations {
			if otherKey == key {
				continue
			}
			reservedInput += item.InputTokenUpperBound
			reservedOutput += item.MaxOutputTokens
			reservedDuration += item.MaxDurationMilliseconds
			reservedCost += workReservationCost(item.ConversationWorkStepReservation, item.InputTokenUpperBound, item.MaxOutputTokens)
		}
		budget := ledger.Budget
		if ledger.Usage.InputTokens+reservedInput+requested.InputTokenUpperBound > budget.MaxInputTokens || ledger.Usage.OutputTokens+reservedOutput+requested.MaxOutputTokens > budget.MaxOutputTokens || ledger.Usage.DurationMilliseconds+reservedDuration+requested.MaxDurationMilliseconds > budget.MaxDurationSeconds*1000 {
			return conversationError("rate_limited", "work_budget_exhausted")
		}
		if budget.MaxModelCost != nil {
			if requested.Price == nil || requested.Price.Currency != budget.Currency {
				return conversationError("conflict", "work_budget_price_unavailable")
			}
			if ledger.Usage.ModelCost+reservedCost+workReservationCost(requested, requested.InputTokenUpperBound, requested.MaxOutputTokens) > *budget.MaxModelCost+1e-12 {
				return conversationError("rate_limited", "work_budget_exhausted")
			}
		}
		ledger.Reservations[key] = conversationWorkReservation{ConversationWorkStepReservation: requested, OwnerKey: conversationOwner(claim.Authority), ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, Fence: claim.Fence, StartedAt: time.Now().UTC()}
		return s.saveConversationWorkLedger(ctx, tx, d.RootConversationID, claim.Authority, ledger, false)
	})
	return requested, err
}

func usageTokenValue(usage map[string]any, keys ...string) (int64, bool) {
	for _, key := range keys {
		value, ok := usage[key]
		if !ok {
			continue
		}
		switch n := value.(type) {
		case int:
			return int64(n), n >= 0
		case int64:
			return n, n >= 0
		case float64:
			return int64(n), n >= 0 && n == math.Trunc(n) && n <= math.MaxInt64
		case json.Number:
			v, err := n.Int64()
			return v, err == nil && v >= 0
		}
		return 0, false
	}
	return 0, false
}

func (s *ConversationStore) completeConversationWorkStep(ctx context.Context, tx *sql.Tx, claim persistence.ConversationClaim, step int, result sdk.ConversationStepResult) error {
	d, ledger, err := s.workLedgerForClaim(ctx, tx, claim)
	if err != nil {
		return err
	}
	key := workReservationKey(claim, step)
	reservation, ok := ledger.Reservations[key]
	if ok && reservation.Fence != claim.Fence {
		return conversationError("conflict", "work_budget_reservation_lost")
	}
	input, inputKnown := usageTokenValue(result.Usage, "input_tokens", "prompt_tokens")
	output, outputKnown := usageTokenValue(result.Usage, "output_tokens", "completion_tokens")
	if !ok {
		// Compatibility for repository callers that predate the reservation
		// port. Production execution always reserves before the provider call.
		reservation = conversationWorkReservation{ConversationWorkStepReservation: persistence.ConversationWorkStepReservation{InputTokenUpperBound: input, MaxOutputTokens: output, MaxDurationMilliseconds: 1}, Fence: claim.Fence, StartedAt: time.Now().UTC()}
		if !inputKnown || !outputKnown {
			reservation.InputTokenUpperBound, reservation.MaxOutputTokens = 0, 0
			ledger.Usage.CostKnown = false
		}
	}
	if !inputKnown || !outputKnown {
		input, output = reservation.InputTokenUpperBound, reservation.MaxOutputTokens
		ledger.Usage.CostKnown = false
	}
	for _, cacheKey := range []string{"cache_creation_input_tokens", "cache_read_input_tokens"} {
		if value, exists := result.Usage[cacheKey]; exists {
			cache, known := usageTokenValue(map[string]any{cacheKey: value}, cacheKey)
			if !known {
				input, output = reservation.InputTokenUpperBound, reservation.MaxOutputTokens
				ledger.Usage.CostKnown = false
				break
			}
			input += cache
		}
	}
	if input > reservation.InputTokenUpperBound || output > reservation.MaxOutputTokens {
		return conversationError("conflict", "work_budget_provider_usage_exceeded")
	}
	duration := time.Since(reservation.StartedAt).Milliseconds()
	if duration < 1 {
		duration = 1
	}
	if duration > reservation.MaxDurationMilliseconds {
		return conversationError("rate_limited", "work_budget_exhausted")
	}
	ledger.Usage.InputTokens += input
	ledger.Usage.OutputTokens += output
	ledger.Usage.DurationMilliseconds += duration
	if reservation.Price == nil {
		ledger.Usage.CostKnown = false
	} else {
		ledger.Usage.ModelCost += workReservationCost(reservation.ConversationWorkStepReservation, input, output)
	}
	if ledger.Usage.InputTokens > ledger.Budget.MaxInputTokens || ledger.Usage.OutputTokens > ledger.Budget.MaxOutputTokens || ledger.Usage.DurationMilliseconds > ledger.Budget.MaxDurationSeconds*1000 || ledger.Budget.MaxModelCost != nil && ledger.Usage.ModelCost > *ledger.Budget.MaxModelCost+1e-12 {
		return conversationError("rate_limited", "work_budget_exhausted")
	}
	delete(ledger.Reservations, key)
	return s.saveConversationWorkLedger(ctx, tx, d.RootConversationID, claim.Authority, ledger, false)
}

func (s *ConversationStore) ReleaseConversationWorkStep(ctx context.Context, claim persistence.ConversationClaim, step int) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.claimed(ctx, tx, claim); err != nil {
			return err
		}
		if err := s.lockConversationWorkspaceCapacity(ctx, tx, claim.Authority); err != nil {
			return err
		}
		d, ledger, err := s.workLedgerForClaim(ctx, tx, claim)
		if err != nil {
			return err
		}
		key := workReservationKey(claim, step)
		item, ok := ledger.Reservations[key]
		if !ok {
			return nil
		}
		if item.Fence != claim.Fence {
			return conversationError("conflict", "work_budget_reservation_lost")
		}
		delete(ledger.Reservations, key)
		return s.saveConversationWorkLedger(ctx, tx, d.RootConversationID, claim.Authority, ledger, false)
	})
}

var _ persistence.ConversationWorkBudgetRepository = (*ConversationStore)(nil)
