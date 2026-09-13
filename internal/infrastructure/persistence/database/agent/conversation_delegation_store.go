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
	"time"
)

func (s *ConversationStore) conversationDelegation(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error) {
	var out agentsdk.ConversationDelegation
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDelegationTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", id))).Build()
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
	normalizeAgreement(&out)
	return out, err
}

func (s *ConversationStore) ConversationDelegation(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error) {
	return s.conversationDelegation(ctx, s.store.Database(), id, a)
}

func (s *ConversationStore) ConversationDelegations(ctx context.Context, conversationID string, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationDelegation, error) {
	out := []agentsdk.ConversationDelegation{}
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	p := query.Equal("owner_key", conversationOwner(a))
	if conversationID != "" {
		c, err := s.get(ctx, s.store.Database(), conversationID, a)
		if err != nil {
			return nil, err
		}
		p = query.And(p, query.Or(query.Equal("source_conversation_id", conversationID), query.Equal("conversation_id", conversationID), query.Equal("delegation_id", c.DelegationID)))
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDelegationTable).Columns("payload_json").Where(p).OrderBy(query.Descending("created_at"), query.Descending("delegation_id")).Limit(101).Build()
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
		normalizeAgreement(&item)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *ConversationStore) CreateConversationDelegation(ctx context.Context, in persistence.ConversationDelegationAdmission, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error) {
	var out agentsdk.ConversationDelegation
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		if err := s.validatePeerMutation(ctx, tx, in.Request.ToolRequest, a); err != nil {
			return err
		}
		key := conversationHash([]string{"delegate", in.Request.ClientID})
		request := []any{in.Request, in.FromAgentID, in.SourceRunID}
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
		if err = s.checkConversationQueueCapacity(ctx, tx, a); err != nil {
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
		}
		rootID, err := s.conversationDelegationRoot(ctx, tx, source.ID, in.Request.AgentID, a)
		if err != nil {
			return err
		}
		// Admission and the limit use the same workspace serialization guard.
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDelegationTable).Projections(query.Project(query.CountAll())).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("root_conversation_id", rootID))).Build()
		if err != nil {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&count); err != nil {
			return err
		}
		if count >= 32 {
			return conversationError("rate_limited", "delegation_limit")
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		id := "delegation_" + conversationHash([]string{conversationOwner(a), in.Request.ClientID})[:32]
		conversationID := "conv_" + conversationHash(id)[:32]
		task := in.Task
		task.ID, task.Status, task.CreatedAt, task.UpdatedAt = "task_"+conversationHash(id)[:32], agentsdk.ConversationTaskStatusQueued, now, now
		task.Agent, task.DelegationID, task.ExecutionConversationID = &in.Agent, id, conversationID
		task.Brief = &in.Request.Brief
		out = agentsdk.ConversationDelegation{ID: id, FromAgentID: in.FromAgentID, ToAgentID: in.Agent.ID, SourceConversationID: source.ID, SourceRunID: in.SourceRunID, ConversationID: conversationID, TaskID: task.ID, Purpose: in.Request.Purpose, Brief: in.Request.Brief, Input: in.Request.Input, Budget: task.Budget, OutputSchema: in.Request.OutputSchema, Status: "accepted", Revision: 1, CreatedAt: now, UpdatedAt: now}
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
		task.Dependencies, err = s.flattenTaskDependencies(ctx, tx, out.Dependencies, a)
		if err != nil {
			return err
		}
		task.AgreementRevision = out.AgreementRevision
		if task.MaxInputBytes > 0 && len(agentsdk.ConversationTaskPrompt(task)) > task.MaxInputBytes {
			return conversationError("bad_request", "task_input_exceeded")
		}
		conversation := agentsdk.Conversation{ID: conversationID, DelegationID: id, AgentID: in.Agent.ID, Title: in.Request.Brief.Goal, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: a.UserID, Revision: 1, CreatedAt: now, UpdatedAt: now}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), "_agent_conversations").Columns("owner_key", "conversation_id", "client_id", "request_hash", "title", "updated_at", "archived", "revision", "payload_json").Values(conversationOwner(a), conversationID, id, conversationHash(out), conversation.Title, now.UnixMilli(), 0, 1, conversationJSON(conversation)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationTaskTable).Columns("owner_key", "workspace_key", "task_id", "runtime_id", "source_conversation_id", "source_run_id", "status", "authority_json", "request_hash", "created_at", "updated_at", "payload_json").Values(conversationOwner(a), conversationHash([]string{a.RuntimeID, a.WorkspaceID}), task.ID, a.RuntimeID, source.ID, in.SourceRunID, task.Status, conversationJSON(a), conversationHash(id), now.UnixMilli(), now.UnixMilli(), conversationJSON(task)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationDelegationTable).Columns("owner_key", "delegation_id", "conversation_id", "source_conversation_id", "root_conversation_id", "status", "revision", "created_at", "payload_json").Values(conversationOwner(a), id, conversationID, source.ID, rootID, out.Status, out.Revision, now.UnixMilli(), conversationJSON(out)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		if err = s.saveAgreementRevision(ctx, tx, out, nil, in.Request.Purpose, in.Request.ToolRequest, a); err != nil {
			return err
		}
		if err = s.insertConversationAssignment(ctx, tx, out.ID, agentsdk.ConversationDelegationAssignment{Number: 1, AgentID: in.Agent.ID, AgentRevision: in.Agent.Revision, ConversationID: out.ConversationID, TaskID: out.TaskID, AgreementRevision: 1, Reason: out.Purpose, ActorID: a.UserID, Source: out.BriefSource, CreatedAt: now}, a); err != nil {
			return err
		}
		return s.saveCollaborationMutation(ctx, tx, a, key, request, out)
	})
	return out, err
}

func (s *ConversationStore) saveConversationDelegation(ctx context.Context, tx *sql.Tx, out agentsdk.ConversationDelegation, expected int64, a agentsdk.ConversationAuthority) error {
	q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationDelegationTable).Set("conversation_id", out.ConversationID).Set("status", out.Status).Set("revision", out.Revision).Set("payload_json", conversationJSON(out)).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", out.ID), query.Equal("revision", expected))).Build()
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
		key := conversationHash([]string{"delegation-update", id, in.ClientID})
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in, &out); err != nil || replay {
			return err
		}
		if err := s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
			return err
		}
		var err error
		out, err = s.conversationDelegation(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if out.Revision != in.ExpectedRevision {
			return conversationError("conflict", "revision_conflict")
		}
		if out.Status == "cancelled" || out.Status == "rejected" || out.Status == "accepted_delivery" && in.Action != "update_brief" && in.Action != "set_dependencies" && in.Action != "update_input" && in.Action != "disagreement" {
			return conversationError("conflict", "delegation_closed")
		}
		if err = s.saveAgreementRevision(ctx, tx, out, nil, "Initial recorded agreement", nil, a); err != nil {
			return err
		}
		if in.Review != nil && in.Action != "accept_delivery" && in.Action != "review_delivery" {
			return conversationError("bad_request", "completion_assessment_invalid")
		}
		if err = s.preserveLegacyDelivery(ctx, tx, out, a); err != nil {
			return err
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
			if in.ToolRequest != nil && in.ToolRequest.ConversationID != out.SourceConversationID {
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
				q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("task_id", out.TaskID))).Build()
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
