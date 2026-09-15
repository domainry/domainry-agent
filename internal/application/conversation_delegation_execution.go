package application

import (
	"context"
	"strings"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) prepareAgentExecutionBinding(ctx context.Context, in sdk.ConversationAgentWrite, a sdk.ConversationAuthority) error {
	if in.DelegationExecution == nil || *in.DelegationExecution == "caller" {
		return nil
	}
	if *in.DelegationExecution != "owner" {
		return conversationFailure("bad_request", "agent_execution_mode_invalid")
	}
	if a.RoleKey == "" || strings.TrimSpace(a.RoleKey) != a.RoleKey {
		return conversationFailure("bad_request", "agent_execution_role_required")
	}
	for _, op := range []string{"view", "receive", "share"} {
		if err := s.authorizeCollaboration(ctx, op, nil, a); err != nil {
			return err
		}
	}
	return nil
}

// Receiver authorization is independent of the issuer's tool invocation and
// selected profile. The issuer's provenance remains on the admission request.
func delegationExecutorContext(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, conversationPeerRequestKey{}, conversationPeerRequest{})
	ctx = context.WithValue(ctx, conversationAgentContextKey{}, (*sdk.ConversationAgentSnapshot)(nil))
	return context.WithValue(ctx, conversationModelSelectionContextKey{}, (*sdk.ConversationModelSelection)(nil))
}

func bindDelegationSnapshot(snapshot *sdk.ConversationAgentSnapshot, a sdk.ConversationAuthority) {
	snapshot.DelegationRoleKey = a.RoleKey
	snapshot.Digest = ""
	snapshot.Digest = conversationDigest(*snapshot)
}

func delegationExecutor(d sdk.ConversationDelegation, a sdk.ConversationAuthority) bool {
	return d.ExecutionSubject != nil && *d.ExecutionSubject == *executionSubject(a)
}

// Resolve only the configuration owner's explicit binding. Neither a public
// request nor a model can supply a different execution user or selected role.
func (s *ConversationService) delegationExecutionAgent(ctx context.Context, id string, issuer sdk.ConversationAuthority) (*sdk.ConversationAgentSnapshot, sdk.ConversationAuthority, error) {
	agent, err := s.conversationAgent(ctx, id, issuer)
	if err != nil {
		return nil, issuer, err
	}
	executor := issuer
	if agent.DelegationExecution != "" && agent.DelegationExecution != "caller" && agent.DelegationExecution != "owner" {
		return nil, executor, conversationFailure("conflict", "agent_execution_mode_invalid")
	}
	if agent.DelegationExecution == "owner" {
		if agent.ID == "default" || agent.OwnerUserID == "" || agent.DelegationRoleKey == "" {
			return nil, executor, conversationFailure("forbidden", "execution_subject_mismatch")
		}
		executor.UserID, executor.RoleKey = agent.OwnerUserID, agent.DelegationRoleKey
		if err := s.validateAgentSharingSubjects(ctx, issuer, []string{executor.UserID}); err != nil {
			return nil, executor, err
		}
		ctx = delegationExecutorContext(ctx)
	}
	d := sdk.ConversationDelegation{OwnerUserID: issuer.UserID, ToAgentID: id, ExecutionSubject: executionSubject(executor)}
	if err := s.authorizeCollaboration(ctx, "receive", &d, executor); err != nil {
		return nil, executor, err
	}
	if agent.DelegationExecution == "owner" {
		if err := s.authorizeCollaboration(ctx, "share", nil, executor); err != nil {
			return nil, executor, err
		}
	}
	snapshot, err := s.freezeConversationAgent(ctx, id, executor)
	if err != nil {
		return nil, executor, err
	}
	if snapshot.Revision != agent.Revision || agent.OwnerUserID != "" && snapshot.OwnerUserID != agent.OwnerUserID {
		return nil, executor, conversationFailure("conflict", "agent_changed")
	}
	if agent.DelegationExecution == "owner" {
		bindDelegationSnapshot(snapshot, executor)
	}
	return snapshot, executor, nil
}

func (s *ConversationService) delegationTaskRecord(ctx context.Context, d sdk.ConversationDelegation, a sdk.ConversationAuthority) (sdk.ConversationTask, error) {
	if d.ExecutionSubject != nil && d.ExecutionSubject.UserID != a.UserID {
		repo, ok := s.repo.(persistence.ConversationDelegationExecutionRepository)
		if !ok {
			return sdk.ConversationTask{}, conversationFailure("unavailable", "delegation_execution_unavailable")
		}
		return repo.ConversationDelegationTask(ctx, d.ID, a)
	}
	return s.conversationTaskRecord(ctx, d.TaskID, a)
}

func (s *ConversationService) authorizeDelegationExecutionBinding(ctx context.Context, d sdk.ConversationDelegation, snapshot *sdk.ConversationAgentSnapshot, executor sdk.ConversationAuthority) error {
	if snapshot == nil || snapshot.DelegationRoleKey == "" {
		return nil
	}
	repo, ok := s.repo.(persistence.ConversationDelegationExecutionRepository)
	if !ok {
		return conversationFailure("unavailable", "delegation_execution_unavailable")
	}
	subjects, err := repo.ConversationDelegationAuthorities(ctx, d.ID, executor)
	if err != nil {
		return err
	}
	if subjects.Executor != executor {
		return conversationFailure("forbidden", "execution_subject_mismatch")
	}
	ctx = delegationExecutorContext(ctx)
	if err := s.authorizeCollaboration(ctx, "initiate", nil, subjects.Issuer); err != nil {
		return err
	}
	current, resolved, err := s.delegationExecutionAgent(ctx, d.ToAgentID, subjects.Issuer)
	if err != nil {
		return err
	}
	if resolved != executor || current.Digest != snapshot.Digest {
		return conversationFailure("conflict", "agent_changed")
	}
	return nil
}

func (s *ConversationService) authorizeDelegationResume(ctx context.Context, d sdk.ConversationDelegation, actor sdk.ConversationAuthority) error {
	task, err := s.delegationTaskRecord(ctx, d, actor)
	if err != nil {
		return err
	}
	executionCtx := delegationExecutorContext(ctx)
	subjects, ok := s.repo.(persistence.ConversationDelegationExecutionRepository)
	if !ok {
		if task.Agent != nil && task.Agent.DelegationRoleKey != "" {
			return conversationFailure("unavailable", "delegation_execution_unavailable")
		}
		return s.authorizeCollaboration(executionCtx, "receive", &d, actor)
	}
	authorities, err := subjects.ConversationDelegationAuthorities(ctx, d.ID, actor)
	if err != nil {
		return err
	}
	executor := authorities.Executor
	if err = s.authorizeCollaboration(executionCtx, "receive", &d, executor); err != nil {
		return err
	}
	if err = s.authorizeDelegationExecutionBinding(executionCtx, d, task.Agent, executor); err != nil {
		return err
	}
	if executionCtx, err = s.selectConversationAgent(executionCtx, task.Agent, executor); err != nil {
		return err
	}
	sourceCtx, err := s.delegationContractSourceContext(executionCtx, d, executor)
	if err != nil {
		return err
	}
	roots := d.Requirements.Sources
	if scope, ok := sourceCtx.Value(conversationPublishedSourceKey{}).(conversationPublishedSource); ok {
		roots = scope.roots
	}
	for _, ref := range mergeConversationSources(roots) {
		if err = s.checkRunSources(sourceCtx, ref, executor); err != nil {
			return err
		}
	}
	audit := s.sourceAudit(executor, d.ConversationID)
	for _, edge := range d.Dependencies {
		if _, err := audit.dependency(executionCtx, edge); err != nil {
			return err
		}
	}
	return nil
}
