package agent

import (
	"context"
	"database/sql"
	"fmt"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func validConversationExecutionLimits(v agentsdk.ConversationExecutionLimits) bool {
	return v.MaxQueuedPerUser >= 1 && v.MaxQueuedPerUser <= 1000 &&
		v.MaxQueuedPerWorkspace >= v.MaxQueuedPerUser && v.MaxQueuedPerWorkspace <= 100000 &&
		v.MaxRunningPerUser >= 1 && v.MaxRunningPerUser <= 64 &&
		v.MaxRunningPerWorkspace >= v.MaxRunningPerUser && v.MaxRunningPerWorkspace <= 1024
}

func (s *ConversationStore) ConfigureConversationExecutionLimits(v agentsdk.ConversationExecutionLimits) error {
	if !validConversationExecutionLimits(v) {
		return fmt.Errorf("invalid conversation execution capacity limits")
	}
	s.capacityMu.Lock()
	defer s.capacityMu.Unlock()
	if s.capacityConfigured && s.capacityLimits != v {
		return fmt.Errorf("conversation execution capacity limits already configured")
	}
	s.capacityLimits, s.capacityConfigured = v, true
	return nil
}

func (s *ConversationStore) conversationExecutionLimits() (agentsdk.ConversationExecutionLimits, bool) {
	s.capacityMu.RLock()
	defer s.capacityMu.RUnlock()
	return s.capacityLimits, s.capacityConfigured
}

func (s *ConversationStore) lockConversationWorkspaceCapacity(ctx context.Context, tx *sql.Tx, a agentsdk.ConversationAuthority) error {
	workspaceKey := conversationHash([]string{a.RuntimeID, a.WorkspaceID})
	scopeKey := a.RuntimeID + ":" + workspaceKey
	id := "worker_scope:" + conversationHash([]string{conversationCapacityOwner, scopeKey})[:24]
	statement, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationCapacityGuardTable).
		Columns("id", "owner", "scope_key", "checkpoint", "updated_at").
		Values(id, conversationCapacityOwner, scopeKey, 1, "").
		OnConflictDoNothing("owner", "scope_key").Build()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, statement, args...); err != nil {
		return err
	}
	builder := query.NewSelectBuilder(s.store.Renderer(), conversationCapacityGuardTable).
		Columns("checkpoint").
		Where(query.And(query.Equal("owner", conversationCapacityOwner), query.Equal("scope_key", scopeKey)))
	if profile := s.store.Profile(); profile != nil && profile.Capabilities().RowLock {
		builder, err = profile.ApplyClaimLock(builder, false)
		if err != nil {
			return err
		}
	}
	statement, args, err = builder.Build()
	if err != nil {
		return err
	}
	var checkpoint int64
	return tx.QueryRowContext(ctx, statement, args...).Scan(&checkpoint)
}

func (s *ConversationStore) conversationExecutionCapacity(ctx context.Context, db conversationDB, a agentsdk.ConversationAuthority) (agentsdk.ConversationExecutionCapacity, error) {
	var out agentsdk.ConversationExecutionCapacity
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	workspaceKey := conversationHash([]string{a.RuntimeID, a.WorkspaceID})
	count := func(table string, predicates ...query.Predicate) (int, error) {
		statement, args, err := query.NewSelectBuilder(s.store.Renderer(), table).
			Projections(query.Project(query.CountAll())).Where(query.And(predicates...)).Build()
		if err != nil {
			return 0, err
		}
		var value int
		err = db.QueryRowContext(ctx, statement, args...).Scan(&value)
		return value, err
	}
	var err error
	runBase := []query.Predicate{agentRunKindPredicate(agentRunKindConversation), query.Equal("runtime_id", a.RuntimeID)}
	if out.UserQueued, err = count(agentRunTable, append(runBase, query.Equal("owner_key", conversationOwner(a)), query.Equal("status", "queued"))...); err != nil {
		return out, err
	}
	if out.UserRunning, err = count(agentRunTable, append(runBase, query.Equal("owner_key", conversationOwner(a)), query.Equal("status", "running"))...); err != nil {
		return out, err
	}
	if out.WorkspaceQueued, err = count(agentRunTable, append(runBase, query.Equal("workspace_id", workspaceKey), query.Equal("status", "queued"))...); err != nil {
		return out, err
	}
	if out.WorkspaceRunning, err = count(agentRunTable, append(runBase, query.Equal("workspace_id", workspaceKey), query.Equal("status", "running"))...); err != nil {
		return out, err
	}

	// A queued background task becomes a queued ConversationRun atomically when
	// launched. Count only one side of that transition so the backlog is stable.
	taskBase := []query.Predicate{conversationTaskKindPredicate(conversationTaskKindTask), query.Equal("runtime_id", a.RuntimeID), query.Equal("status", agentsdk.ConversationTaskStatusQueued)}
	userTasks, err := count(conversationTaskTable, append(taskBase, query.Equal("owner_key", conversationOwner(a)))...)
	if err != nil {
		return out, err
	}
	workspaceTasks, err := count(conversationTaskTable, append(taskBase, query.Equal("workspace_key", workspaceKey))...)
	if err != nil {
		return out, err
	}
	out.UserQueued += userTasks
	out.WorkspaceQueued += workspaceTasks
	return out, err
}

func (s *ConversationStore) ConversationExecutionCapacity(ctx context.Context, a agentsdk.ConversationAuthority) (agentsdk.ConversationExecutionCapacity, error) {
	return s.conversationExecutionCapacity(ctx, s.store.Database(), a)
}

func (s *ConversationStore) checkConversationQueueCapacity(ctx context.Context, tx *sql.Tx, a agentsdk.ConversationAuthority) error {
	limits, configured := s.conversationExecutionLimits()
	if !configured {
		return nil
	}
	if err := s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
		return err
	}
	capacity, err := s.conversationExecutionCapacity(ctx, tx, a)
	if err != nil {
		return err
	}
	if capacity.UserQueued >= limits.MaxQueuedPerUser {
		return conversationError("rate_limited", "user_queue_full")
	}
	if capacity.WorkspaceQueued >= limits.MaxQueuedPerWorkspace {
		return conversationError("rate_limited", "workspace_queue_full")
	}
	return nil
}

func (s *ConversationStore) checkConversationRunningCapacity(ctx context.Context, tx *sql.Tx, a agentsdk.ConversationAuthority) error {
	limits, configured := s.conversationExecutionLimits()
	if !configured {
		return nil
	}
	if err := s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
		return err
	}
	capacity, err := s.conversationExecutionCapacity(ctx, tx, a)
	if err != nil {
		return err
	}
	if capacity.UserRunning >= limits.MaxRunningPerUser {
		return conversationError("rate_limited", "user_execution_quota")
	}
	if capacity.WorkspaceRunning >= limits.MaxRunningPerWorkspace {
		return conversationError("rate_limited", "workspace_execution_quota")
	}
	return nil
}

var _ agentpersistence.ConversationCapacityRepository = (*ConversationStore)(nil)
