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
	d, recordOwner, err := s.participantDelegation(ctx, s.store.Database(), id, a, "communicate")
	if err != nil {
		return nil, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json").Where(delegationMessageScope(d, recordOwner)).OrderBy(query.Descending("created_at"), query.Descending("message_id")).Limit(129).Build()
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
	if !agentsdk.ValidConversationDocumentReferences(in.Documents) {
		return agentsdk.ConversationAgentMessage{}, conversationError("bad_request", "shared_document_invalid")
	}
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
		executionAgent := in.ExecutionAgent
		d, recordOwner, err := s.participantDelegation(ctx, tx, id, a, "communicate")
		if err != nil {
			return err
		}
		if d.Status == "cancelled" || d.Status == "rejected" || d.Status == "accepted_delivery" {
			return conversationError("conflict", "delegation_closed")
		}
		if in.BriefVersion != d.Brief.Version || max(1, in.AgreementRevision) != d.AgreementRevision {
			return conversationError("conflict", "brief_version_invalid")
		}
		messageScope := delegationMessageScope(d, recordOwner)
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Projections(query.Project(query.CountAll())).Where(messageScope).Build()
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
		now := time.Now().UTC().Truncate(time.Millisecond)
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json").Where(query.And(messageScope, query.GreaterThan("created_at", now.Add(-time.Minute).UnixMilli()))).Build()
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		recent := 0
		for rows.Next() {
			var raw []byte
			var message agentsdk.ConversationAgentMessage
			if err = rows.Scan(&raw); err == nil {
				err = json.Unmarshal(raw, &message)
			}
			if err != nil {
				rows.Close()
				return err
			}
			if message.SenderUserID == a.UserID {
				recent++
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if recent >= 16 {
			return conversationError("rate_limited", "agent_message_rate")
		}
		out = agentsdk.ConversationAgentMessage{ID: "amsg_" + conversationHash([]string{conversationOwner(a), key})[:32], DelegationID: id, ToAgentID: in.ToAgentID, Kind: "message", Content: in.Content, BriefVersion: in.BriefVersion, AgreementRevision: d.AgreementRevision, CreatedAt: now}
		out.Kind, out.DeliveryMode, out.ReplyToID = in.Kind, in.DeliveryMode, in.ReplyToID
		out.SenderUserID, out.SenderRoleKey = a.UserID, a.RoleKey
		out.Documents = append([]agentsdk.ConversationDocumentReference(nil), in.Documents...)
		participant := recordOwner.UserID != a.UserID && (d.ExecutionSubject == nil || d.ExecutionSubject.UserID != a.UserID)
		if participant {
			grant, _ := delegationParticipant(d, a.UserID, "communicate")
			out.ParticipantUserID, out.ParticipantRevision, out.FromUserID = a.UserID, grant.Revision, a.UserID
			out.ParticipantRoleKey = a.RoleKey
			if senderConversationID != "" {
				sender, err := s.get(ctx, tx, senderConversationID, a)
				if err != nil {
					return err
				}
				out.FromAgentID = sender.AgentID
				if out.FromAgentID == "" {
					out.FromAgentID = "default"
				}
			}
		}
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
		if participant {
			if in.ToAgentID == d.ToAgentID {
				out.ConversationID = d.ConversationID
			} else if in.ToAgentID == d.FromAgentID {
				out.ConversationID = d.SourceConversationID
			}
		}
		if in.ToolRequest != nil {
			out.Source = &agentsdk.ConversationRunReference{ConversationID: in.ToolRequest.ConversationID, RunID: in.ToolRequest.RunID, BeforeStep: in.ToolRequest.Step + 1}
		}
		if out.ConversationID == "" {
			return conversationError("forbidden", "agent_message_recipient_invalid")
		}
		subjects, bound, err := s.delegationSubjects(ctx, tx, d.ID, recordOwner)
		if err != nil {
			return err
		}
		if participant || bound {
			// Reuse the authority already stored when this owned task was accepted.
			// A participant can send input but cannot choose the recipient's identity.
			taskOwner := recordOwner
			if bound {
				taskOwner = subjects.execution
			}
			q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(taskOwner), d.TaskID)).Build()
			if err != nil {
				return err
			}
			target, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
			if err != nil {
				return err
			}
			if conversationOwner(target.authority) != conversationOwner(taskOwner) || !target.authority.Known {
				return conversationError("forbidden", "execution_subject_mismatch")
			}
			recordOwner = target.authority
			if in.ToAgentID == d.ToAgentID {
				executionAgent = target.task.Agent
			} else {
				executionAgent = d.SourceAgent
			}
			if executionAgent == nil {
				return conversationError("forbidden", "agent_message_recipient_invalid")
			}
		}
		recordOwner, err = s.delegationMessageAuthority(ctx, tx, d, out.ConversationID, recordOwner)
		if err != nil {
			return err
		}
		if out.Source != nil {
			if err := s.saveSourceReleases(ctx, tx, d.ID, "message", a, recordOwner, []agentsdk.ConversationRunReference{*out.Source}); err != nil {
				return err
			}
		}
		if err = s.preparePeerMessageDelivery(ctx, tx, d, &out, recordOwner); err != nil {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("owner_key", "message_id", "delegation_id", "conversation_id", "created_at", "consumed_run_id", "payload_json", "runtime_id", "authority_json", "agent_json").Values(conversationOwner(recordOwner), out.ID, id, out.ConversationID, out.CreatedAt.UnixMilli(), "", conversationJSON(out), a.RuntimeID, conversationJSON(recordOwner), conversationJSON(executionAgent)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
	})
	return out, err
}

func (s *ConversationStore) insertConversationPeerNotice(ctx context.Context, tx *sql.Tx, message agentsdk.ConversationAgentMessage, agent *agentsdk.ConversationAgentSnapshot, a agentsdk.ConversationAuthority, canonical ...agentsdk.ConversationDelegation) error {
	subjects, found, err := s.delegationSubjects(ctx, tx, message.DelegationID, a)
	if err != nil {
		return err
	}
	owner := a
	if found {
		owner = subjects.source
	}
	// System dependency invalidation has already resolved the canonical row
	// in this transaction. Use it only for storage routing; the notice's
	// receiving authority is still resolved from the original admission.
	if len(canonical) > 1 || len(canonical) == 1 && canonical[0].ID != message.DelegationID {
		return conversationError("forbidden", "delegation_actor_invalid")
	}
	if len(canonical) == 1 {
		owner = delegationRecordAuthority(canonical[0], a)
	}
	d, err := s.ownedConversationDelegation(ctx, tx, message.DelegationID, owner)
	if err != nil {
		return err
	}
	a, err = s.delegationMessageAuthority(ctx, tx, d, message.ConversationID, a)
	if err != nil {
		return err
	}
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("owner_key", "message_id", "delegation_id", "conversation_id", "created_at", "consumed_run_id", "payload_json", "runtime_id", "authority_json", "agent_json").Values(conversationOwner(a), message.ID, message.DelegationID, message.ConversationID, message.CreatedAt.UnixMilli(), "", conversationJSON(message), a.RuntimeID, conversationJSON(a), conversationJSON(agent)).OnConflictDoNothing("owner_key", "message_id").Build()
	return conversationExec(ctx, tx, q, args, err)
}
