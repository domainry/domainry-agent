package application

import (
	"context"
	"encoding/json"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
	"slices"
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
	agent, executor, err := s.delegationExecutionAgent(ctx, in.AgentID, a)
	if err != nil {
		return out, err
	}
	if err := s.authorizeCollaboration(delegationExecutorContext(ctx), "receive", &agentsdk.ConversationDelegation{OwnerUserID: a.UserID, FromAgentID: from, ToAgentID: agent.ID, ExecutionSubject: executionSubject(executor)}, executor); err != nil {
		return out, err
	}
	sourceAgent, err := s.freezeConversationAgent(ctx, from, a)
	if err != nil {
		return out, err
	}
	targetCtx, err := s.selectConversationAgent(delegationExecutorContext(ctx), agent, executor)
	if err != nil {
		return out, err
	}
	if err := s.checkProspectiveDelegationSources(targetCtx, in.Requirements.Sources, a, executor); err != nil {
		return out, conversationFailure("forbidden", "delegation_contract_source_unavailable")
	}
	if executor != a {
		if err := s.authorizeConversationExecution(targetCtx, source.ID, "", "delegate", executor); err != nil {
			return out, err
		}
		if err := s.validateDependencySources(targetCtx, in.Dependencies, "new_peer_conversation", executor); err != nil {
			return out, err
		}
	}
	if peer.RunID != "" {
		ref := agentsdk.ConversationRunReference{ConversationID: source.ID, RunID: peer.RunID}
		if peer.ToolRequest != nil {
			ref.BeforeStep = peer.ToolRequest.Step + 1
		}
		releaseCtx, err := s.sourceReleaseContext(targetCtx, "contract", "", a, executor, []agentsdk.ConversationRunReference{ref})
		if err != nil {
			return out, err
		}
		if err := s.checkRunSources(releaseCtx, ref, executor); err != nil {
			return out, conversationFailure("forbidden", "delegation_contract_source_unavailable")
		}
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
	authorizationConversation, authorizationRun := source.ID, peer.RunID
	if executor.UserID != a.UserID {
		// The recipient conversation does not exist until admission commits.
		// Check tool grants without presenting the issuer's private conversation
		// as an execution resource owned by the recipient.
		authorizationConversation, authorizationRun = "", ""
	}
	task, err := s.prepareConversationTaskStart(targetCtx, agentsdk.ConversationTaskStart{Goal: in.Brief.Goal, Input: in.Input, AllowedTools: allowed, Budget: in.Budget}, executor, authorizationConversation, authorizationRun, nil)
	if err != nil {
		return out, err
	}
	task.SourceConversationID, task.SourceRunID = source.ID, peer.RunID
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
	admission := persistence.ConversationDelegationAdmission{Request: in, FromAgentID: from, SourceRunID: peer.RunID, SourceAgent: *sourceAgent, Agent: *agent, Task: task}
	if agent.DelegationRoleKey != "" {
		admission.ExecutionAuthority = &executor
	}
	d, err := repo.CreateConversationDelegation(ctx, admission, a)
	if err != nil {
		return out, err
	}
	s.signalConversationTasks()
	return s.projectConversationDelegation(ctx, d, a)
}

func (s *ConversationService) projectConversationDelegation(ctx context.Context, d agentsdk.ConversationDelegation, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationDetail, error) {
	if d.SubjectExited {
		return s.projectUnavailableDelegation(ctx, d, a)
	}
	out, err := s.projectConversationDelegationContent(ctx, d, a)
	if err == nil || !collaborationDenied(err) {
		return out, err
	}
	// A source denial must leave a recovery route for a currently authorized
	// viewer. No source-derived text, tasks, messages or deliveries survive.
	return s.projectUnavailableDelegation(ctx, d, a)
}

func (s *ConversationService) projectUnavailableDelegation(ctx context.Context, d agentsdk.ConversationDelegation, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationDetail, error) {
	access, accessErr := s.collaborationAccess(ctx, d, a)
	if accessErr != nil {
		return agentsdk.ConversationDelegationDetail{}, accessErr
	}
	if !access.View {
		return agentsdk.ConversationDelegationDetail{}, conversationFailure("forbidden", "collaboration_access_denied")
	}
	if d.SubjectExited {
		access.Receive, access.ExecutionRead, access.DeliveryRead = false, false, false
	}
	sourceConversationID, conversationID := d.SourceConversationID, d.ConversationID
	if d.OwnerUserID != "" && d.OwnerUserID != a.UserID && !delegationExecutor(d, a) {
		sourceConversationID, conversationID = "", ""
	}
	return agentsdk.ConversationDelegationDetail{ContractOmitted: true, Access: &access, Messages: []agentsdk.ConversationAgentMessage{}, ConversationDelegation: agentsdk.ConversationDelegation{
		ID: d.ID, SubjectExited: d.SubjectExited, OwnerUserID: d.OwnerUserID, ExecutionSubject: d.ExecutionSubject, FromAgentID: d.FromAgentID, ToAgentID: d.ToAgentID, SourceConversationID: sourceConversationID, ConversationID: conversationID, Status: d.Status, Revision: d.Revision, AgreementRevision: d.AgreementRevision, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}}, nil
}

func (s *ConversationService) projectConversationDelegationContent(ctx context.Context, d agentsdk.ConversationDelegation, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationDetail, error) {
	access, accessErr := s.collaborationAccess(ctx, d, a)
	if accessErr != nil {
		return agentsdk.ConversationDelegationDetail{}, accessErr
	}
	if !access.View {
		return agentsdk.ConversationDelegationDetail{}, conversationFailure("forbidden", "collaboration_access_denied")
	}
	if d.OwnerUserID != "" && d.OwnerUserID != a.UserID && !delegationExecutor(d, a) {
		return s.projectParticipantDelegation(ctx, d, access, a)
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
	deliveryCtx, deliveryErr := s.delegationDeliverySourceContext(ctx, d, a)
	if deliveryErr != nil {
		return agentsdk.ConversationDelegationDetail{}, deliveryErr
	}
	contractCtx, contractErr := s.delegationContractSourceContext(ctx, d, a)
	if contractErr != nil {
		return agentsdk.ConversationDelegationDetail{}, contractErr
	}
	contractAudit := s.sourceAudit(a)
	contractRoots := d.Requirements.Sources
	if scope, ok := contractCtx.Value(conversationPublishedSourceKey{}).(conversationPublishedSource); ok {
		contractRoots = scope.roots
	}
	for _, ref := range mergeConversationSources(contractRoots) {
		if _, err := contractAudit.run(contractCtx, ref); err != nil {
			return agentsdk.ConversationDelegationDetail{}, err
		}
	}
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
	if d.OwnerUserID != "" && d.OwnerUserID != a.UserID {
		out.Participants = nil
		for _, p := range d.Participants {
			if p.UserID == a.UserID {
				out.Participants = append(out.Participants, p)
			}
		}
	}
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
		if _, err := audit.run(contractCtx, *d.InputSource); err != nil {
			return agentsdk.ConversationDelegationDetail{}, err
		}
	}
	if d.BriefSource != nil {
		if _, err := audit.run(contractCtx, *d.BriefSource); err != nil {
			return agentsdk.ConversationDelegationDetail{}, err
		}
	}
	for _, edge := range d.Dependencies {
		if _, err := audit.dependency(ctx, edge); err != nil {
			return agentsdk.ConversationDelegationDetail{}, err
		}
	}
	var dependencyErr error
	out.DependencyStates, dependencyErr = s.dependencyStates(ctx, d, a)
	if dependencyErr != nil {
		return out, dependencyErr
	}
	if d.OwnerUserID == "" || d.OwnerUserID == a.UserID {
		if _, err := s.repo.Get(ctx, d.SourceConversationID, a); err != nil {
			return out, err
		}
	}
	if d.ExecutionSubject == nil || delegationExecutor(d, a) {
		if _, err := s.repo.Get(ctx, d.ConversationID, a); err != nil {
			return out, err
		}
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
		task, err := s.delegationTaskRecord(ctx, d, a)
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
		if e := s.checkSharedDocuments(ctx, m.Documents, a); e != nil {
			m.Content = "共享资料当前不可访问或版本已变化，请重新核对资料引用。"
			m.Documents, m.DocumentsOmitted = nil, true
		}
		if m.Source != nil {
			messageCtx, e := s.messageSourceContext(ctx, *m, a)
			if e != nil || s.checkRunSources(messageCtx, *m.Source, a) != nil {
				m.Content = "消息来源当前无法验证，内容暂不可查看。"
				m.DocumentsOmitted = m.DocumentsOmitted || len(m.Documents) > 0
				m.Documents = nil
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
			if d.OwnerUserID != "" && d.OwnerUserID != a.UserID && collaborationDenied(err) {
				continue
			}
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
	if in.Participants != nil && in.Action != "set_participants" {
		return out, conversationFailure("bad_request", "delegation_participants_invalid")
	}
	if in.Publication != nil && in.Action != "republish_delivery" {
		return out, conversationFailure("bad_request", "delivery_publication_invalid")
	}
	if in.ContractPublication != nil && in.Action != "republish_contract" {
		return out, conversationFailure("bad_request", "contract_publication_invalid")
	}
	if !conversationKey(in.ClientID) || in.ExpectedRevision < 1 || !conversationText(in.Reason, 4096, true) || in.Brief != nil && !validConversationBrief(*in.Brief) {
		return out, conversationFailure("bad_request", "delegation_update_invalid")
	}
	if err := validateConversationStructuredInput(in.StructuredInput); err != nil {
		return out, err
	}
	if in.Action == "republish_contract" {
		return s.republishConversationContract(ctx, id, in, a)
	}
	if in.Action == "republish_delivery" {
		return s.republishConversationDelivery(ctx, id, in, a)
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
	if in.Action == "set_participants" {
		return s.updateDelegationParticipants(ctx, d, in, a)
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
		task, err := s.delegationTaskRecord(ctx, d, a)
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
	participantManager := d.OwnerUserID != "" && d.OwnerUserID != a.UserID && slices.Contains(participantOperations(d, a.UserID), "manage")
	if err = s.validateDisagreementUpdate(ctx, d, in, a, peer.ConversationID); err != nil {
		return out, err
	}

	if in.Review != nil {
		if in.Action != "accept_delivery" && in.Action != "review_delivery" {
			return out, conversationFailure("bad_request", "completion_assessment_invalid")
		}
		reviewCtx, sourceErr := s.delegationDeliverySourceContext(ctx, d, a)
		if sourceErr != nil {
			return out, sourceErr
		}
		if err = s.checkCompletionAssessments(reviewCtx, d.Brief, in.Review.Conditions, a, peer.ConversationID); err != nil {
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
		} else if !participantManager && peer.ConversationID != d.SourceConversationID {
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
		submissionCtx, sourceErr := s.proposedDeliverySourceContext(ctx, d, in.Delivery, a)
		if sourceErr != nil {
			return out, sourceErr
		}
		if err = s.validateConversationDelivery(submissionCtx, d, in.Delivery, a); err != nil {
			return out, err
		}
	}
	if in.Action == "accept_delivery" || in.Action == "review_delivery" {
		releaseCtx, err := s.delegationDeliverySourceContext(ctx, d, a)
		if err != nil {
			return out, err
		}
		if err = s.validateConversationDelivery(releaseCtx, d, d.Delivery, a); err != nil {
			return out, err
		}
		if in.Review == nil {
			return out, conversationFailure("bad_request", "completion_review_required")
		}
	}
	if in.Action == "accept_delivery" {
		task, err := s.delegationTaskRecord(ctx, d, a)
		if err != nil {
			return out, err
		}
		if task.Status != "completed" {
			return out, conversationFailure("conflict", "delegation_still_running")
		}
	}
	if in.Action == "resume" {
		if err = s.authorizeDelegationResume(ctx, d, a); err != nil {
			return out, err
		}
	}

	d, err = repo.UpdateConversationDelegation(ctx, id, in, a)
	if err != nil {
		return out, err
	}

	s.signalConversationTasks()
	if participantManager {
		return delegationManagementReceipt(d), nil
	}
	return s.projectConversationDelegation(ctx, d, a)
}

func delegationManagementReceipt(d agentsdk.ConversationDelegation) agentsdk.ConversationDelegationDetail {
	return agentsdk.ConversationDelegationDetail{ManagementOnly: true, Messages: []agentsdk.ConversationAgentMessage{}, ConversationDelegation: agentsdk.ConversationDelegation{
		ID: d.ID, OwnerUserID: d.OwnerUserID, Status: d.Status, Revision: d.Revision, Decision: d.Decision, UpdatedAt: d.UpdatedAt,
	}}
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
		refCtx, err := s.publishedReferenceContext(ctx, ref, a)
		if err != nil {
			return err
		}
		if _, err := s.repo.Run(refCtx, ref.ConversationID, ref.RunID, releasedEvidenceAuthority(refCtx, ref, a)); err != nil {
			return err
		}
		if err := s.checkRunSources(refCtx, ref, a); err != nil {
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
	if len(in.Documents) > 0 {
		if err = s.authorizeCollaboration(ctx, "share", &d, a); err != nil {
			return out, err
		}
		if err = s.checkSharedDocuments(ctx, in.Documents, a); err != nil {
			return out, err
		}
	}
	if d.OwnerUserID != "" && d.OwnerUserID != a.UserID && !delegationExecutor(d, a) {
		if err = s.validateAgentSharingSubjects(ctx, a, []string{d.OwnerUserID}); err != nil {
			return out, err
		}
		// The repository resolves only the already accepted recipient and its
		// frozen configuration. The sending participant never becomes executor.
		in.ExecutionAgent = nil
		out, err = repo.SendConversationAgentMessage(ctx, id, in, peer.ConversationID, a)
		if err == nil {
			out.ConversationID = ""
			s.signalConversationTasks()
			s.signal()
		}
		return out, err
	}
	in.ExecutionAgent = nil
	recipient := a
	if executionRepo, ok := s.repo.(persistence.ConversationDelegationExecutionRepository); ok {
		subjects, err := executionRepo.ConversationDelegationAuthorities(ctx, d.ID, a)
		if err != nil {
			return out, err
		}
		if in.ToAgentID == d.ToAgentID {
			recipient = subjects.Executor
		} else if in.ToAgentID == d.FromAgentID {
			recipient = subjects.Issuer
		}
	}
	if in.ToAgentID == d.ToAgentID {
		task, err := s.delegationTaskRecord(ctx, d, a)
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
	if _, err = s.selectConversationAgent(delegationExecutorContext(ctx), in.ExecutionAgent, recipient); err != nil {
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
