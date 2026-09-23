package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
	"github.com/domainry/domainry-orm/query"
	"sort"
	"time"
)

func (s *ConversationStore) ownedConversationDelegation(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error) {
	var out agentsdk.ConversationDelegation
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationPeerLinkTable).Columns("payload_json").Where(query.And(query.Equal("link_kind", conversationPeerLinkKindDelegation), query.Equal("owner_key", conversationOwner(a)), query.Equal("link_id", id), query.Equal("peer_key", ""))).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	if err = db.QueryRowContext(ctx, q, args...).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return out, conversationError("not_found", "delegation_not_found")
	} else if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	out.OwnerUserID = a.UserID
	normalizeAgreement(&out)
	return out, err
}

func (s *ConversationStore) conversationDelegation(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error) {
	d, err := s.ownedConversationDelegation(ctx, db, id, a)
	var coded *agentsdk.Error
	if err == nil || !errors.As(err, &coded) || coded.Code != "agent.conversation.delegation_not_found" {
		return d, err
	}
	return s.executionDelegation(ctx, db, id, a)
}

func (s *ConversationStore) ConversationDelegation(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error) {
	d, _, err := s.participantDelegation(ctx, s.store.Database(), id, a, "view")
	return d, err
}

func (s *ConversationStore) ConversationDelegations(ctx context.Context, conversationID string, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationDelegation, error) {
	out := []agentsdk.ConversationDelegation{}
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	p := query.And(query.Equal("link_kind", conversationPeerLinkKindDelegation), query.Equal("owner_key", conversationOwner(a)))
	conversationDelegationID := ""
	if conversationID != "" {
		c, err := s.get(ctx, s.store.Database(), conversationID, a)
		if err != nil {
			return nil, err
		}
		conversationDelegationID = c.DelegationID
		p = query.And(p, query.Or(query.Equal("source_conversation_id", conversationID), query.Equal("conversation_id", conversationID), query.Equal("link_id", c.DelegationID)))
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationPeerLinkTable).Columns("payload_json").Where(p).OrderBy(query.Descending("created_at"), query.Descending("link_id")).Limit(101).Build()
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
		var item agentsdk.ConversationDelegation
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		item.OwnerUserID = a.UserID
		normalizeAgreement(&item)
		out = append(out, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if conversationID != "" {
		if len(out) == 0 && conversationDelegationID != "" {
			d, err := s.conversationDelegation(ctx, s.store.Database(), conversationDelegationID, a)
			if err != nil {
				return nil, err
			}
			out = append(out, d)
		}
		return out, nil
	}
	shared, err := s.participantDelegations(ctx, a)
	if err != nil {
		return out, err
	}
	out = append(out, shared...)
	received, err := s.executionDelegations(ctx, a)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, d := range out {
		seen[d.ID] = true
	}
	for _, d := range received {
		if !seen[d.ID] {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > 101 {
		out = out[:101]
	}
	return out, nil
}

func (s *ConversationStore) CreateConversationDelegation(ctx context.Context, in persistence.ConversationDelegationAdmission, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error) {
	var out agentsdk.ConversationDelegation
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
		if err := s.validatePeerMutation(ctx, tx, in.Request.ToolRequest, a); err != nil {
			return err
		}
		key := conversationHash([]string{"delegate", in.Request.ClientID})
		request := []any{in.Request, in.FromAgentID, in.SourceRunID}
		if in.ExecutionAuthority != nil {
			request = append(request, executor)
		}
		if replay, err := s.collaborationReplay(ctx, tx, a, key, request, &out); err != nil || replay {
			return err
		}
		source, err := s.get(ctx, tx, in.Request.ConversationID, a)
		if err != nil {
			return err
		}
		if source.DelegationID != "" {
			current, err := s.conversationDelegation(ctx, tx, source.DelegationID, a)
			if err != nil {
				return err
			}
			if current.ConversationID != source.ID {
				return conversationError("conflict", "delegation_superseded")
			}
		}
		from := source.AgentID
		if from == "" {
			from = "default"
		}
		if source.Archived || in.FromAgentID != from || in.Agent.ID != in.Request.AgentID || in.Task.SourceConversationID != source.ID {
			return conversationError("conflict", "delegation_invalid")
		}
		if in.SourceRunID != "" {
			run, err := s.runRow(ctx, tx, source.ID, in.SourceRunID, a)
			if err != nil {
				return err
			}
			if run.Run.Status != "running" {
				return conversationError("conflict", "run_superseded")
			}
		}
		if err = s.checkConversationQueueCapacity(ctx, tx, executor); err != nil {
			return err
		}
		if err = s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
			return err
		}
		if in.Agent.ID != "default" {
			current, err := s.conversationAgent(ctx, tx, in.Agent.ID, a)
			if err != nil {
				return err
			}
			if !current.Enabled || current.Revision != in.Agent.Revision {
				return conversationError("conflict", "agent_changed")
			}
			if in.Agent.DelegationRoleKey != "" && (in.ExecutionAuthority == nil || current.DelegationExecution != "owner" || current.OwnerUserID != executor.UserID || current.DelegationRoleKey != executor.RoleKey || in.Agent.DelegationRoleKey != executor.RoleKey) {
				return conversationError("forbidden", "execution_subject_mismatch")
			}
			if current.Shared && in.Agent.ExecutionSubject == nil || in.Agent.OwnerUserID != "" && in.Agent.OwnerUserID != current.OwnerUserID {
				return conversationError("forbidden", "execution_subject_mismatch")
			}
			if conversationOwner(executor) != conversationOwner(a) && (!current.Shared || current.OwnerUserID != executor.UserID || in.Agent.OwnerUserID != current.OwnerUserID) {
				return conversationError("forbidden", "execution_subject_mismatch")
			}
		} else if conversationOwner(executor) != conversationOwner(a) {
			return conversationError("forbidden", "execution_subject_mismatch")
		}
		if in.Agent.ExecutionSubject != nil && *in.Agent.ExecutionSubject != (agentsdk.ConversationExecutionSubject{RuntimeID: executor.RuntimeID, WorkspaceID: executor.WorkspaceID, UserID: executor.UserID}) {
			return conversationError("forbidden", "execution_subject_mismatch")
		}
		rootID, err := s.conversationDelegationRoot(ctx, tx, source.ID, in.Request.AgentID, a)
		if err != nil {
			return err
		}
		workBudget, workUsage, err := s.admitConversationWorkBudget(ctx, tx, rootID, in.Request.WorkBudget, a)
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		id := "delegation_" + conversationHash([]string{conversationOwner(a), in.Request.ClientID})[:32]
		conversationID := "conv_" + conversationHash(id)[:32]
		task := in.Task
		task.ID, task.Status, task.CreatedAt, task.UpdatedAt = "task_"+conversationHash(id)[:32], agentsdk.ConversationTaskStatusQueued, now, now
		task.Agent, task.DelegationID, task.ExecutionConversationID = &in.Agent, id, conversationID
		task.Brief = &in.Request.Brief
		out = agentsdk.ConversationDelegation{ID: id, FromAgentID: in.FromAgentID, ToAgentID: in.Agent.ID, SourceConversationID: source.ID, SourceRunID: in.SourceRunID, ConversationID: conversationID, TaskID: task.ID, Model: task.Model, ModelExplicit: in.Request.Model != nil, Purpose: in.Request.Purpose, Brief: in.Request.Brief, Input: in.Request.Input, Budget: task.Budget, WorkBudget: &workBudget, WorkUsage: &workUsage, OutputSchema: in.Request.OutputSchema, Status: "accepted", Revision: 1, CreatedAt: now, UpdatedAt: now}
		out.OwnerUserID = a.UserID
		out.ExecutionSubject = &agentsdk.ConversationExecutionSubject{RuntimeID: executor.RuntimeID, WorkspaceID: executor.WorkspaceID, UserID: executor.UserID}
		out.SourceAgent, out.RootConversationID = &in.SourceAgent, rootID
		out.Requirements = in.Request.Requirements
		out.StructuredInput = in.Request.StructuredInput
		task.StructuredInput = in.Request.StructuredInput
		out.AgreementRevision = 1
		out.AssignmentNumber = 1
		if in.Request.ToolRequest != nil {
			out.BriefSource = &agentsdk.ConversationRunReference{ConversationID: in.Request.ToolRequest.ConversationID, RunID: in.Request.ToolRequest.RunID, BeforeStep: in.Request.ToolRequest.Step + 1}
		}
		out.InputSource = out.BriefSource
		task.InputSource = out.InputSource
		refs := append([]agentsdk.ConversationDependencyInput{}, in.Request.Dependencies...)
		if source.DelegationID != "" {
			explicit := false
			for _, ref := range refs {
				explicit = explicit || ref.DelegationID == source.DelegationID
			}
			if !explicit {
				upstream, err := s.conversationDelegation(ctx, tx, source.DelegationID, a)
				if err != nil {
					return err
				}
				refs = append(refs, agentsdk.ConversationDependencyInput{DelegationID: source.DelegationID, BriefVersion: upstream.Brief.Version, AgreementRevision: upstream.AgreementRevision})
			}
		}
		out.Dependencies, err = s.freezeTaskDependencies(ctx, tx, out.ID, rootID, refs, a)
		if err != nil {
			return err
		}
		task.Dependencies, err = s.flattenTaskDependencies(ctx, tx, out.Dependencies, executor)
		if err != nil {
			return err
		}
		task.AgreementRevision = out.AgreementRevision
		if !validStoredConversationTaskContent(task) {
			return conversationError("bad_request", "task_input_invalid")
		}
		if task.MaxInputBytes > 0 && len(agentsdk.ConversationTaskPrompt(task)) > task.MaxInputBytes {
			return conversationError("bad_request", "task_input_exceeded")
		}
		conversation := agentsdk.Conversation{ID: conversationID, DelegationID: id, AgentID: in.Agent.ID, Title: in.Request.Brief.Goal, RuntimeID: executor.RuntimeID, WorkspaceID: executor.WorkspaceID, UserID: executor.UserID, Revision: 1, CreatedAt: now, UpdatedAt: now}
		q, args, err := query.NewInsertBuilder(s.store.Renderer(), "_agent_conversations").Columns("owner_key", "conversation_id", "client_id", "request_hash", "title", "updated_at", "archived", "revision", "payload_json").Values(conversationOwner(executor), conversationID, id, conversationHash(out), conversation.Title, now.UnixMilli(), 0, 1, conversationJSON(conversation)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationTaskTable).Columns("record_kind", "owner_key", "workspace_key", "task_id", "runtime_id", "source_conversation_id", "source_run_id", "status", "authority_json", "request_hash", "created_at", "updated_at", "payload_json").Values(conversationTaskKindTask, conversationOwner(executor), conversationHash([]string{executor.RuntimeID, executor.WorkspaceID}), task.ID, executor.RuntimeID, source.ID, in.SourceRunID, task.Status, conversationJSON(executor), conversationHash(id), now.UnixMilli(), now.UnixMilli(), conversationJSON(task)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationPeerLinkTable).Columns("link_kind", "owner_key", "link_id", "scope_key", "conversation_id", "source_conversation_id", "root_conversation_id", "status", "revision", "created_at", "updated_at", "payload_json").Values(conversationPeerLinkKindDelegation, conversationOwner(a), id, conversationID, conversationID, source.ID, rootID, out.Status, out.Revision, now.UnixMilli(), now.UnixMilli(), conversationJSON(out)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		if err = s.saveDelegationSubjects(ctx, tx, id, a, executor, now); err != nil {
			return err
		}
		if err := s.saveDelegationContractReleases(ctx, tx, out, a); err != nil {
			return err
		}
		if err = s.saveAgreementRevision(ctx, tx, out, nil, in.Request.Purpose, in.Request.ToolRequest, a); err != nil {
			return err
		}
		if err = s.insertConversationAssignmentForExecutor(ctx, tx, out.ID, agentsdk.ConversationDelegationAssignment{Number: 1, AgentID: in.Agent.ID, AgentRevision: in.Agent.Revision, ConversationID: out.ConversationID, TaskID: out.TaskID, AgreementRevision: 1, Reason: out.Purpose, ActorID: a.UserID, Source: out.BriefSource, CreatedAt: now}, a, executor); err != nil {
			return err
		}
		return s.saveCollaborationMutation(ctx, tx, a, key, request, out)
	})
	return out, err
}

func (s *ConversationStore) saveConversationDelegation(ctx context.Context, tx *sql.Tx, out agentsdk.ConversationDelegation, expected int64, a agentsdk.ConversationAuthority) error {
	if out.BriefSource != nil || out.InputSource != nil || len(out.Requirements.Sources) > 0 {
		previous, err := s.ownedConversationDelegation(ctx, tx, out.ID, delegationRecordAuthority(out, a))
		if err != nil {
			return err
		}
		// Publishing is tied to a new/changed source, not every metadata CAS.
		// Unavailable historical sources must not prevent pause/cancel/transfer
		// or replace their provenance with the current caller's role.
		if conversationHash([]any{out.BriefSource, out.InputSource, out.Requirements.Sources}) != conversationHash([]any{previous.BriefSource, previous.InputSource, previous.Requirements.Sources}) {
			if err := s.saveDelegationContractReleases(ctx, tx, out, a); err != nil {
				return err
			}
		}
	}
	if out.OwnerUserID != "" && out.OwnerUserID != a.UserID {
		if _, err := s.executionDelegation(ctx, tx, out.ID, a); err != nil {
			current, _, managementErr := s.participantDelegation(ctx, tx, out.ID, a, "manage")
			if managementErr != nil || !participantManagement(current, a) {
				return err
			}
		}
	}
	q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationPeerLinkTable).Set("scope_key", out.ConversationID).Set("conversation_id", out.ConversationID).Set("status", out.Status).Set("revision", out.Revision).Set("updated_at", out.UpdatedAt.UnixMilli()).Set("payload_json", conversationJSON(out)).Where(query.And(query.Equal("link_kind", conversationPeerLinkKindDelegation), query.Equal("owner_key", conversationOwner(delegationRecordAuthority(out, a))), query.Equal("link_id", out.ID), query.Equal("peer_key", ""), query.Equal("revision", expected))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

func (s *ConversationStore) UpdateConversationDelegation(ctx context.Context, id string, in agentsdk.ConversationDelegationUpdate, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error) {
	var out agentsdk.ConversationDelegation
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		if err := s.validatePeerMutation(ctx, tx, in.ToolRequest, a); err != nil {
			return err
		}
		if err := s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
			return err
		}
		var err error
		out, _, err = s.participantDelegation(ctx, tx, id, a, "manage")
		if err != nil {
			return err
		}
		manager := out.OwnerUserID != a.UserID && participantManagement(out, a)
		executorPublication := false
		if out.OwnerUserID != a.UserID && in.Action == "republish_delivery" {
			_, executionErr := s.executionDelegation(ctx, tx, id, a)
			executorPublication = executionErr == nil
		}
		if out.OwnerUserID != a.UserID && in.Action != "deliver" && in.Action != "reject" && !executorPublication && (!manager || !collaborationManagementOperation(in.Action)) {
			return conversationError("forbidden", "delegation_actor_invalid")
		}
		if out.OwnerUserID != a.UserID && (in.Action == "deliver" || in.Action == "reject") {
			if _, err := s.executionDelegation(ctx, tx, id, a); err != nil {
				return conversationError("forbidden", "delegation_actor_invalid")
			}
		}
		if manager && (in.Action == "accept_delivery" || in.Action == "review_delivery" || in.Action == "disagreement" && in.Disagreement != nil && in.Disagreement.Operation == "decide") {
			if _, allowed := delegationParticipant(out, a.UserID, "delivery_read"); !allowed {
				// The admitted receiver has independent delivery-read access.
				// Its additional management grant must not erase that access or
				// lend it to another participant. Current role policy is checked
				// by the application before this transaction.
				if _, executionErr := s.executionDelegation(ctx, tx, id, a); executionErr != nil {
					var coded *agentsdk.Error
					if errors.As(executionErr, &coded) && coded.Code == "agent.conversation.delegation_not_found" {
						return conversationError("forbidden", "collaboration_access_denied")
					}
					return executionErr
				}
			}
		}
		key := conversationHash([]string{"delegation-update", id, in.ClientID})
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in, &out); err != nil || replay {
			return err
		}
		if in.Action == "republish_contract" {
			out, err = s.republishContract(ctx, tx, out, in, a)
			if err != nil {
				return err
			}
			return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
		}
		if in.ContractPublication != nil {
			return conversationError("bad_request", "contract_publication_invalid")
		}
		if in.Action == "republish_delivery" {
			out, err = s.republishDelivery(ctx, tx, out, in, a)
			if err != nil {
				return err
			}
			return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
		}
		if in.Publication != nil {
			return conversationError("bad_request", "delivery_publication_invalid")
		}
		if out.ExecutionSubject != nil && out.ExecutionSubject.UserID != out.OwnerUserID && (in.Action == "deliver" || in.Action == "reject") && a.UserID != out.ExecutionSubject.UserID {
			return conversationError("forbidden", "delegation_actor_invalid")
		}
		if out.Revision != in.ExpectedRevision {
			return conversationError("conflict", "revision_conflict")
		}
		if out.Status == "cancelled" || out.Status == "rejected" || out.Status == "accepted_delivery" && in.Action != "update_brief" && in.Action != "set_dependencies" && in.Action != "update_input" && in.Action != "disagreement" {
			return conversationError("conflict", "delegation_closed")
		}
		if in.Review != nil && in.Action != "accept_delivery" && in.Action != "review_delivery" {
			return conversationError("bad_request", "completion_assessment_invalid")
		}
		if in.Disagreement != nil && in.Action != "disagreement" {
			return conversationError("bad_request", "disagreement_invalid")
		}
		fields := []string{}
		agreementChanged := false
		switch in.Action {
		case "disagreement":
			if err = s.applyDisagreement(ctx, tx, &out, in, a); err != nil {
				return err
			}
		case "accept_delivery", "review_delivery":
			if in.ToolRequest != nil && !manager && in.ToolRequest.ConversationID != out.SourceConversationID {
				return conversationError("forbidden", "delegation_actor_invalid")
			}
			if out.Status != "delivered" || out.Delivery == nil || out.Delivery.BriefVersion != out.Brief.Version || out.Delivery.AgreementRevision != out.AgreementRevision || len(out.PendingChanges) > 0 {
				return conversationError("conflict", "delegation_delivery_invalid")
			}
			if in.Review == nil {
				return conversationError("bad_request", "completion_review_required")
			}
			report, err := s.verifyDelegationDelivery(ctx, tx, out, *out.Delivery, in.Review, in.ToolRequest, a)
			if err != nil {
				return err
			}
			out.Verification = &report
			if in.Action == "accept_delivery" {
				if !report.Ready {
					return conversationError("conflict", "completion_conditions_unmet")
				}
				executor, err := s.delegationExecutionAuthority(ctx, tx, out, a)
				if err != nil {
					return err
				}
				q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(executor), out.TaskID)).Build()
				if err != nil {
					return err
				}
				task, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
				if err != nil {
					return err
				}
				if task.task.Status != "completed" {
					return conversationError("conflict", "delegation_still_running")
				}
				if _, err = s.prepareDelegationHandoff(ctx, tx, out, "Acceptance evidence check", a); err != nil {
					return err
				}
				out.Status = "accepted_delivery"
			}
		case "deliver":
			if out.Status != "running" && out.Status != "awaiting_delivery" && out.Status != "delivered" {
				return conversationError("conflict", "delegation_transition_invalid")
			}
			if in.Delivery == nil || in.Delivery.BriefVersion != out.Brief.Version || len(out.PendingChanges) > 0 {
				return conversationError("conflict", "delegation_delivery_invalid")
			}
			delivery := *in.Delivery
			if delivery.AgreementRevision == 0 && out.AgreementRevision == 1 {
				delivery.AgreementRevision = 1
			}
			if delivery.AgreementRevision != out.AgreementRevision {
				return conversationError("conflict", "delegation_delivery_invalid")
			}
			if in.ToolRequest != nil {
				row, err := s.runRow(ctx, tx, in.ToolRequest.ConversationID, in.ToolRequest.RunID, a)
				if err != nil {
					return err
				}
				if row.Run.BackgroundTask == nil || max(1, row.Run.BackgroundTask.AgreementRevision) != out.AgreementRevision {
					return conversationError("conflict", "delegation_superseded")
				}
			}
			report, err := s.verifyDelegationDelivery(ctx, tx, out, delivery, nil, in.ToolRequest, a)
			if err != nil {
				return err
			}
			out.Delivery, out.Verification, out.Status = &delivery, &report, "delivered"
		case "request_changes":
			out.Status = "needs_changes"
		case "update_brief":
			if in.Brief == nil || in.Brief.Version != out.Brief.Version+1 {
				return conversationError("conflict", "brief_version_invalid")
			}
			fields = execution.ChangedBriefFields(out.Brief, *in.Brief)
			if len(fields) == 0 {
				return conversationError("bad_request", "brief_unchanged")
			}
			out.Brief = *in.Brief
			out.Status = "needs_update"
			out.BriefSource = nil
			if in.ToolRequest != nil {
				out.BriefSource = &agentsdk.ConversationRunReference{ConversationID: in.ToolRequest.ConversationID, RunID: in.ToolRequest.RunID, BeforeStep: in.ToolRequest.Step + 1}
			}
			agreementChanged = true
		case "update_input":
			if in.StructuredInput == nil {
				return conversationError("bad_request", "structured_input_invalid")
			}
			if conversationHash(in.StructuredInput) == conversationHash(out.StructuredInput) {
				return conversationError("bad_request", "brief_unchanged")
			}
			out.StructuredInput = in.StructuredInput
			out.InputSource = nil
			if in.ToolRequest != nil {
				out.InputSource = &agentsdk.ConversationRunReference{ConversationID: in.ToolRequest.ConversationID, RunID: in.ToolRequest.RunID, BeforeStep: in.ToolRequest.Step + 1}
			}
			out.Status = "needs_update"
			fields = []string{"input"}
			agreementChanged = true
		case "set_dependencies":
			if in.Dependencies == nil {
				return conversationError("bad_request", "dependencies_invalid")
			}
			out.Dependencies, err = s.freezeTaskDependencies(ctx, tx, out.ID, out.RootConversationID, *in.Dependencies, a)
			if err != nil {
				return err
			}
			out.Status = "needs_update"
			fields = []string{"dependencies"}
			agreementChanged = true
		case "pause":
			out.Status = "paused"
		case "cancel":
			out.Status = "cancelled"
		case "reject":
			out.Status = "rejected"
		case "resume":
			if out.Status != "paused" && out.Status != "needs_changes" && out.Status != "needs_update" && out.Status != "failed" {
				return conversationError("conflict", "delegation_transition_invalid")
			}
			dependencies, err := s.resumeDependencyReferences(ctx, tx, out, in.Dependencies, a)
			if err != nil {
				return err
			}
			if conversationHash(dependencies) != conversationHash(out.Dependencies) {
				out.Dependencies = dependencies
				agreementChanged = true
				fields = []string{"dependencies"}
			}
			out.PendingChanges = nil
			out.Status = "accepted"
		default:
			return conversationError("bad_request", "delegation_action_invalid")
		}
		if in.StructuredInput != nil && in.Action != "update_input" {
			return conversationError("bad_request", "structured_input_invalid")
		}
		if in.Dependencies != nil && in.Action != "set_dependencies" && in.Action != "resume" {
			return conversationError("bad_request", "dependencies_invalid")
		}
		if agreementChanged {
			out.AgreementRevision++
		}
		out.Decision, out.Revision, out.UpdatedAt = in.Reason, out.Revision+1, time.Now().UTC().Truncate(time.Millisecond)
		if agreementChanged && in.Action != "resume" {
			pendingFields := fields
			pending := []agentsdk.ConversationRequirementChange{}
			for _, change := range out.PendingChanges {
				if change.SourceDelegationID != out.ID {
					pending = append(pending, change)
				} else {
					pendingFields = mergeRequirementFields(pendingFields, change.ChangedFields)
				}
			}
			out.PendingChanges = append(pending, agentsdk.ConversationRequirementChange{ID: "change_" + conversationHash([]any{out.ID, out.AgreementRevision})[:32], SourceDelegationID: out.ID, SourceBriefVersion: out.Brief.Version, SourceAgreementRevision: out.AgreementRevision, ChangedFields: pendingFields, CreatedAt: out.UpdatedAt})
		}
		if err = s.saveConversationDelegation(ctx, tx, out, in.ExpectedRevision, a); err != nil {
			return err
		}
		if in.Action == "deliver" || in.Action == "accept_delivery" || in.Action == "review_delivery" {
			if err = s.saveDeliveryRecord(ctx, tx, out, in.Action, in.Reason, a); err != nil {
				return err
			}
		}
		if err = s.controlDelegationTask(ctx, tx, out, in.Action, a); err != nil {
			return err
		}
		if agreementChanged {
			if err = s.supersedePeerMessages(ctx, tx, out, a); err != nil {
				return err
			}
		}
		if agreementChanged {
			if err = s.saveAgreementRevision(ctx, tx, out, fields, in.Reason, in.ToolRequest, a); err != nil {
				return err
			}
			if err = s.propagateRequirementChange(ctx, tx, out, fields, a); err != nil {
				return err
			}
			if in.Action != "resume" {
				if err = s.requirementChangeNotices(ctx, tx, out, out.PendingChanges[len(out.PendingChanges)-1], requirementChangeSource(out, fields), a); err != nil {
					return err
				}
			}
		}
		if in.Action == "reject" {
			notice := agentsdk.ConversationAgentMessage{ID: "amsg_" + conversationHash([]any{out.ID, out.Revision, "rejected"})[:32], DelegationID: out.ID, FromAgentID: out.ToAgentID, ToAgentID: out.FromAgentID, ConversationID: out.SourceConversationID, Kind: "task_rejected", Content: "The receiving Agent rejected delegation " + out.ID + ". Inspect the recorded decision and arrange the remaining work.", BriefVersion: out.Brief.Version, AgreementRevision: out.AgreementRevision, CreatedAt: out.UpdatedAt}
			if err = s.insertConversationPeerNotice(ctx, tx, notice, out.SourceAgent, a); err != nil {
				return err
			}
		}
		return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
	})
	return out, err
}

func collaborationManagementOperation(action string) bool {
	switch action {
	case "pause", "cancel", "resume", "update_brief", "update_input", "set_dependencies", "request_changes", "accept_delivery", "review_delivery", "disagreement":
		return true
	}
	return false
}

func (s *ConversationStore) finishConversationDelegation(ctx context.Context, tx *sql.Tx, task agentsdk.ConversationTask, run agentsdk.ConversationRun, a agentsdk.ConversationAuthority) error {
	if task.DelegationID == "" {
		return nil
	}
	d, err := s.conversationDelegation(ctx, tx, task.DelegationID, a)
	if err != nil {
		return err
	}
	if d.TaskID != task.ID || d.Status != "accepted" && d.Status != "running" && d.Status != "delivered" {
		return nil
	}
	version := d.Revision
	if run.Status == "completed" {
		if d.Status != "delivered" {
			d.Status = "awaiting_delivery"
		}
	} else {
		d.Status = "failed"
	}
	if run.Status == "cancelled" {
		d.Status = "paused"
	}
	d.Revision, d.UpdatedAt = version+1, time.Now().UTC().Truncate(time.Millisecond)
	if err = s.saveConversationDelegation(ctx, tx, d, version, a); err != nil {
		return err
	}
	message := agentsdk.ConversationAgentMessage{ID: "amsg_" + conversationHash([]any{d.ID, run.ID, "terminal"})[:32], DelegationID: d.ID, FromAgentID: d.ToAgentID, ToAgentID: d.FromAgentID, ConversationID: d.SourceConversationID, Kind: "task_" + run.Status, BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, Content: "Delegation " + d.ID + " has a new recorded outcome: " + d.Status + ". Inspect delegation_get for the task, actual execution evidence and any submitted delivery before accepting or summarizing it.", CreatedAt: d.UpdatedAt}
	return s.insertConversationPeerNotice(ctx, tx, message, d.SourceAgent, a)
}
