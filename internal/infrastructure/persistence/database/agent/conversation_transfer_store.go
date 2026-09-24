package agent

import (
	"context"
	"database/sql"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	"github.com/domainry/domainry-orm/query"
)

func transferMutationKey(id, clientID string) string {
	return conversationHash([]string{"transfer", id, clientID})
}
func (s *ConversationStore) ConversationDelegationTransferReceipt(ctx context.Context, id string, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegation, bool, error) {
	var out sdk.ConversationDelegation
	var found bool
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		if _, _, err := s.transferDelegationActor(ctx, tx, id, a); err != nil {
			return err
		}
		var err error
		found, err = s.collaborationReceipt(sharedoperation.WithExecutor(ctx, tx), a, transferMutationKey(id, in.ClientID), in, &out)
		return err
	})
	return out, found, err
}

func (s *ConversationStore) TransferConversationDelegation(ctx context.Context, id string, in persistence.ConversationDelegationTransferAdmission, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	var out sdk.ConversationDelegation
	if in.Request.Transfer == nil || in.Request.Action != "transfer" || !personalMemoryKey(in.Request.ClientID) || !executionText(in.Request.Reason, 4096, true) || !executionText(in.Request.Transfer.RemainingWork, 8192, true) {
		return out, conversationError("bad_request", "delegation_transfer_invalid")
	}
	executor := a
	if in.ExecutionAuthority != nil {
		executor = *in.ExecutionAuthority
	}
	if conversationAuthority(executor) != nil || !sameConversationWorkspace(a, executor) {
		return out, conversationError("forbidden", "execution_subject_mismatch")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		if err := s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
			return err
		}
		if err := s.validatePeerMutation(ctx, tx, in.Request.ToolRequest, a); err != nil {
			return err
		}
		key := transferMutationKey(id, in.Request.ClientID)
		d, record, err := s.transferDelegationActor(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in.Request, &out); err != nil || replay {
			return err
		}
		manager := d.OwnerUserID != a.UserID
		subjects, err := s.delegationAuthorities(ctx, tx, d, record)
		if err != nil {
			return err
		}
		if d.Revision != in.Request.ExpectedRevision {
			return conversationError("conflict", "revision_conflict")
		}
		if d.SubjectExited || d.Status == "cancelled" || d.Status == "accepted_delivery" {
			return conversationError("conflict", "delegation_closed")
		}
		if in.Agent.ID != in.Request.Transfer.AgentID || in.Task.Agent != nil || in.Task.SourceConversationID != d.SourceConversationID {
			return conversationError("bad_request", "delegation_transfer_invalid")
		}
		if in.Request.ToolRequest != nil && !manager && in.Request.ToolRequest.ConversationID != d.SourceConversationID {
			return conversationError("forbidden", "delegation_actor_invalid")
		}
		if in.Agent.ID != "default" {
			agent, err := s.conversationAgent(ctx, tx, in.Agent.ID, a)
			if err != nil {
				return err
			}
			if !agent.Enabled || agent.Revision != in.Agent.Revision {
				return conversationError("conflict", "agent_changed")
			}
			if in.Agent.DelegationRoleKey != "" && (in.ExecutionAuthority == nil || agent.DelegationExecution != "owner" || agent.OwnerUserID != executor.UserID || agent.DelegationRoleKey != executor.RoleKey || in.Agent.DelegationRoleKey != executor.RoleKey) {
				return conversationError("forbidden", "execution_subject_mismatch")
			}
			if agent.DelegationExecution == "owner" && in.Agent.DelegationRoleKey == "" || in.Agent.OwnerUserID != "" && in.Agent.OwnerUserID != agent.OwnerUserID {
				return conversationError("forbidden", "execution_subject_mismatch")
			}
			if conversationOwner(executor) != conversationOwner(a) && (!agent.Shared || agent.OwnerUserID != executor.UserID || in.Agent.OwnerUserID != agent.OwnerUserID) {
				return conversationError("forbidden", "execution_subject_mismatch")
			}
		} else if conversationOwner(executor) != conversationOwner(a) {
			return conversationError("forbidden", "execution_subject_mismatch")
		}
		if in.Agent.ExecutionSubject != nil && *in.Agent.ExecutionSubject != (sdk.ConversationExecutionSubject{RuntimeID: executor.RuntimeID, WorkspaceID: executor.WorkspaceID, UserID: executor.UserID}) {
			return conversationError("forbidden", "execution_subject_mismatch")
		}
		cycleReceiver := in.Agent.ID
		if cycleReceiver == d.ToAgentID {
			cycleReceiver = ""
		}
		if _, err = s.conversationDelegationRoot(ctx, tx, d.SourceConversationID, cycleReceiver, record); err != nil {
			return err
		}
		assignments, err := s.conversationAssignments(ctx, tx, d, a)
		if err != nil {
			return err
		}
		if len(assignments) >= 16 {
			return conversationError("rate_limited", "delegation_transfer_limit")
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(subjects.execution), d.TaskID)).Build()
		if err != nil {
			return err
		}
		oldTask, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
		if err != nil {
			return err
		}
		if !oldTask.task.Terminal() {
			return conversationError("conflict", "delegation_still_running")
		}
		if in.Agent.ID == d.ToAgentID && oldTask.task.Agent != nil && oldTask.task.Agent.Revision == in.Agent.Revision && oldTask.task.Agent.Digest == in.Agent.Digest && oldTask.task.Agent.ModelIdentity == in.Agent.ModelIdentity {
			return conversationError("bad_request", "delegation_recovery_not_needed")
		}
		// Active outgoing work still reports to this assignment's conversation.
		// Do not strand it by silently changing who is responsible for it.
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationPeerLinkTable).Projections(query.Project(query.CountAll())).Where(query.And(query.Equal("link_kind", conversationPeerLinkKindDelegation), query.Equal("owner_key", conversationOwner(subjects.execution)), query.Equal("source_conversation_id", d.ConversationID), query.Not(query.In("status", "accepted_delivery", "cancelled", "rejected")))).Build()
		if err != nil {
			return err
		}
		var open int
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return conversationError("conflict", "delegation_transfer_outgoing_active")
		}
		handoff, err := s.prepareDelegationHandoff(ctx, tx, d, in.Request.Transfer.RemainingWork, a)
		if err != nil {
			return err
		}
		if in.Request.ToolRequest != nil {
			handoff.Source = &sdk.ConversationRunReference{ConversationID: in.Request.ToolRequest.ConversationID, RunID: in.Request.ToolRequest.RunID, BeforeStep: in.Request.ToolRequest.Step + 1}
		}
		if conversationHash(handoff) != conversationHash(in.Handoff) {
			return conversationError("conflict", "delegation_handoff_changed")
		}
		if err := s.validateTransferExecutionPublications(ctx, tx, d, handoff.Runs, a, executor); err != nil {
			return err
		}
		dependencies, err := s.resumeDependencyReferences(ctx, tx, d, in.Request.Dependencies, a)
		if err != nil {
			return err
		}
		remaining, err := s.conversationDelegationRemainingBudget(ctx, tx, d, a)
		if err != nil {
			return err
		}
		if remaining.MaxSteps < 1 || remaining.MaxToolCalls < 1 {
			return conversationError("rate_limited", "delegation_budget_exhausted")
		}
		if err = s.checkConversationQueueCapacity(ctx, tx, executor); err != nil {
			return err
		}
		for _, assignment := range assignments {
			executor, err := s.assignmentExecutionAuthority(ctx, tx, d, assignment, a)
			if err != nil {
				return err
			}
			if err = s.insertConversationAssignmentForExecutor(ctx, tx, id, assignment, record, executor); err != nil {
				return err
			}
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		number := int64(len(assignments) + 1)
		conversationID := "conv_" + conversationHash([]any{id, "assignment", number})[:32]
		task := in.Task
		task.ID = "task_" + conversationHash([]any{id, "assignment", number})[:32]
		task.Agent, task.DelegationID, task.ExecutionConversationID = &in.Agent, id, conversationID
		task.Status, task.Budget = sdk.ConversationTaskStatusQueued, remaining
		task.CreatedAt, task.UpdatedAt = now, now
		task.Brief, task.Goal, task.Input = &d.Brief, d.Brief.Goal, d.Input
		task.AgreementRevision = d.AgreementRevision + 1
		task.StructuredInput, task.InputSource, task.Handoff = d.StructuredInput, d.InputSource, &handoff
		task.Requirements = d.Requirements
		task.Dependencies, err = s.flattenTaskDependencies(ctx, tx, dependencies, executor)
		if err != nil {
			return err
		}
		if !validStoredConversationTaskContent(task) {
			return conversationError("bad_request", "task_input_invalid")
		}
		if task.MaxInputBytes > 0 && len(sdk.ConversationTaskPrompt(task)) > task.MaxInputBytes {
			return conversationError("bad_request", "delegation_handoff_exceeded")
		}
		assignment := sdk.ConversationDelegationAssignment{Number: number, AgentID: in.Agent.ID, AgentRevision: in.Agent.Revision, ConversationID: conversationID, TaskID: task.ID, AgreementRevision: task.AgreementRevision, Reason: in.Request.Reason, ActorID: a.UserID, PreviousDelivery: d.Delivery, CreatedAt: now}
		if in.Request.ToolRequest != nil {
			assignment.Source = &sdk.ConversationRunReference{ConversationID: in.Request.ToolRequest.ConversationID, RunID: in.Request.ToolRequest.RunID, BeforeStep: in.Request.ToolRequest.Step + 1}
		}
		conversation := sdk.Conversation{ID: conversationID, DelegationID: id, AgentID: in.Agent.ID, Title: d.Brief.Goal, RuntimeID: executor.RuntimeID, WorkspaceID: executor.WorkspaceID, UserID: executor.UserID, Revision: 1, CreatedAt: now, UpdatedAt: now}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), "_agent_conversations").Columns("owner_key", "conversation_id", "client_id", "request_hash", "title", "updated_at", "archived", "revision", "payload_json").Values(conversationOwner(executor), conversationID, id+"-"+conversationHash(number)[:12], conversationHash(assignment), conversation.Title, now.UnixMilli(), 0, 1, conversationJSON(conversation)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationTaskTable).Columns("record_kind", "owner_key", "workspace_key", "task_id", "runtime_id", "source_conversation_id", "source_run_id", "status", "authority_json", "request_hash", "created_at", "updated_at", "payload_json").Values(conversationTaskKindTask, conversationOwner(executor), conversationHash([]string{executor.RuntimeID, executor.WorkspaceID}), task.ID, executor.RuntimeID, d.SourceConversationID, d.SourceRunID, task.Status, conversationJSON(executor), conversationHash(assignment), now.UnixMilli(), now.UnixMilli(), conversationJSON(task)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		out = d
		out.AssignmentNumber = number
		out.ToAgentID = in.Agent.ID
		out.ConversationID = conversationID
		out.TaskID = task.ID
		out.Model = task.Model
		out.ExecutionSubject = &sdk.ConversationExecutionSubject{RuntimeID: executor.RuntimeID, WorkspaceID: executor.WorkspaceID, UserID: executor.UserID}
		out.AgreementRevision = task.AgreementRevision
		out.Handoff = &handoff
		out.Dependencies = dependencies
		out.PendingChanges = nil
		out.AdoptedAgreementRevision = 0
		out.AdoptedAt = nil
		out.Delivery = nil
		out.Verification = nil
		out.Status = "accepted"
		out.Decision = in.Request.Reason
		out.Revision++
		out.UpdatedAt = now
		if err = s.transferDisagreementResponsibilities(ctx, tx, d, &out, in.Request, a); err != nil {
			return err
		}
		if err = s.saveConversationDelegation(ctx, tx, out, d.Revision, a); err != nil {
			return err
		}
		if err = s.insertConversationAssignmentForExecutor(ctx, tx, id, assignment, record, executor); err != nil {
			return err
		}
		// Supersede both original inbox owners before changing execution routing.
		oldScope := d
		oldScope.AgreementRevision = out.AgreementRevision
		if err := s.supersedePeerMessages(ctx, tx, oldScope, a); err != nil {
			return err
		}
		if err := s.transferDelegationSubjects(ctx, tx, d, subjects, executor); err != nil {
			return err
		}
		if executor != subjects.execution {
			if manager {
				if err := s.validateTransferContractPublications(ctx, tx, d, executor); err != nil {
					return err
				}
			} else {
				if err := s.saveDelegationContractReleases(ctx, tx, out, a); err != nil {
					return err
				}
			}
		}
		if handoff.Source != nil {
			if err := s.saveSourceReleases(ctx, tx, id, "contract", a, executor, []sdk.ConversationRunReference{*handoff.Source}); err != nil {
				return err
			}
		}
		if err = s.saveAgreementRevision(ctx, tx, out, []string{"assignment"}, in.Request.Reason, in.Request.ToolRequest, a); err != nil {
			return err
		}
		if err = s.supersedePeerMessages(ctx, tx, out, a); err != nil {
			return err
		}
		return s.saveCollaborationMutation(ctx, tx, a, key, in.Request, out)
	})
	return out, err
}
