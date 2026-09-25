package agent

import (
	"context"
	"database/sql"
	"errors"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
	"time"
)

// Effects and idempotency receipts share a transaction. The trusted invocation
// must still own its lease and exact approved call when that transaction commits.
func (s *ConversationStore) validatePeerMutation(ctx context.Context, tx *sql.Tx, in *agentsdk.ConversationToolRequest, a agentsdk.ConversationAuthority) error {
	if in == nil {
		return nil
	}
	if conversationOwner(in.Authority) != conversationOwner(a) {
		return conversationError("forbidden", "principal_required")
	}
	claim := persistence.ConversationClaim{Authority: a, Run: agentsdk.ConversationRun{ID: in.RunID, ConversationID: in.ConversationID}, Owner: in.LeaseOwner, Fence: in.Fence}
	row, err := s.claimed(ctx, tx, claim)
	if err != nil {
		return err
	}
	var call persistence.ConversationToolExecution
	found, err := s.readExecutionTool(ctx, tx, claim, in.Step, in.Call.ID, &call)
	if err != nil {
		return err
	}
	if !found || in.IdempotencyKey == "" || in.IdempotencyKey != call.IdempotencyKey || conversationHash(in.Call) != conversationHash(call.Call) || conversationHash(in.Definition) != conversationHash(call.Definition) {
		return conversationError("conflict", "tool_input_conflict")
	}
	known := false
	for _, d := range agentsdk.ConversationCollaborationTools() {
		known = known || d.Effect == "write" && conversationHash(d) == conversationHash(call.Definition)
	}
	if !known {
		return conversationError("forbidden", "tool_access_denied")
	}
	if !agentsdk.ConversationPeerCommunication(call.Call) && !row.Run.WriteScope.Allows(call.Call.Name) {
		record, found, err := s.readInteraction(ctx, tx, claim, in.Step, in.Call.ID, "confirmation")
		if err != nil {
			return err
		}
		i := record.Interaction
		if !found || i.Status != "approved" || i.RespondedBy != a.UserID || i.RespondedAt == nil || i.DefinitionHash != conversationHash(call.Definition) || i.ArgumentsHash != conversationHash(call.Call.Arguments) {
			return conversationError("forbidden", "tool_confirmation_required")
		}
	}
	// This CAS takes the source run lock until commit and fails if cancellation,
	// takeover or another execution event won after validation.
	return s.saveRun(ctx, tx, row, row)
}

// A relationship can be followed to its originating goal without turning
// either participating identity into a parent or child identity.
func (s *ConversationStore) conversationDelegationRoot(ctx context.Context, tx *sql.Tx, source, receiver string, a agentsdk.ConversationAuthority, ancestry ...map[string]bool) (string, error) {
	current := source
	for depth := 0; depth < 8; depth++ {
		c, err := s.get(ctx, tx, current, a)
		if err != nil {
			return "", err
		}
		agent := c.AgentID
		if agent == "" {
			agent = "default"
		}
		if len(ancestry) > 0 {
			ancestry[0][agent] = true
		}
		if c.DelegationID != "" {
			d, err := s.conversationDelegation(ctx, tx, c.DelegationID, a)
			if err != nil {
				return "", err
			}
			if len(ancestry) > 0 {
				ancestry[0][d.ToAgentID] = true
				ancestry[0][d.FromAgentID] = true
			}
			if receiver != "" && d.ToAgentID == receiver {
				return "", conversationError("conflict", "delegation_cycle")
			}
			subjects, found, err := s.delegationSubjects(ctx, tx, d.ID, a)
			if err != nil {
				return "", err
			}
			if found {
				a = subjects.source
			}
			current = d.SourceConversationID
			continue
		}
		if receiver != "" && agent == receiver {
			return "", conversationError("conflict", "delegation_cycle")
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationPeerLinkTable).Columns("payload_json").Where(query.And(query.Equal("link_kind", conversationPeerLinkKindDelegation), query.Equal("owner_key", conversationOwner(a)), query.Equal("conversation_id", current))).Build()
		if err != nil {
			return "", err
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			return current, nil
		} else if err != nil {
			return "", err
		}
		var d agentsdk.ConversationDelegation
		if err = unmarshalDurableJSON(raw, &d); err != nil {
			return "", err
		}
		if len(ancestry) > 0 {
			ancestry[0][d.ToAgentID] = true
			ancestry[0][d.FromAgentID] = true
		}
		current = d.SourceConversationID
	}
	return "", conversationError("rate_limited", "delegation_depth_limit")
}

func (s *ConversationStore) controlDelegationTask(ctx context.Context, tx *sql.Tx, d agentsdk.ConversationDelegation, action string, a agentsdk.ConversationAuthority) error {
	if action == "deliver" || action == "accept_delivery" || action == "review_delivery" || action == "disagreement" {
		return nil
	}
	// The caller's management policy and mutation lease were checked before
	// this internal control. Preserve the accepted execution user's role.
	subjects, err := s.delegationAuthorities(ctx, tx, d, delegationRecordAuthority(d, a))
	if err != nil {
		return err
	}
	a = subjects.execution
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(a), d.TaskID)).Build()
	if err != nil {
		return err
	}
	row, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
	if err != nil {
		return err
	}
	// Same-identity admissions have no subject mapping. The task still owns
	// an immutable accepted execution role; another role managing the task
	// must not become its executor through this control or its next launch.
	if conversationAuthority(row.authority) != nil || row.authority != subjects.execution {
		return conversationError("forbidden", "execution_subject_mismatch")
	}
	a = row.authority
	task := row.task
	if task.ExternalExecution != nil && (action == "pause" || action == "cancel") && !task.ExternalExecution.Capabilities.Cancellation {
		return conversationError("conflict", "external_agent_cancellation_unsupported")
	}
	// Invalidate the agreement without rewriting an observed terminal outcome.
	// Keep its result receipt, but version the affected plan so stale dependent
	// work cannot continue to look verified. Explicit resume replaces the task's
	// execution state below.
	if action == "invalidate_dependencies" && task.Terminal() {
		if task.Plan == nil {
			return nil
		}
		previousUpdated := task.UpdatedAt
		if err = s.supersedeConversationTaskPlan(ctx, tx, conversationOwner(a), &task, d.AgreementRevision, []string{"dependencies"}, "Delegation dependencies changed"); err != nil {
			return err
		}
		task.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("updated_at", task.UpdatedAt.UnixMilli()).Set("payload_json", conversationJSON(task)).Where(query.And(conversationTaskPredicate(conversationOwner(a), task.ID), query.Equal("status", row.task.Status), query.Equal("updated_at", previousUpdated.UnixMilli()))).Build()
		return conversationCAS(ctx, tx, q, args, err)
	}
	if action != "resume" && task.ExternalExecution == nil && task.ExecutionRunID != "" && !task.Terminal() {
		if _, err = s.transitionRun(ctx, tx, d.ConversationID, task.ExecutionRunID, a, false); err != nil {
			return err
		}
	}
	task.Status = agentsdk.ConversationTaskStatusCancelled
	now := time.Now().UTC().Truncate(time.Millisecond)
	task.CompletedAt, task.UpdatedAt = &now, now
	if task.ExternalExecution != nil && action != "resume" {
		task.ExternalExecution.Status = "stop_requested"
		task.ExternalExecution.StopRequested = true
		task.ExternalExecution.StopAcknowledged = false
		task.ExternalExecution.EffectState = ""
		task.ExternalExecution.UpdatedAt = now
	}
	planFields := []string{}
	switch action {
	case "update_brief":
		planFields = conversationPlanBriefFields(task.Brief, d.Brief)
	case "update_input":
		planFields = []string{"input"}
	case "set_dependencies", "invalidate_dependencies":
		planFields = []string{"dependencies"}
	}
	if len(planFields) > 0 {
		if err = s.supersedeConversationTaskPlan(ctx, tx, conversationOwner(a), &task, d.AgreementRevision, planFields, "Delegation requirements changed"); err != nil {
			return err
		}
	}
	if action == "resume" {
		if !row.task.Terminal() {
			return conversationError("conflict", "delegation_still_running")
		}
		// Unknown writes require reconciliation, never a fresh execution that could duplicate effects.
		if task.ExternalExecution != nil {
			if !task.ExternalExecution.Capabilities.Resume {
				return conversationError("conflict", "external_agent_resume_unsupported")
			}
			if !task.ExternalExecution.StopAcknowledged {
				return conversationError("conflict", "external_agent_stop_unconfirmed")
			}
			if task.ExternalExecution.EffectState == "unknown" {
				return conversationError("conflict", "delegation_reconciliation_required")
			}
		} else {
			q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationRunStepTable).Columns("payload_json").Where(query.And(conversationRunStepKindPredicate(conversationRunStepKindTool), conversationScope(a, d.ConversationID))).Build()
			if err != nil {
				return err
			}
			rows, queryErr := tx.QueryContext(ctx, q, args...)
			if queryErr != nil {
				return queryErr
			}
			for rows.Next() {
				var raw []byte
				var call persistence.ConversationToolExecution
				if err = rows.Scan(&raw); err == nil {
					err = unmarshalDurableJSON(raw, &call)
				}
				if err != nil {
					rows.Close()
					return err
				}
				if call.Definition.Effect == "write" && (call.State != "completed" || call.Result == nil || call.Result.Status == "uncertain") {
					rows.Close()
					return conversationError("conflict", "delegation_reconciliation_required")
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
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
		appendConversationTaskPreviousRun(&task)
		task.Brief, task.Goal, task.Budget = &d.Brief, d.Brief.Goal, remaining
		task.AgreementRevision = d.AgreementRevision
		task.StructuredInput, task.InputSource = d.StructuredInput, d.InputSource
		task.Dependencies, err = s.flattenTaskDependencies(ctx, tx, d.Dependencies, a)
		if err != nil {
			return err
		}
		if task.MaxInputBytes > 0 && len(agentsdk.ConversationTaskPrompt(task)) > task.MaxInputBytes {
			return conversationError("bad_request", "task_input_exceeded")
		}
		task.Status, task.ExecutionRunID, task.CompletedAt = agentsdk.ConversationTaskStatusQueued, "", nil
		task.ResultMessageID, task.ErrorCode, task.CompletionEventID, task.CompletionEventSeq = "", "", "", 0
		setConversationTaskGoalPhase(&task, now)
		if task.ExternalExecution != nil {
			task.ExternalExecution.Status = "waiting_claim"
			task.ExternalExecution.SessionID, task.ExternalExecution.ClaimClientID = "", ""
			task.ExternalExecution.Attempt++
			task.ExternalExecution.StopRequested, task.ExternalExecution.StopAcknowledged = false, false
			task.ExternalExecution.EffectState, task.ExternalExecution.ClaimedAt = "", nil
			task.ExternalExecution.UpdatedAt = now
		}
	}
	q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", task.Status).Set("updated_at", now.UnixMilli()).Set("authority_json", conversationJSON(a)).Set("payload_json", conversationJSON(task)).Where(conversationTaskPredicate(conversationOwner(a), task.ID)).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

func (s *ConversationStore) peerAgentCapacity(ctx context.Context, tx *sql.Tx, run conversationRunRow) (bool, error) {
	if run.Run.Agent == nil || run.Run.Agent.ID == "default" {
		return true, nil
	}
	if err := s.lockConversationWorkspaceCapacity(ctx, tx, run.Authority); err != nil {
		return false, err
	}
	agent, err := s.conversationAgent(ctx, tx, run.Run.Agent.ID, run.Authority)
	if err != nil {
		var coded *agentsdk.Error
		if errors.As(err, &coded) && coded.Code == "agent.conversation.agent_not_found" {
			// Let the application claim and terminate a revoked configuration;
			// a missing grant must not poison the queue for unrelated work.
			return true, nil
		}
		return false, err
	}
	if !agent.Enabled || agent.Revision != run.Run.Agent.Revision {
		return true, nil
	}
	// A shared Agent remains one identity. Count its leases across callers in
	// this workspace while exposing none of those callers' execution details.
	scope := query.Or(query.Equal("owner_key", conversationOwner(run.Authority)), query.And(query.Equal("runtime_id", run.Authority.RuntimeID), query.Equal("workspace_id", conversationRunWorkspaceKey(run.Authority))))
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), agentRunTable).Columns("payload_json").Where(query.And(agentRunKindPredicate(agentRunKindConversation), scope, query.Equal("status", "running"), query.GreaterThan("lease_expires_at", time.Now().UnixMilli()))).Build()
	if err != nil {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw []byte
		var item agentsdk.ConversationRun
		if err = rows.Scan(&raw); err != nil {
			return false, err
		}
		if err = unmarshalDurableJSON(raw, &item); err != nil {
			return false, err
		}
		if item.Agent != nil && item.Agent.ID == agent.ID {
			count++
		}
	}
	return count < agent.MaxConcurrent, rows.Err()
}
