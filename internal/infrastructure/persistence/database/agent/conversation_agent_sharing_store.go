package agent

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

func validateAgentSharingUsers(users []string, owner string) error {
	if len(users) > 64 {
		return conversationError("bad_request", "agent_sharing_invalid")
	}
	seen := map[string]bool{}
	for _, user := range users {
		if user == "" || len(user) > 255 || strings.TrimSpace(user) != user || user == owner || seen[user] || strings.ContainsAny(user, "\x00\r\n\t") {
			return conversationError("bad_request", "agent_sharing_invalid")
		}
		seen[user] = true
	}
	return nil
}

func (s *ConversationStore) saveConversationAgentGrants(ctx context.Context, tx *sql.Tx, agent sdk.ConversationAgent, a sdk.ConversationAuthority) error {
	if err := validateAgentSharingUsers(agent.SharedWithUserIDs, a.UserID); err != nil {
		return err
	}
	q, args, err := query.NewDeleteBuilder(s.store.Renderer(), conversationAgentGrantTable).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("agent_id", agent.ID))).Build()
	if err = conversationExec(ctx, tx, q, args, err); err != nil {
		return err
	}
	for _, user := range agent.SharedWithUserIDs {
		viewer := sdk.ConversationAuthority{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: user}
		count, err := s.visibleConversationAgentCount(ctx, tx, viewer)
		if err != nil {
			return err
		}
		if count >= 64 {
			return conversationError("rate_limited", "agent_limit")
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationAgentGrantTable).Columns("owner_key", "agent_id", "viewer_key", "owner_user_id").Values(conversationOwner(a), agent.ID, conversationOwner(viewer), a.UserID).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationStore) visibleConversationAgentCount(ctx context.Context, db conversationDB, a sdk.ConversationAuthority) (int, error) {
	total := 0
	for _, source := range []struct{ table, key string }{{conversationAgentTable, "owner_key"}, {conversationAgentGrantTable, "viewer_key"}} {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), source.table).Projections(query.Project(query.CountAll())).Where(query.Equal(source.key, conversationOwner(a))).Build()
		if err != nil {
			return 0, err
		}
		var count int
		if err = db.QueryRowContext(ctx, q, args...).Scan(&count); err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func (s *ConversationStore) visibleConversationAgentIDs(ctx context.Context, tx *sql.Tx, a sdk.ConversationAuthority) (map[string]bool, error) {
	out := map[string]bool{"default": true}
	for _, source := range []struct{ table, key string }{{conversationAgentTable, "owner_key"}, {conversationAgentGrantTable, "viewer_key"}} {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), source.table).Columns("agent_id").Where(query.Equal(source.key, conversationOwner(a))).Build()
		if err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			out[id] = true
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *ConversationStore) conversationAgent(ctx context.Context, db conversationDB, id string, a sdk.ConversationAuthority) (sdk.ConversationAgent, error) {
	owned, err := s.ownedConversationAgent(ctx, db, id, a)
	var coded *sdk.Error
	if err == nil || !errors.As(err, &coded) || coded.Code != "agent.conversation.agent_not_found" {
		return owned, err
	}
	return s.sharedConversationAgent(ctx, db, id, a)
}

func (s *ConversationStore) sharedConversationAgent(ctx context.Context, db conversationDB, id string, a sdk.ConversationAuthority) (sdk.ConversationAgent, error) {
	var out sdk.ConversationAgent
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentGrantTable).Columns("owner_key", "owner_user_id").Where(query.And(query.Equal("viewer_key", conversationOwner(a)), query.Equal("agent_id", id))).Build()
	if err != nil {
		return out, err
	}
	var ownerKey, ownerUser string
	if err = db.QueryRowContext(ctx, q, args...).Scan(&ownerKey, &ownerUser); errors.Is(err, sql.ErrNoRows) {
		return out, conversationError("not_found", "agent_not_found")
	} else if err != nil {
		return out, err
	}
	// This identity is only the owner key for a granted configuration lookup.
	// It is never returned as authority for tools, tasks, conversations or data.
	owner := sdk.ConversationAuthority{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: ownerUser}
	if conversationOwner(owner) != ownerKey {
		return out, conversationError("not_found", "agent_not_found")
	}
	out, err = s.ownedConversationAgent(ctx, db, id, owner)
	if err != nil {
		return out, err
	}
	if !slices.Contains(out.SharedWithUserIDs, a.UserID) {
		return sdk.ConversationAgent{}, conversationError("not_found", "agent_not_found")
	}
	out.Shared = true
	out.SharedWithUserIDs = nil
	return out, nil
}

func (s *ConversationStore) sharedConversationAgents(ctx context.Context, a sdk.ConversationAuthority) ([]sdk.ConversationAgent, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentGrantTable).Columns("agent_id").Where(query.Equal("viewer_key", conversationOwner(a))).OrderBy(query.Ascending("agent_id")).Limit(64).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []sdk.ConversationAgent{}
	for _, id := range ids {
		item, err := s.sharedConversationAgent(ctx, s.store.Database(), id, a)
		var coded *sdk.Error
		if errors.As(err, &coded) && coded.Code == "agent.conversation.agent_not_found" {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
