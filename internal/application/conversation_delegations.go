package application

import (
	"context"
	"encoding/json"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
	"strings"
	"time"
)

type conversationPeerRequestKey struct{}
type conversationPeerRequest struct {
	ConversationID, RunID string
	ToolRequest           *agentsdk.ConversationToolRequest
}

func validConversationBrief(in agentsdk.ConversationTaskBrief) bool {
	if execution.ValidateCompletionRules(in) != nil {
		return false
	}
	if in.Version < 1 || !conversationText(in.Goal, 2048, true) || !conversationText(in.Deliverable, 2048, true) || !conversationText(in.Audience, 512, false) || len(in.Constraints) > 32 || len(in.CompletionConditions) < 1 || len(in.CompletionConditions) > 32 || len(in.Assumptions) > 32 {
		return false
	}
	for _, group := range [][]string{in.Constraints, in.CompletionConditions, in.Assumptions} {
		for _, text := range group {
			if !conversationText(text, 2048, true) {
				return false
			}
		}
	}
	raw, err := json.Marshal(in)
	return err == nil && len(raw) <= 8192 && (in.DueAt == nil || !in.DueAt.IsZero())
}

func (s *ConversationService) CreateConversationDelegation(ctx context.Context, in agentsdk.ConversationDelegationCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationDetail, error) {
	var out agentsdk.ConversationDelegationDetail
	if err := s.authorizeCollaboration(ctx, "initiate", nil, a); err != nil {
		return out, err
	}
	if err := s.authorizeCollaboration(ctx, "view", nil, a); err != nil {
		return out, err
	}
	if !conversationKey(in.ClientID) || !conversationKey(in.ConversationID) || !conversationKey(in.AgentID) || !conversationText(in.Purpose, 2048, true) || !conversationText(in.Input, 8192, false) || in.Brief.Version != 1 || !validConversationBrief(in.Brief) || len(in.OutputSchema) > 16384 {
		return out, conversationFailure("bad_request", "delegation_invalid")
	}
	if err := validateConversationStructuredInput(in.StructuredInput); err != nil {
		return out, err
	}
	if len(in.OutputSchema) > 0 {
		if _, err := compileConversationSchema(in.OutputSchema); err != nil {
			return out, conversationFailure("bad_request", "delivery_schema_invalid")
		}
	}
	if err := validateAgentRequirements(in.Requirements); err != nil {
		return out, err
	}
	if len(in.Requirements.Tools) > 0 || len(in.Requirements.Skills) > 0 || len(in.Requirements.Sources) > 0 || in.Requirements.MaxModelCost != nil {
		match, err := s.MatchConversationAgents(ctx, agentsdk.ConversationAgentMatchRequest{ConversationID: in.ConversationID, Requirements: in.Requirements}, a)
		if err != nil {
			return out, err
		}
		matched := false
		for _, candidate := range match.Items {
			if candidate.AgentID == in.AgentID && candidate.CanAccept {
				matched = true
			}
		}
		if !matched {
			return out, conversationFailure("conflict", "agent_requirements_unmet")
		}
	}
	if err := s.authorizeConversationExecution(ctx, in.ConversationID, "", "delegate", a); err != nil {
		return out, err
	}
	source, err := s.repo.Get(ctx, in.ConversationID, a)
	if err != nil {
		return out, err
	}
	if source.DelegationID != "" {
		repo, err := s.collaborationRepository()
		if err != nil {
			return out, err
		}
		current, err := repo.ConversationDelegation(ctx, source.DelegationID, a)
		if err != nil {
			return out, err
		}
		if current.ConversationID != source.ID {
			return out, conversationFailure("conflict", "delegation_superseded")
		}
		explicit := false
		for _, ref := range in.Dependencies {
			explicit = explicit || ref.DelegationID == source.DelegationID
		}
		if !explicit {
			repo, err := s.collaborationRepository()
			if err != nil {
				return out, err
			}
			upstream, err := repo.ConversationDelegation(ctx, source.DelegationID, a)
			if err != nil {
				return out, err
			}
			in.Dependencies = append(in.Dependencies, agentsdk.ConversationDependencyInput{DelegationID: upstream.ID, BriefVersion: upstream.Brief.Version, AgreementRevision: upstream.AgreementRevision})
		}
	}
	if err = s.validateDependencySources(ctx, in.Dependencies, "new_peer_conversation", a); err != nil {
		return out, err
	}
	from := source.AgentID
	if from == "" {
		from = "default"
	}
	peer, _ := ctx.Value(conversationPeerRequestKey{}).(conversationPeerRequest)
	if peer.ConversationID != "" && peer.ConversationID != source.ID {
		return out, conversationFailure("forbidden", "delegation_source_invalid")
	}
	in.ToolRequest = peer.ToolRequest
	agent, err := s.freezeConversationAgent(ctx, in.AgentID, a)
	if err != nil {
		return out, err
	}
	if err = s.authorizeCollaboration(ctx, "receive", &agentsdk.ConversationDelegation{FromAgentID: from, ToAgentID: agent.ID}, a); err != nil {
		return out, err
	}
	sourceAgent, err := s.freezeConversationAgent(ctx, from, a)
	if err != nil {
		return out, err
	}
	targetCtx, err := s.selectConversationAgent(ctx, agent, a)
	if err != nil {
		return out, err
	}
	if in.Budget == (agentsdk.ConversationTaskBudget{}) {
		in.Budget = agentsdk.ConversationTaskBudget{MaxSteps: min(12, s.options.MaxSteps), MaxToolCalls: min(12, s.options.MaxToolCalls), MaxOutputBytes: min(8192, s.options.MaxOutputBytes), TimeoutSeconds: int(min(5*time.Minute, s.options.RunTimeout) / time.Second)}
	}
	allowed := []string{}
	for _, key := range agent.Profile.Tools {
		if !strings.HasPrefix(key, "task_") {
			allowed = append(allowed, key)
		}
	}
	task, err := s.prepareConversationTaskStart(targetCtx, agentsdk.ConversationTaskStart{Goal: in.Brief.Goal, Input: in.Input, AllowedTools: allowed, Budget: in.Budget}, a, source.ID, peer.RunID, nil)
	if err != nil {
		return out, err
	}
	task.Brief = &in.Brief
	task.StructuredInput = in.StructuredInput
	task.MaxInputBytes = s.options.MaxInputBytes
	task.Requirements = in.Requirements
	if !conversationText(agentsdk.ConversationTaskPrompt(task), s.options.MaxInputBytes, true) {
		return out, conversationFailure("bad_request", "task_input_exceeded")
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	d, err := repo.CreateConversationDelegation(ctx, persistence.ConversationDelegationAdmission{Request: in, FromAgentID: from, SourceRunID: peer.RunID, SourceAgent: *sourceAgent, Agent: *agent, Task: task}, a)
	if err != nil {
		return out, err
	}
	s.signalConversationTasks()
	return s.projectConversationDelegation(ctx, d, a)
}

func (s *ConversationService) projectConversationDelegation(ctx context.Context, d agentsdk.ConversationDelegation, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationDetail, error) {
	access, accessErr := s.collaborationAccess(ctx, d, a)
	if accessErr != nil {
		return agentsdk.ConversationDelegationDetail{}, accessErr
	}
	if !access.View {
		return agentsdk.ConversationDelegationDetail{}, conversationFailure("forbidden", "collaboration_access_denied")
	}
	if !access.DeliveryRead {
		d.Delivery = nil
		d.Verification = nil
		d.Handoff = nil
		d.DisagreementsOmitted = len(d.Disagreements) > 0
		d.Disagreements = nil
	}
	if !access.ExecutionRead {
		d.Handoff = nil
	}
	deliveryCtx := deliverySourceContext(ctx, d.ID)
	projectDisagreementVerification(&d)
	if err := s.projectDisagreements(deliveryCtx, &d, a, ""); err != nil {
		return agentsdk.ConversationDelegationDetail{}, err
	}

	if d.Delivery != nil && d.Verification == nil {
		if verifier, ok := s.repo.(persistence.ConversationDeliveryVerificationRepository); ok {
			report, err := verifier.PreviewConversationDeliveryVerification(ctx, d.ID, a)
			if err != nil {
				return agentsdk.ConversationDelegationDetail{}, err
			}
			d.Verification = &report
		}
	}
	if err := s.checkVerificationSources(deliveryCtx, d.Verification, a, ""); err != nil {
		// Receipt-backed reviews disclose the same source as their delivery.
		// Keep the existing delegation metadata view when that source is revoked.
		d.Delivery, d.Verification = nil, nil
	}
	out := agentsdk.ConversationDelegationDetail{Access: &access, ConversationDelegation: d, MessagesComplete: true, Messages: []agentsdk.ConversationAgentMessage{}}
	out.SourceAgent = nil
	if err := s.checkHandoffSources(ctx, d.Handoff, a, ""); err != nil {
		return agentsdk.ConversationDelegationDetail{}, err
	}
	if assignments, ok := s.repo.(persistence.ConversationDelegationTransferRepository); ok {
		var err error
		out.Assignments, err = assignments.ConversationDelegationAssignments(ctx, d.ID, a)
		if err != nil {
			return out, err
		}
		for i := range out.Assignments {
			item := &out.Assignments[i]
			if !access.DeliveryRead {
				item.PreviousDelivery = nil
			}
			if item.Source != nil {
				if err = s.checkRunSources(ctx, *item.Source, a); err != nil {
					return agentsdk.ConversationDelegationDetail{}, err
				}
			}
			if item.PreviousDelivery != nil {
				for _, assessment := range item.PreviousDelivery.Conditions {
					if err = s.checkCompletionReceipts(deliveryCtx, assessment.Receipts, a, ""); err != nil {
						item.PreviousDelivery = nil
						break
					}
				}
			}
			if item.PreviousDelivery != nil {
				for _, ref := range item.PreviousDelivery.Evidence {
					if err = s.checkRunSources(deliveryCtx, ref, a); err != nil {
						item.PreviousDelivery = nil
						break
					}
				}
			}
		}
	}
	audit := s.sourceAudit(a)
	if d.InputSource != nil {
		if _, err := audit.run(ctx, *d.InputSource); err != nil {
			return agentsdk.ConversationDelegationDetail{}, err
		}
	}
	if d.BriefSource != nil {
		if _, err := audit.run(ctx, *d.BriefSource); err != nil {
			return agentsdk.ConversationDelegationDetail{}, err
		}
	}
	for _, edge := range d.Dependencies {
		if edge.InputSource != nil {
			if _, err := audit.run(ctx, *edge.InputSource); err != nil {
				return agentsdk.ConversationDelegationDetail{}, err
			}
		}
		if edge.Source != nil {
			if _, err := audit.run(ctx, *edge.Source); err != nil {
				return agentsdk.ConversationDelegationDetail{}, err
			}
		}
	}
	var dependencyErr error
	out.DependencyStates, dependencyErr = s.dependencyStates(ctx, d, a)
	if dependencyErr != nil {
		return out, dependencyErr
	}
	if _, err := s.repo.Get(ctx, d.SourceConversationID, a); err != nil {
		return out, err
	}
	if _, err := s.repo.Get(ctx, d.ConversationID, a); err != nil {
		return out, err
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	if access.Communicate {
		out.Messages, err = repo.ConversationAgentMessages(ctx, d.ID, a)
		if err != nil {
			return out, err
		}
	}
	if len(out.Messages) > 128 {
		out.MessagesComplete = false
		out.Messages = out.Messages[len(out.Messages)-128:]
	}
	if access.ExecutionRead {
		tasks, ok := s.repo.(persistence.ConversationTaskReadRepository)
		if !ok {
			return out, conversationFailure("unavailable", "tasks_unavailable")
		}
		task, err := tasks.ConversationTask(ctx, d.TaskID, a)
		if err != nil {
			return out, err
		}
		detail, err := s.projectConversationTask(ctx, task, a, true)
		if err != nil {
			return out, err
		}
		out.Task = &detail
	}
	for i := range out.Messages {
		m := &out.Messages[i]
		if m.Source != nil {
			if e := s.checkRunSources(ctx, *m.Source, a); e != nil {
				m.Content = "消息来源当前无法验证，内容暂不可查看。"
			}
		}
	}
	if d.Delivery != nil {
		for _, assessment := range d.Delivery.Conditions {
			if err := s.checkCompletionReceipts(deliveryCtx, assessment.Receipts, a, ""); err != nil {
				out.Delivery = nil
				out.Verification = nil
				break
			}
		}
		for _, ref := range d.Delivery.Evidence {
			if err = s.checkRunSources(deliveryCtx, ref, a); err != nil {
				out.Delivery = nil
				out.Verification = nil
				break
			}
		}
	}
	return out, nil
}

func (s *ConversationService) ConversationDelegation(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationDetail, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationDelegationDetail{}, err
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return agentsdk.ConversationDelegationDetail{}, err
	}
	d, err := repo.ConversationDelegation(ctx, id, a)
	if err != nil {
		return agentsdk.ConversationDelegationDetail{}, err
	}
	return s.projectConversationDelegation(ctx, d, a)
}

func (s *ConversationService) ConversationDelegations(ctx context.Context, conversationID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationPage, error) {
	out := agentsdk.ConversationDelegationPage{Items: []agentsdk.ConversationDelegationDetail{}, Complete: true}
	if err := s.authorizeCollaboration(ctx, "view", nil, a); err != nil {
		return out, err
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	items, err := repo.ConversationDelegations(ctx, conversationID, a)
	if err != nil {
		return out, err
	}
	if len(items) > 100 {
		out.Complete, items = false, items[:100]
	}
	for _, d := range items {
		detail, err := s.projectConversationDelegation(ctx, d, a)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, detail)
	}
	return out, nil
}

func (s *ConversationService) UpdateConversationDelegation(ctx context.Context, id string, in agentsdk.ConversationDelegationUpdate, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationDetail, error) {
	var out agentsdk.ConversationDelegationDetail
	if err := s.authorize(a); err != nil {
		return out, err
	}
	if !conversationKey(in.ClientID) || in.ExpectedRevision < 1 || !conversationText(in.Reason, 4096, true) || in.Brief != nil && !validConversationBrief(*in.Brief) {
		return out, conversationFailure("bad_request", "delegation_update_invalid")
	}
	if err := validateConversationStructuredInput(in.StructuredInput); err != nil {
		return out, err
	}
	if in.Dependencies != nil {
		if err := validateDependencyInputs(*in.Dependencies); err != nil {
			return out, err
		}
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	d, err := repo.ConversationDelegation(ctx, id, a)
	if err != nil {
		return out, err
	}
	if collaborationUpdateOperation(in) == "" {
		return out, conversationFailure("bad_request", "delegation_action_invalid")
	}
	if err = s.authorizeCollaboration(ctx, "view", &d, a); err != nil {
		return out, err
	}
	if err = s.authorizeCollaboration(ctx, collaborationUpdateOperation(in), &d, a); err != nil {
		return out, err
	}
	if in.Action == "accept_delivery" || in.Action == "review_delivery" || in.Action == "disagreement" && in.Disagreement != nil && in.Disagreement.Operation == "decide" {
		if err = s.authorizeCollaboration(ctx, "delivery_read", &d, a); err != nil {
			return out, err
		}
	}
	if in.Action == "inspect_outcome" {
		if err = s.authorizeCollaboration(ctx, "execution_read", &d, a); err != nil {
			return out, err
		}
	}
	if in.Action == "update_input" {
		if in.StructuredInput == nil {
			return out, conversationFailure("bad_request", "structured_input_invalid")
		}
		task, err := s.conversationTaskRecord(ctx, d.TaskID, a)
		if err != nil {
			return out, err
		}
		task.StructuredInput = in.StructuredInput
		task.Brief = &d.Brief
		if !conversationText(agentsdk.ConversationTaskPrompt(task), s.options.MaxInputBytes, true) {
			return out, conversationFailure("bad_request", "task_input_exceeded")
		}
	}
	peer, _ := ctx.Value(conversationPeerRequestKey{}).(conversationPeerRequest)
	in.ToolRequest = peer.ToolRequest
	if err = s.validateDisagreementUpdate(ctx, d, in, a, peer.ConversationID); err != nil {
		return out, err
	}

	if in.Review != nil {
		if in.Action != "accept_delivery" && in.Action != "review_delivery" {
			return out, conversationFailure("bad_request", "completion_assessment_invalid")
		}
		if err = s.checkCompletionAssessments(ctx, d.Brief, in.Review.Conditions, a, peer.ConversationID); err != nil {
			return out, err
		}
	}
	if in.Dependencies != nil {
		if err = s.validateDependencySources(ctx, *in.Dependencies, d.ConversationID, a); err != nil {
			return out, err
		}
	}
	if peer.ConversationID != "" {
		if in.Action == "disagreement" {
			if peer.ConversationID != d.SourceConversationID && (peer.ConversationID != d.ConversationID || in.Disagreement.Operation == "decide") {
				return out, conversationFailure("forbidden", "delegation_actor_invalid")
			}
		} else if in.Action == "deliver" || in.Action == "reject" {
			if peer.ConversationID != d.ConversationID {
				return out, conversationFailure("forbidden", "delegation_actor_invalid")
			}
		} else if peer.ConversationID != d.SourceConversationID {
			return out, conversationFailure("forbidden", "delegation_actor_invalid")
		}
	}
	if in.Action == "inspect_outcome" {
		return s.inspectDelegationOutcome(ctx, d, in, a)
	}
	if in.Action == "transfer" {
		return s.transferConversationDelegation(ctx, d, in, a)
	}
	if in.Transfer != nil {
		return out, conversationFailure("bad_request", "delegation_transfer_invalid")
	}
	if in.Inspection != nil {
		return out, conversationFailure("bad_request", "outcome_inspection_invalid")
	}
	if in.Action == "deliver" {
		if peer.ToolRequest != nil && in.Delivery != nil {
			copy := *in.Delivery
			copy.Evidence = append(append([]agentsdk.ConversationRunReference{}, copy.Evidence...), agentsdk.ConversationRunReference{ConversationID: peer.ConversationID, RunID: peer.RunID, BeforeStep: peer.ToolRequest.Step + 1})
			in.Delivery = &copy
		}
		if err = s.validateConversationDelivery(ctx, d, in.Delivery, a); err != nil {
			return out, err
		}
	}
	if in.Action == "accept_delivery" || in.Action == "review_delivery" {
		if err = s.validateConversationDelivery(ctx, d, d.Delivery, a); err != nil {
			return out, err
		}
		if in.Review == nil {
			return out, conversationFailure("bad_request", "completion_review_required")
		}
	}
	if in.Action == "accept_delivery" {
		task, err := s.conversationTaskRecord(ctx, d.TaskID, a)
		if err != nil {
			return out, err
		}
		if task.Status != "completed" {
			return out, conversationFailure("conflict", "delegation_still_running")
		}
	}
	d, err = repo.UpdateConversationDelegation(ctx, id, in, a)
	if err != nil {
		return out, err
	}

	s.signalConversationTasks()
	return s.projectConversationDelegation(ctx, d, a)
}

func (s *ConversationService) validateConversationDelivery(ctx context.Context, d agentsdk.ConversationDelegation, delivery *agentsdk.ConversationDelegationDelivery, a agentsdk.ConversationAuthority) error {
	if delivery != nil && (max(1, delivery.AgreementRevision) != max(1, d.AgreementRevision) || len(d.PendingChanges) > 0) {
		return conversationFailure("bad_request", "delegation_delivery_invalid")
	}
	if delivery == nil || delivery.BriefVersion != d.Brief.Version || !conversationText(delivery.Summary, 8192, true) || len(delivery.Data) > 65536 || len(delivery.Evidence) > 32 || len(delivery.Unresolved) > 32 {
		return conversationFailure("bad_request", "delegation_delivery_invalid")
	}
	for _, issue := range delivery.Unresolved {
		if !conversationText(issue, 2048, true) {
			return conversationFailure("bad_request", "delegation_delivery_invalid")
		}
	}
	peer, _ := ctx.Value(conversationPeerRequestKey{}).(conversationPeerRequest)
	if err := s.checkCompletionAssessments(ctx, d.Brief, delivery.Conditions, a, peer.ConversationID); err != nil {
		return err
	}
	if len(d.OutputSchema) > 0 {
		schema, err := compileConversationSchema(d.OutputSchema)
		if err != nil || validateToolJSON(schema, delivery.Data) != nil {
			return conversationFailure("bad_request", "delivery_schema_invalid")
		}
	} else if len(delivery.Data) > 0 && !json.Valid(delivery.Data) {
		return conversationFailure("bad_request", "delegation_delivery_invalid")
	}
	for _, ref := range delivery.Evidence {
		if _, err := s.repo.Run(ctx, ref.ConversationID, ref.RunID, a); err != nil {
			return err
		}
		if err := s.checkRunSources(ctx, ref, a); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationService) SendConversationAgentMessage(ctx context.Context, id string, in agentsdk.ConversationAgentMessageSend, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgentMessage, error) {
	var out agentsdk.ConversationAgentMessage
	if err := s.authorize(a); err != nil {
		return out, err
	}
	if in.Kind == "" {
		in.Kind = "message"
	}
	if in.DeliveryMode == "" {
		in.DeliveryMode = "next_step"
	}
	if in.AgreementRevision < 0 || in.Kind != "message" && in.Kind != "question" && in.Kind != "reply" || in.DeliveryMode != "next_step" && in.DeliveryMode != "next_run" || in.Kind == "reply" && !conversationKey(in.ReplyToID) || in.Kind != "reply" && in.ReplyToID != "" {
		return out, conversationFailure("bad_request", "agent_message_invalid")
	}
	if !conversationKey(in.ClientID) || !conversationKey(in.ToAgentID) || !conversationText(in.Content, 8192, true) || in.BriefVersion < 1 {
		return out, conversationFailure("bad_request", "agent_message_invalid")
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	peer, _ := ctx.Value(conversationPeerRequestKey{}).(conversationPeerRequest)
	in.ToolRequest = peer.ToolRequest
	d, err := repo.ConversationDelegation(ctx, id, a)
	if err != nil {
		return out, err
	}
	if err = s.authorizeCollaboration(ctx, "communicate", &d, a); err != nil {
		return out, err
	}
	in.ExecutionAgent = nil
	if in.ToAgentID == d.ToAgentID {
		task, err := s.conversationTaskRecord(ctx, d.TaskID, a)
		if err != nil {
			return out, err
		}
		in.ExecutionAgent = task.Agent
	} else if in.ToAgentID == d.FromAgentID {
		in.ExecutionAgent = d.SourceAgent
	}
	if in.ExecutionAgent == nil {
		return out, conversationFailure("forbidden", "agent_message_recipient_invalid")
	}
	if _, err = s.selectConversationAgent(ctx, in.ExecutionAgent, a); err != nil {
		return out, err
	}
	out, err = repo.SendConversationAgentMessage(ctx, id, in, peer.ConversationID, a)
	if err == nil {
		s.signalConversationTasks()
		s.signal()
	}
	return out, err
}

var _ agentsdk.ConversationCollaborationService = (*ConversationService)(nil)
