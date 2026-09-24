package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) collaborationReplay(ctx context.Context, tx *sql.Tx, a agentsdk.ConversationAuthority, key string, request any, out any) (bool, error) {
	command := agentOperationCommand("agent.collaboration_mutation", "agent.collaboration.mutate", key, request, a)
	receipt, claimed, err := s.store.operations.Claim(sharedoperation.WithExecutor(ctx, tx), command)
	if err = agentOperationError(err); err != nil {
		return false, err
	}
	if claimed {
		return false, nil
	}
	if receipt.Status != sharedoperation.StatusSucceeded {
		return false, conversationError("conflict", "mutation_in_progress")
	}
	return true, json.Unmarshal(receipt.Result, out)
}

func (s *ConversationStore) collaborationReceipt(ctx context.Context, a agentsdk.ConversationAuthority, key string, request any, out any) (bool, error) {
	command := agentOperationCommand("agent.collaboration_mutation", "agent.collaboration.mutate", key, request, a)
	receipt, found, err := s.store.operations.Get(ctx, sharedoperation.ManagedIdentity{ID: command.ID, Scope: command.Scope, Owner: command.Owner, Kind: command.Kind})
	if err != nil || !found {
		return false, err
	}
	if receipt.Command.IdempotencyKey != command.IdempotencyKey || receipt.Command.RequestFingerprint != command.RequestFingerprint {
		return false, agentOperationError(sharedoperation.ErrIdempotencyConflict)
	}
	if receipt.Status != sharedoperation.StatusSucceeded {
		return false, conversationError("conflict", "mutation_in_progress")
	}
	return true, json.Unmarshal(receipt.Result, out)
}

func (s *ConversationStore) saveCollaborationMutation(ctx context.Context, tx *sql.Tx, a agentsdk.ConversationAuthority, key string, request, result any) error {
	command := agentOperationCommand("agent.collaboration_mutation", "agent.collaboration.mutate", key, request, a)
	return s.store.operations.Complete(sharedoperation.WithExecutor(ctx, tx), sharedoperation.Completion{ID: command.ID, Scope: command.Scope, Owner: command.Owner, Kind: command.Kind, IdempotencyKey: command.IdempotencyKey, RequestFingerprint: command.RequestFingerprint, Result: json.RawMessage(conversationJSON(result)), CompletedAt: time.Now().UTC()})
}

func (s *ConversationStore) ownedConversationAgent(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error) {
	var out agentsdk.ConversationAgent
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationPeerLinkTable).Columns("payload_json").Where(query.And(query.Equal("link_kind", conversationPeerLinkKindInstance), query.Equal("owner_key", conversationOwner(a)), query.Equal("link_id", id), query.Equal("peer_key", ""))).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	err = db.QueryRowContext(ctx, q, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, conversationError("not_found", "agent_not_found")
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	if err == nil {
		out.OwnerUserID = a.UserID
		out.Shared = false
	}
	return out, err
}

func (s *ConversationStore) ConversationAgent(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error) {
	return s.conversationAgent(ctx, s.store.Database(), id, a)
}

func (s *ConversationStore) ConversationAgents(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationAgent, error) {
	out := []agentsdk.ConversationAgent{}
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationPeerLinkTable).Columns("payload_json").Where(query.And(query.Equal("link_kind", conversationPeerLinkKindInstance), query.Equal("owner_key", conversationOwner(a)))).OrderBy(query.Ascending("link_id")).Limit(64).Build()
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
		var item agentsdk.ConversationAgent
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		out[i].OwnerUserID = a.UserID
		out[i].Shared = false
	}
	shared, err := s.sharedConversationAgents(ctx, a)
	return append(out, shared...), err
}

func (s *ConversationStore) WriteConversationAgent(ctx context.Context, id string, in agentsdk.ConversationAgentWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error) {
	var out agentsdk.ConversationAgent
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		if !personalMemoryKey(in.ClientID) || in.ExpectedRevision < 0 {
			return conversationError("bad_request", "agent_invalid")
		}
		key := conversationHash([]string{"agent", id, in.ClientID})
		var request any = in
		if in.DelegationExecution != nil && *in.DelegationExecution == "owner" {
			request = struct {
				Input   agentsdk.ConversationAgentWrite
				RoleKey string
			}{in, a.RoleKey}
		}
		if replay, err := s.collaborationReplay(ctx, tx, a, key, request, &out); err != nil || replay {
			return err
		}
		if err := s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		if id == "" {
			if in.ExpectedRevision != 0 {
				return conversationError("conflict", "revision_conflict")
			}
			count, err := s.visibleConversationAgentCount(ctx, tx, a)
			if err != nil {
				return err
			}
			if count >= 64 {
				return conversationError("rate_limited", "agent_limit")
			}
			out = agentsdk.ConversationAgent{ID: "agent_" + conversationHash([]string{conversationOwner(a), in.ClientID})[:32], CreatedAt: now}
		} else {
			var err error
			out, err = s.ownedConversationAgent(ctx, tx, id, a)
			if err != nil {
				return err
			}
			if out.Revision != in.ExpectedRevision {
				return conversationError("conflict", "revision_conflict")
			}
		}
		out.Name, out.Description, out.Instructions = in.Name, in.Description, in.Instructions
		out.DefinitionKey, out.DefinitionVersion, out.DefinitionDigest = in.DefinitionKey, in.DefinitionVersion, in.DefinitionDigest
		out.Tools, out.SkillKeys = append([]string{}, in.Tools...), append([]string{}, in.SkillKeys...)
		out.External = in.External
		out.ModelKey, out.ReasoningEffort, out.Enabled, out.MaxConcurrent = in.ModelKey, in.ReasoningEffort, in.Enabled, in.MaxConcurrent
		out.OwnerUserID, out.Shared = a.UserID, false
		if in.DelegationExecution != nil {
			switch *in.DelegationExecution {
			case "caller":
				out.DelegationExecution, out.DelegationRoleKey = "", ""
			case "owner":
				if a.RoleKey == "" {
					return conversationError("bad_request", "agent_execution_role_required")
				}
				out.DelegationExecution, out.DelegationRoleKey = "owner", a.RoleKey
			default:
				return conversationError("bad_request", "agent_execution_mode_invalid")
			}
		}
		if in.SharedWithUserIDs != nil {
			if err := validateAgentSharingUsers(*in.SharedWithUserIDs, a.UserID); err != nil {
				return err
			}
			out.SharedWithUserIDs = append([]string{}, (*in.SharedWithUserIDs)...)
		}
		out.Revision, out.UpdatedAt = in.ExpectedRevision+1, now
		if id == "" {
			q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationPeerLinkTable).Columns("link_kind", "owner_key", "link_id", "scope_key", "revision", "created_at", "updated_at", "payload_json").Values(conversationPeerLinkKindInstance, conversationOwner(a), out.ID, out.ID, out.Revision, out.CreatedAt.UnixMilli(), out.UpdatedAt.UnixMilli(), conversationJSON(out)).Build()
			if err = conversationExec(ctx, tx, q, args, err); err != nil {
				return err
			}
		} else {
			q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationPeerLinkTable).Set("revision", out.Revision).Set("updated_at", out.UpdatedAt.UnixMilli()).Set("payload_json", conversationJSON(out)).Where(query.And(query.Equal("link_kind", conversationPeerLinkKindInstance), query.Equal("owner_key", conversationOwner(a)), query.Equal("link_id", id), query.Equal("peer_key", ""), query.Equal("revision", in.ExpectedRevision))).Build()
			if err = conversationCAS(ctx, tx, q, args, err); err != nil {
				return err
			}
		}
		if err := s.saveConversationAgentGrants(ctx, tx, out, a); err != nil {
			return err
		}
		return s.saveCollaborationMutation(ctx, tx, a, key, request, out)
	})
	return out, err
}
