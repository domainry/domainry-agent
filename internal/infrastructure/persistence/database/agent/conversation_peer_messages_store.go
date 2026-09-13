package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
	"slices"
	"time"
)

func (s *ConversationStore) ConversationAgentMessages(ctx context.Context, id string, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationAgentMessage, error) {
	out := []agentsdk.ConversationAgentMessage{}
	if _, err := s.ConversationDelegation(ctx, id, a); err != nil {
		return nil, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", id))).OrderBy(query.Descending("created_at"), query.Descending("message_id")).Limit(129).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var message agentsdk.ConversationAgentMessage
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &message); err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	slices.Reverse(out)
	return out, rows.Err()
}

func (s *ConversationStore) SendConversationAgentMessage(ctx context.Context, id string, in agentsdk.ConversationAgentMessageSend, senderConversationID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgentMessage, error) {
	if in.Kind == "" {
		in.Kind = "message"
	}
	if in.DeliveryMode == "" {
		in.DeliveryMode = "next_step"
	}
	if in.AgreementRevision < 0 || in.Kind != "message" && in.Kind != "question" && in.Kind != "reply" || in.DeliveryMode != "next_step" && in.DeliveryMode != "next_run" || in.Kind == "reply" && !personalMemoryKey(in.ReplyToID) || in.Kind != "reply" && in.ReplyToID != "" {
		return agentsdk.ConversationAgentMessage{}, conversationError("bad_request", "agent_message_invalid")
	}
	var out agentsdk.ConversationAgentMessage
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		if err := s.validatePeerMutation(ctx, tx, in.ToolRequest, a); err != nil {
			return err
		}
		key := conversationHash([]string{"peer-message", id, senderConversationID, in.ClientID})
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in, &out); err != nil || replay {
			return err
		}
		if err := s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
			return err
		}
		d, err := s.conversationDelegation(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if d.Status == "cancelled" || d.Status == "rejected" || d.Status == "accepted_delivery" {
			return conversationError("conflict", "delegation_closed")
		}
		if in.BriefVersion != d.Brief.Version || max(1, in.AgreementRevision) != d.AgreementRevision {
			return conversationError("conflict", "brief_version_invalid")
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Projections(query.Project(query.CountAll())).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", id))).Build()
		if err != nil {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&count); err != nil {
			return err
		}
		if count >= 128 {
			return conversationError("rate_limited", "agent_message_limit")
		}
		out = agentsdk.ConversationAgentMessage{ID: "amsg_" + conversationHash([]string{conversationOwner(a), key})[:32], DelegationID: id, ToAgentID: in.ToAgentID, Kind: "message", Content: in.Content, BriefVersion: in.BriefVersion, AgreementRevision: d.AgreementRevision, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
		out.Kind, out.DeliveryMode, out.ReplyToID = in.Kind, in.DeliveryMode, in.ReplyToID
		switch senderConversationID {
		case "":
			out.FromUserID = a.UserID
			if in.ToAgentID == d.ToAgentID {
				out.ConversationID = d.ConversationID
			} else if in.ToAgentID == d.FromAgentID {
				out.ConversationID = d.SourceConversationID
			}
		case d.SourceConversationID:
			if in.ToAgentID == d.ToAgentID {
				out.FromAgentID, out.ConversationID = d.FromAgentID, d.ConversationID
			}
		case d.ConversationID:
			if in.ToAgentID == d.FromAgentID {
				out.FromAgentID, out.ConversationID = d.ToAgentID, d.SourceConversationID
			}
		}
		if in.ToolRequest != nil {
			out.Source = &agentsdk.ConversationRunReference{ConversationID: in.ToolRequest.ConversationID, RunID: in.ToolRequest.RunID, BeforeStep: in.ToolRequest.Step + 1}
		}
		if out.ConversationID == "" {
			return conversationError("forbidden", "agent_message_recipient_invalid")
		}
		if err = s.preparePeerMessageDelivery(ctx, tx, d, &out, a); err != nil {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("owner_key", "message_id", "delegation_id", "conversation_id", "created_at", "consumed_run_id", "payload_json", "runtime_id", "authority_json", "agent_json").Values(conversationOwner(a), out.ID, id, out.ConversationID, out.CreatedAt.UnixMilli(), "", conversationJSON(out), a.RuntimeID, conversationJSON(a), conversationJSON(in.ExecutionAgent)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
	})
	return out, err
}

func (s *ConversationStore) insertConversationPeerNotice(ctx context.Context, tx *sql.Tx, message agentsdk.ConversationAgentMessage, agent *agentsdk.ConversationAgentSnapshot, a agentsdk.ConversationAuthority) error {
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("owner_key", "message_id", "delegation_id", "conversation_id", "created_at", "consumed_run_id", "payload_json", "runtime_id", "authority_json", "agent_json").Values(conversationOwner(a), message.ID, message.DelegationID, message.ConversationID, message.CreatedAt.UnixMilli(), "", conversationJSON(message), a.RuntimeID, conversationJSON(a), conversationJSON(agent)).OnConflictDoNothing("owner_key", "message_id").Build()
	return conversationExec(ctx, tx, q, args, err)
}
