package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
	"github.com/domainry/domainry-orm/query"
)

func disagreementDeliveryDigest(d sdk.ConversationDelegation) string {
	if d.Delivery == nil {
		return ""
	}
	return conversationHash(d.Delivery)
}

func (s *ConversationStore) disagreementActor(ctx context.Context, db conversationDB, in *sdk.ConversationToolRequest, a sdk.ConversationAuthority) (sdk.ConversationDisagreementActor, error) {
	out := sdk.ConversationDisagreementActor{UserID: a.UserID}
	if in != nil {
		c, err := s.get(ctx, db, in.ConversationID, a)
		if err != nil {
			return out, err
		}
		out.AgentID = c.AgentID
		if out.AgentID == "" {
			out.AgentID = "default"
		}
		out.Source = &sdk.ConversationRunReference{ConversationID: in.ConversationID, RunID: in.RunID, BeforeStep: in.Step + 1}
	}
	return out, nil
}

func disagreementSources(value sdk.ConversationDisagreement) []sdk.ConversationRunReference {
	out := []sdk.ConversationRunReference{}
	seen := map[sdk.ConversationRunReference]bool{}
	add := func(ref sdk.ConversationRunReference) {
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	if value.DecisionActor != nil && value.DecisionActor.Source != nil {
		add(*value.DecisionActor.Source)
	}
	if value.Actor.Source != nil {
		add(*value.Actor.Source)
	}
	for _, claim := range value.Claims {
		if claim.Actor.Source != nil {
			add(*claim.Actor.Source)
		}
		for _, ref := range claim.Receipts {
			add(sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2})
		}
	}
	return out
}

func (s *ConversationStore) readDisagreement(ctx context.Context, db conversationDB, did, id string, revision int64, a sdk.ConversationAuthority) (sdk.ConversationDisagreement, error) {
	var out sdk.ConversationDisagreement
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDisagreementTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", did), query.Equal("disagreement_id", id), query.Equal("revision", revision))).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	if err = db.QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, conversationError("not_found", "disagreement_unavailable")
		}
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (s *ConversationStore) saveDisagreement(ctx context.Context, tx *sql.Tx, did string, value sdk.ConversationDisagreement, a sdk.ConversationAuthority) error {
	raw := conversationJSON(value)
	if len(raw) > 24576 || len(value.Sources) > 32 {
		return conversationError("rate_limited", "disagreement_capacity")
	}
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationDisagreementTable).Columns("owner_key", "delegation_id", "disagreement_id", "revision", "payload_json").Values(conversationOwner(a), did, value.ID, value.Revision, raw).OnConflictDoNothing("owner_key", "delegation_id", "disagreement_id", "revision").Build()
	if err = conversationExec(ctx, tx, q, args, err); err != nil {
		return err
	}
	saved, err := s.readDisagreement(ctx, tx, did, value.ID, value.Revision, a)
	if err != nil {
		return err
	}
	if conversationHash(saved) != conversationHash(value) {
		return conversationError("conflict", "disagreement_history_conflict")
	}
	return nil
}

// This runs inside the existing delegation revision/lease transaction. A new
// claim cannot race acceptance, and a replay cannot duplicate a claim or notice.
func (s *ConversationStore) applyDisagreement(ctx context.Context, tx *sql.Tx, d *sdk.ConversationDelegation, update sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) error {
	in := update.Disagreement
	if err := execution.ValidateDisagreementChange(*d, in); err != nil {
		return conversationError("bad_request", "disagreement_invalid")
	}
	if actor := update.ToolRequest; actor != nil {
		if actor.ConversationID != d.SourceConversationID && (actor.ConversationID != d.ConversationID || in.Operation == "decide") {
			return conversationError("forbidden", "delegation_actor_invalid")
		}
	}
	actor, err := s.disagreementActor(ctx, tx, update.ToolRequest, a)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	index := -1
	var value sdk.ConversationDisagreement
	if in.Operation == "raise" {
		if len(d.Disagreements) >= 16 {
			return conversationError("rate_limited", "disagreement_capacity")
		}
		value.ConversationDisagreementSummary = sdk.ConversationDisagreementSummary{ID: "dispute_" + conversationHash([]string{d.ID, update.ClientID})[:32], Title: in.Title, Condition: in.Condition, BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, DeliveryDigest: disagreementDeliveryDigest(*d)}
		if in.Condition != nil {
			value.Requirement = d.Brief.CompletionConditions[*in.Condition]
		}
	} else {
		for i, item := range d.Disagreements {
			if item.ID == in.ID {
				index = i
				break
			}
		}
		if index < 0 {
			return conversationError("not_found", "disagreement_unavailable")
		}
		if d.Disagreements[index].Revision != in.ExpectedRevision {
			return conversationError("conflict", "disagreement_changed")
		}
		value, err = s.readDisagreement(ctx, tx, d.ID, in.ID, in.ExpectedRevision, a)
		if err != nil {
			return err
		}
	}
	value.Revision++
	value.Actor = actor
	value.Event = in.Operation
	value.Reason = update.Reason
	value.UpdatedAt = now
	if in.Operation == "raise" || in.Operation == "add_claim" {
		if len(value.Claims)+len(in.Claims) > 8 {
			return conversationError("rate_limited", "disagreement_capacity")
		}
		for _, input := range in.Claims {
			for _, ref := range input.Receipts {
				var record persistence.ConversationToolExecution
				found, err := s.readExecutionTool(ctx, tx, persistence.ConversationClaim{Authority: a, Run: sdk.ConversationRun{ID: ref.RunID, ConversationID: ref.ConversationID}}, ref.Step, ref.CallID, &record)
				if err != nil {
					return err
				}
				if !found || record.State != "completed" || record.Result == nil || conversationHash(record.Result) != ref.SHA256 {
					return conversationError("conflict", "completion_evidence_changed")
				}
			}
			value.Claims = append(value.Claims, sdk.ConversationDisagreementClaim{ID: "claim_" + conversationHash([]any{value.ID, value.Revision, len(value.Claims), input})[:32], ConversationDisagreementClaimInput: input, Actor: actor, CreatedAt: now})
		}
		value.Status = "open"
		value.Decision = nil
		value.DecisionActor = nil
		value.OwnerAgentID = d.FromAgentID
		value.OwnerUserID = ""
		value.NextAction = "对照结论的证据、范围、时间和计算过程，决定后续处理"
	} else {
		decision := *in.Decision
		if decision.BriefVersion != d.Brief.Version || decision.AgreementRevision != d.AgreementRevision || decision.DeliveryDigest != disagreementDeliveryDigest(*d) {
			return conversationError("conflict", "disagreement_context_changed")
		}
		if decision.Outcome == "adopt" {
			found := false
			for _, claim := range value.Claims {
				found = found || claim.ID == decision.AdoptClaimID
			}
			if !found {
				return conversationError("bad_request", "disagreement_claim_invalid")
			}
		}
		value.Decision = &decision
		value.DecisionActor = &actor
		value.BriefVersion = d.Brief.Version
		value.AgreementRevision = d.AgreementRevision
		value.DeliveryDigest = decision.DeliveryDigest
		value.OwnerAgentID = decision.OwnerAgentID
		value.OwnerUserID = ""
		value.NextAction = decision.NextAction
		value.Status = map[string]string{"inspect": "checking", "revise": "needs_revision", "ask_user": "waiting_user", "adopt": "resolved"}[decision.Outcome]
		if decision.Outcome == "ask_user" {
			value.OwnerUserID = a.UserID
		}
	}
	value.Sources = disagreementSources(value)
	if err = s.saveDisagreement(ctx, tx, d.ID, value, a); err != nil {
		return err
	}
	if index < 0 {
		d.Disagreements = append(d.Disagreements, value.ConversationDisagreementSummary)
	} else {
		d.Disagreements[index] = value.ConversationDisagreementSummary
	}
	if d.Status == "accepted_delivery" && execution.DisagreementBlocks(value.ConversationDisagreementSummary, *d, disagreementDeliveryDigest(*d)) {
		d.Status = "delivered"
	}
	return s.disagreementNotice(ctx, tx, *d, value, update.ToolRequest, a)
}

func (s *ConversationStore) disagreementNotice(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, value sdk.ConversationDisagreement, actor *sdk.ConversationToolRequest, a sdk.ConversationAuthority) error {
	// Supersede only earlier notices for this exact issue; other questions and
	// operation confirmations keep their own delivery and authorization state.
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", d.ID), query.Equal("consumed_run_id", ""))).Build()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	old := []sdk.ConversationAgentMessage{}
	for rows.Next() {
		var raw []byte
		var m sdk.ConversationAgentMessage
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &m); err != nil {
			break
		}
		if m.DisagreementID == value.ID {
			m.Superseded = true
			old = append(old, m)
		}
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, m := range old {
		q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationAgentMessageTable).Set("payload_json", conversationJSON(m)).Set("consumed_run_id", "superseded").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("message_id", m.ID))).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
	}
	to, conversation, agent := d.FromAgentID, d.SourceConversationID, d.SourceAgent
	if value.OwnerAgentID == d.ToAgentID {
		to, conversation = d.ToAgentID, d.ConversationID
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(a), d.TaskID)).Build()
		if err != nil {
			return err
		}
		task, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
		if err != nil {
			return err
		}
		agent = task.task.Agent
	}
	// A user wait needs no new Agent run. It remains visible in the attention list.
	if value.Status == "waiting_user" || value.Status == "resolved" {
		return nil
	}
	m := sdk.ConversationAgentMessage{ID: "amsg_" + conversationHash([]any{d.ID, value.ID, value.Revision})[:32], DelegationID: d.ID, ToAgentID: to, ConversationID: conversation, Kind: "disagreement_changed", DisagreementID: value.ID, DisagreementRevision: value.Revision, BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, Content: "Disagreement " + value.ID + " changed. Read delegation_get and delegation_disagreement for current evidence and the assigned next action before continuing. This notice grants no new business authority.", CreatedAt: value.UpdatedAt}
	if actor != nil {
		m.FromAgentID = value.Actor.AgentID
		m.Source = value.Actor.Source
	}
	return s.insertConversationPeerNotice(ctx, tx, m, agent, a)
}

func (s *ConversationStore) ConversationDisagreementHistory(ctx context.Context, did, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationDisagreementHistory, error) {
	out := sdk.ConversationDisagreementHistory{Items: []sdk.ConversationDisagreement{}, Complete: true}
	d, err := s.ConversationDelegation(ctx, did, a)
	if err != nil {
		return out, err
	}
	found := false
	for _, v := range d.Disagreements {
		found = found || v.ID == id
	}
	if !found {
		return out, conversationError("not_found", "disagreement_unavailable")
	}
	if before < 0 {
		return out, conversationError("bad_request", "cursor_invalid")
	}
	p := query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", did), query.Equal("disagreement_id", id))
	if before > 0 {
		p = query.And(p, query.LessThan("revision", before))
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDisagreementTable).Columns("payload_json").Where(p).OrderBy(query.Descending("revision")).Limit(3).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item sdk.ConversationDisagreement
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if len(out.Items) > 2 {
		out.Items = out.Items[:2]
		out.Complete = false
		out.NextBefore = out.Items[1].Revision
	}
	return out, rows.Err()
}

func (s *ConversationStore) transferDisagreementResponsibilities(ctx context.Context, tx *sql.Tx, before sdk.ConversationDelegation, after *sdk.ConversationDelegation, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) error {
	actor, err := s.disagreementActor(ctx, tx, in.ToolRequest, a)
	if err != nil {
		return err
	}
	after.Disagreements = append([]sdk.ConversationDisagreementSummary{}, after.Disagreements...)
	record := delegationRecordAuthority(before, a)
	for i, summary := range before.Disagreements {
		if summary.OwnerAgentID != before.ToAgentID || summary.Status == "resolved" {
			continue
		}
		value, err := s.readDisagreement(ctx, tx, before.ID, summary.ID, summary.Revision, record)
		if err != nil {
			return err
		}
		value.Revision++
		value.OwnerAgentID = after.ToAgentID
		value.Actor = actor
		value.Event = "transfer_responsibility"
		value.Reason = in.Reason
		value.UpdatedAt = after.UpdatedAt
		value.Sources = disagreementSources(value)
		if err = s.saveDisagreement(ctx, tx, after.ID, value, record); err != nil {
			return err
		}
		after.Disagreements[i] = value.ConversationDisagreementSummary
	}
	return nil
}
