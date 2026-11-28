package agent

import (
	"context"
	"database/sql"
	"reflect"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) PendingConversationMemoryCandidateDeliveries(ctx context.Context, runtimeID string, limit int) ([]agentpersistence.ConversationMemoryCandidateDelivery, error) {
	runtimeID = strings.TrimSpace(runtimeID)
	if runtimeID == "" {
		return nil, conversationError("bad_request", "runtime_invalid")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), agentRunTable).
		Columns("payload_json", "authority_json").
		Where(query.And(agentRunKindPredicate(agentRunKindConversation), query.Equal("runtime_id", runtimeID), query.Equal("status", "completed"), query.Equal("memory_candidate_state", memoryCandidateStatePending))).
		OrderBy(query.Ascending("updated_at"), query.Ascending("run_id")).Limit(limit).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentpersistence.ConversationMemoryCandidateDelivery{}
	for rows.Next() {
		var payload, authorityPayload []byte
		var delivery agentpersistence.ConversationMemoryCandidateDelivery
		if err = rows.Scan(&payload, &authorityPayload); err != nil {
			return nil, err
		}
		if err = unmarshalDurableJSON(payload, &delivery.Run); err != nil {
			return nil, err
		}
		if err = unmarshalDurableJSON(authorityPayload, &delivery.Authority); err != nil {
			return nil, err
		}
		out = append(out, delivery)
	}
	return out, rows.Err()
}

func (s *ConversationStore) RecordConversationMemoryCandidateReceipts(ctx context.Context, conversationID, runID string, receipts []agentsdk.AgentMemoryCandidateReceipt, authority agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	var out agentsdk.ConversationRun
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		old, err := s.runRow(ctx, tx, conversationID, runID, authority)
		if err != nil {
			return err
		}
		if old.Run.Status != "completed" || old.Run.Memory == nil || len(old.Run.Memory.Candidates) == 0 {
			return conversationError("conflict", "memory_candidate_state_invalid")
		}
		if reflect.DeepEqual(old.Run.Memory.CandidateReceipts, receipts) {
			out = old.Run
			return nil
		}
		if len(old.Run.Memory.CandidateReceipts) != 0 && !memoryCandidateReceiptsRetryable(old.Run.Memory.CandidateReceipts) {
			return conversationError("conflict", "memory_candidate_receipt_conflict")
		}
		current := old
		memory := *old.Run.Memory
		memory.CandidateReceipts = append([]agentsdk.AgentMemoryCandidateReceipt(nil), receipts...)
		current.Run.Memory = &memory
		if err = s.event(ctx, tx, &current, "memory.candidates.submitted", map[string]any{"candidate_count": len(memory.Candidates), "receipt_count": len(receipts)}); err != nil {
			return err
		}
		if err = s.saveRun(ctx, tx, current, old); err != nil {
			return err
		}
		out = current.Run
		return nil
	})
	return out, err
}
