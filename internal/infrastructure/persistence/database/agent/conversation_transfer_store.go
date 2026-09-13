package agent

import (
	"context"
	"database/sql"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
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
		var err error
		found, err = s.collaborationReplay(ctx, tx, a, transferMutationKey(id, in.ClientID), in, &out)
		return err
	})
	return out, found, err
}

func (s *ConversationStore) TransferConversationDelegation(ctx context.Context, id string, in persistence.ConversationDelegationTransferAdmission, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	var out sdk.ConversationDelegation
	if in.Request.Transfer == nil || in.Request.Action != "transfer" || !personalMemoryKey(in.Request.ClientID) || !executionText(in.Request.Reason, 4096, true) || !executionText(in.Request.Transfer.RemainingWork, 8192, true) {
		return out, conversationError("bad_request", "delegation_transfer_invalid")
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
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in.Request, &out); err != nil || replay {
			return err
		}
		d, err := s.conversationDelegation(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if d.OwnerUserID != a.UserID {
			return conversationError("forbidden", "delegation_actor_invalid")
		}
		_, subjectBound, err := s.delegationSubjects(ctx, tx, d.ID, a)
		if err != nil {
			return err
		}
		if subjectBound {
			// Cross-subject transfer must also move the persisted subject binding
			// and keep per-assignment execution evidence. The existing admission
			// is caller-scoped and cannot safely perform that transition.
			return conversationError("conflict", "execution_subject_mismatch")
		}
		if d.Revision != in.Request.ExpectedRevision {
			return conversationError("conflict", "revision_conflict")
		}
		if d.Status == "cancelled" || d.Status == "accepted_delivery" {
			return conversationError("conflict", "delegation_closed")
		}
		if in.Agent.ID != in.Request.Transfer.AgentID || in.Agent.ID == d.ToAgentID || in.Task.Agent != nil || in.Task.SourceConversationID != d.SourceConversationID {
			return conversationError("bad_request", "delegation_transfer_invalid")
		}
		if in.Request.ToolRequest != nil && in.Request.ToolRequest.ConversationID != d.SourceConversationID {
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
		}
		if _, err = s.conversationDelegationRoot(ctx, tx, d.SourceConversationID, in.Agent.ID, a); err != nil {
			return err
		}
		assignments, err := s.conversationAssignments(ctx, tx, d, a)
		if err != nil {
			return err
		}
		if len(assignments) >= 16 {
			return conversationError("rate_limited", "delegation_transfer_limit")
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("task_id", d.TaskID))).Build()
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
		// Active outgoing work still reports to this assignment's conversation.
		// Do not strand it by silently changing who is responsible for it.
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationDelegationTable).Projections(query.Project(query.CountAll())).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("source_conversation_id", d.ConversationID), query.Not(query.In("status", "accepted_delivery", "cancelled", "rejected")))).Build()
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
		if err = s.checkConversationQueueCapacity(ctx, tx, a); err != nil {
			return err
		}
		for _, assignment := range assignments {
			if err = s.insertConversationAssignment(ctx, tx, id, assignment, a); err != nil {
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
		task.Dependencies, err = s.flattenTaskDependencies(ctx, tx, dependencies, a)
		if err != nil {
			return err
		}
		if task.MaxInputBytes > 0 && len(sdk.ConversationTaskPrompt(task)) > task.MaxInputBytes {
			return conversationError("bad_request", "delegation_handoff_exceeded")
		}
		assignment := sdk.ConversationDelegationAssignment{Number: number, AgentID: in.Agent.ID, AgentRevision: in.Agent.Revision, ConversationID: conversationID, TaskID: task.ID, AgreementRevision: task.AgreementRevision, Reason: in.Request.Reason, ActorID: a.UserID, PreviousDelivery: d.Delivery, CreatedAt: now}
		if in.Request.ToolRequest != nil {
			assignment.Source = &sdk.ConversationRunReference{ConversationID: in.Request.ToolRequest.ConversationID, RunID: in.Request.ToolRequest.RunID, BeforeStep: in.Request.ToolRequest.Step + 1}
		}
		conversation := sdk.Conversation{ID: conversationID, DelegationID: id, AgentID: in.Agent.ID, Title: d.Brief.Goal, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: a.UserID, Revision: 1, CreatedAt: now, UpdatedAt: now}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), "_agent_conversations").Columns("owner_key", "conversation_id", "client_id", "request_hash", "title", "updated_at", "archived", "revision", "payload_json").Values(conversationOwner(a), conversationID, id+"-"+conversationHash(number)[:12], conversationHash(assignment), conversation.Title, now.UnixMilli(), 0, 1, conversationJSON(conversation)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationTaskTable).Columns("owner_key", "workspace_key", "task_id", "runtime_id", "source_conversation_id", "source_run_id", "status", "authority_json", "request_hash", "created_at", "updated_at", "payload_json").Values(conversationOwner(a), conversationHash([]string{a.RuntimeID, a.WorkspaceID}), task.ID, a.RuntimeID, d.SourceConversationID, d.SourceRunID, task.Status, conversationJSON(a), conversationHash(assignment), now.UnixMilli(), now.UnixMilli(), conversationJSON(task)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		out = d
		out.AssignmentNumber = number
		out.ToAgentID = in.Agent.ID
		out.ConversationID = conversationID
		out.TaskID = task.ID
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
		if err = s.preserveLegacyDelivery(ctx, tx, d, a); err != nil {
			return err
		}
		if err = s.saveConversationDelegation(ctx, tx, out, d.Revision, a); err != nil {
			return err
		}
		if err = s.insertConversationAssignment(ctx, tx, id, assignment, a); err != nil {
			return err
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
