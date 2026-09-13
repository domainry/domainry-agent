package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) peerMessageReady(ctx context.Context, db conversationDB, m sdk.ConversationAgentMessage, a sdk.ConversationAuthority) (bool, error) {
	if m.Superseded {
		return false, nil
	}
	d, err := s.conversationDelegation(ctx, db, m.DelegationID, a)
	if err != nil {
		return false, err
	}
	if m.BriefVersion != d.Brief.Version || max(1, m.AgreementRevision) != d.AgreementRevision {
		return false, nil
	}
	if m.ConversationID != d.ConversationID && m.ConversationID != d.SourceConversationID {
		return false, nil
	}
	if m.DisagreementID != "" {
		current := false
		for _, issue := range d.Disagreements {
			current = current || issue.ID == m.DisagreementID && issue.Revision == m.DisagreementRevision
		}
		if !current {
			return false, nil
		}
	}
	if m.AfterRunID != "" {
		row, err := s.runRow(ctx, db, m.ConversationID, m.AfterRunID, a)
		if err != nil {
			return false, err
		}
		if !row.Run.Terminal() {
			return false, nil
		}
	}
	return true, nil
}

func (s *ConversationStore) preparePeerMessageDelivery(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, m *sdk.ConversationAgentMessage, a sdk.ConversationAuthority) error {
	if m.DeliveryMode == "next_run" {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_runs").Columns("run_id").Where(query.And(conversationScope(a, m.ConversationID), query.Or(query.Equal("status", "queued"), query.Equal("status", "running"), query.Equal("status", "waiting_user"), query.Equal("status", "waiting_confirmation"), query.Equal("status", "needs_reconciliation")))).OrderBy(query.Descending("created_at")).Limit(1).Build()
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, q, args...).Scan(&m.AfterRunID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if m.Kind != "reply" {
		return nil
	}
	p := query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", d.ID), query.Equal("message_id", m.ReplyToID))
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json").Where(p).Build()
	if err != nil {
		return err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
		return conversationError("conflict", "peer_question_unavailable")
	}
	var question sdk.ConversationAgentMessage
	if err = json.Unmarshal(raw, &question); err != nil {
		return err
	}
	if question.Kind != "question" || question.Superseded || question.AnsweredByID != "" || question.BriefVersion != d.Brief.Version || max(1, question.AgreementRevision) != d.AgreementRevision {
		return conversationError("conflict", "peer_question_unavailable")
	}
	if m.FromAgentID != "" && question.ToAgentID != m.FromAgentID {
		return conversationError("forbidden", "peer_reply_actor_invalid")
	}
	expectedRecipient := question.FromAgentID
	if expectedRecipient == "" {
		expectedRecipient = d.FromAgentID
		if question.ToAgentID == d.FromAgentID {
			expectedRecipient = d.ToAgentID
		}
	}
	if m.ToAgentID != expectedRecipient {
		return conversationError("forbidden", "peer_reply_actor_invalid")
	}
	question.AnsweredByID = m.ID
	q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationAgentMessageTable).Set("payload_json", conversationJSON(question)).Where(p).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

func (s *ConversationStore) supersedePeerMessages(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, a sdk.ConversationAuthority) error {
	p := query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", d.ID), query.Equal("consumed_run_id", ""))
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json").Where(p).Build()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	items := []sdk.ConversationAgentMessage{}
	for rows.Next() {
		var raw []byte
		var m sdk.ConversationAgentMessage
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &m); err != nil {
			break
		}
		if m.BriefVersion != d.Brief.Version || max(1, m.AgreementRevision) != d.AgreementRevision {
			m.Superseded = true
			items = append(items, m)
		}
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, m := range items {
		q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationAgentMessageTable).Set("consumed_run_id", "superseded").Set("payload_json", conversationJSON(m)).Where(query.And(p, query.Equal("message_id", m.ID))).Build()
		if err = conversationCAS(ctx, tx, q, args, err); err != nil {
			return err
		}
	}
	return nil
}
