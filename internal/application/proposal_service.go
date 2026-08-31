package application

import (
	"context"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
)

// ProposalService owns proposal normalization, decision CAS, approval
// execution and the link back to Agent-owned task approval state. Runtime is
// used only for current principal resolution and business effects.
type ProposalService struct {
	state agentsdk.AgentDialogStateService
	host  modulehost.ProposalHost
	audit modulehost.AuditHost
	tasks *TaskExecutionService
}

// CreateHostDraft persists a proposal draft returned by a Runtime Host tool.
// The host describes the guarded business effect; Agent owns proposal identity,
// persistence and lifecycle.
func (s *ProposalService) CreateHostDraft(ctx context.Context, raw any, principal modulehost.Principal) (agentsdk.AgentProposal, error) {
	draft := proposalMap(raw)
	proposed := proposalMap(draft["proposed"])
	metadata := proposalMap(draft["metadata"])
	if len(proposed) == 0 {
		return agentsdk.AgentProposal{}, badRequest("agent.tool.proposal_invalid")
	}
	reference := firstProposalString(draft, "reference")
	idempotencyKey := firstProposalString(metadata, "idempotency_key")
	proposalID := firstProposalString(draft, "proposal_id")
	if proposalID == "" {
		hash := stableHash(map[string]any{"workspace_id": principal.WorkspaceID, "reference": reference, "idempotency_key": idempotencyKey, "proposed": proposed})
		proposalID = "agent-proposal-" + hash[:24]
	}
	return s.Create(ctx, CreateProposalRequest{
		ProposalID: proposalID, Title: firstProposalString(draft, "title"), Summary: firstProposalString(draft, "summary"),
		Source: firstProposalString(draft, "source"), Reference: reference,
		Proposed: proposed, Metadata: metadata, Principal: principal,
	})
}

func NewProposalService(state agentsdk.AgentDialogStateService, host modulehost.ProposalHost, audit modulehost.AuditHost, tasks *TaskExecutionService) *ProposalService {
	return &ProposalService{state: state, host: host, audit: audit, tasks: tasks}
}

type CreateProposalRequest struct {
	ProposalID, Title, Summary, Source, Reference string
	Proposed, Metadata                            map[string]any
	Principal                                     modulehost.Principal
}

func (s *ProposalService) Create(ctx context.Context, request CreateProposalRequest) (agentsdk.AgentProposal, error) {
	if s == nil || s.state == nil {
		return agentsdk.AgentProposal{}, unavailable("agent.dialog_state.unavailable")
	}
	proposed := s.NormalizeGuardedWrite(ctx, cloneTaskMap(request.Proposed), request.Principal)
	metadata := cloneTaskMap(request.Metadata)
	metadata["proposal_id"], metadata["title"] = strings.TrimSpace(request.ProposalID), strings.TrimSpace(request.Title)
	metadata["source"], metadata["reference"] = strings.TrimSpace(request.Source), strings.TrimSpace(request.Reference)
	metadata["requesting_user"], metadata["service_role"], metadata["automation_user"] = request.Principal.UserID, "agent_service_user", "agent_automation"
	proposal, err := s.state.StoreProposal(ctx, agentsdk.AgentProposal{
		ProposalID: strings.TrimSpace(request.ProposalID), Status: "draft", Title: strings.TrimSpace(request.Title), Summary: strings.TrimSpace(request.Summary),
		Source: strings.TrimSpace(request.Source), Reference: strings.TrimSpace(request.Reference), Actor: request.Principal.UserID,
		WorkspaceID: request.Principal.WorkspaceID, UserID: request.Principal.UserID, Role: request.Principal.RoleKey,
		Proposed: proposed, Metadata: metadata,
	})
	if err != nil {
		return agentsdk.AgentProposal{}, err
	}
	s.appendAudit(ctx, "agent_dialog_proposal_created", proposal, request.Principal, metadata)
	return proposal, nil
}

func (s *ProposalService) NormalizeGuardedWrite(ctx context.Context, proposed map[string]any, principal modulehost.Principal) map[string]any {
	if proposed == nil {
		return map[string]any{}
	}
	if binding := proposalMap(proposed["action_binding"]); len(binding) > 0 {
		return proposed
	}
	var tool map[string]any
	for _, key := range []string{"tool_binding", "tool_call", "tool", "crud_binding"} {
		if candidate := proposalMap(proposed[key]); len(candidate) > 0 {
			tool = candidate
			break
		}
	}
	if len(tool) == 0 {
		return proposed
	}
	toolName := firstProposalString(tool, "tool_name", "tool", "name")
	operation := map[string]string{"createRecord": "create", "updateRecord": "update", "deleteRecord": "delete"}[toolName]
	objectKey := firstProposalString(tool, "object_key", "objectKey", "object")
	if operation == "" || objectKey == "" || s == nil || s.host == nil {
		return proposed
	}
	var guarded *modulehost.GuardedWriteContract
	for _, contract := range s.host.GuardedWrites(ctx, principal) {
		if contract.ObjectKey == objectKey && contract.Operation == operation && contract.ActionKey != "" {
			value := contract
			guarded = &value
			break
		}
	}
	if guarded == nil {
		return proposed
	}
	recordID := firstProposalString(tool, "record_id", "recordId", "id")
	if guarded.RequiresRecord && recordID == "" {
		return proposed
	}
	data := proposalMap(tool["data"])
	if operation == "update" && len(data) == 0 {
		data = proposalMap(tool["patch"])
	}
	normalized := cloneTaskMap(proposed)
	delete(normalized, "tool_binding")
	normalized["action_binding"] = map[string]any{
		"object_key": guarded.ObjectKey, "record_id": recordID, "action_key": guarded.ActionKey,
		"data": data, "guarded_write": true, "operation": guarded.Operation, "endpoint": guarded.Endpoint, "source_tool": toolName,
	}
	return normalized
}

func (s *ProposalService) Decide(ctx context.Context, proposalID, decision, reason string, metadata map[string]any, principal modulehost.Principal) (agentsdk.AgentProposal, error) {
	decision = strings.TrimSpace(decision)
	switch decision {
	case "approved", "rejected", "returned", "timed_out", "cancelled":
	default:
		return agentsdk.AgentProposal{}, badRequest("agent_dialog.proposal_decision_invalid")
	}
	if s == nil || s.state == nil || s.host == nil {
		return agentsdk.AgentProposal{}, unavailable("agent.dialog_state.unavailable")
	}
	proposal, err := s.state.GetProposal(ctx, proposalID, proposalAuthority(principal))
	if err != nil {
		return proposal, err
	}
	if proposal.Status != "draft" {
		if proposal.Status != decision {
			return proposal, conflict("agent_dialog.proposal_already_decided")
		}
		return proposal, s.resolveTask(ctx, proposal, principal)
	}
	executionPrincipal := principal
	if decision == "approved" {
		executionPrincipal, err = s.resolveApprovalPrincipal(ctx, proposal, principal)
		if err != nil {
			return proposal, err
		}
	}
	proposal, err = s.decideCAS(ctx, proposal, decision, reason, metadata, nil, principal)
	if err != nil {
		return proposal, err
	}
	if decision != "approved" {
		s.appendAudit(ctx, "agent_dialog_proposal_"+decision, proposal, principal, metadata)
		return proposal, s.resolveTask(ctx, proposal, principal)
	}
	execution := s.execute(ctx, proposal, executionPrincipal)
	proposal, err = s.decideCAS(ctx, proposal, decision, reason, metadata, execution, principal)
	if err != nil {
		return proposal, err
	}
	s.appendAudit(ctx, "agent_dialog_proposal_"+decision, proposal, principal, metadata)
	return proposal, s.resolveTask(ctx, proposal, principal)
}

func (s *ProposalService) appendAudit(ctx context.Context, event string, proposal agentsdk.AgentProposal, principal modulehost.Principal, metadata map[string]any) {
	if s == nil || s.audit == nil {
		return
	}
	_ = s.audit.AppendAgentAudit(ctx, modulehost.AuditRequest{
		Event: event, ObjectKey: "agent_proposal", RecordID: proposal.ProposalID, Summary: strings.ReplaceAll(event, "_", " "),
		Principal: principal, Metadata: cloneTaskMap(metadata),
	})
}

func (s *ProposalService) decideCAS(ctx context.Context, proposal agentsdk.AgentProposal, decision, reason string, metadata, execution map[string]any, principal modulehost.Principal) (agentsdk.AgentProposal, error) {
	return s.state.DecideProposal(ctx, agentsdk.AgentProposalDecision{
		ProposalID: proposal.ProposalID, Decision: decision, Reason: reason, Metadata: cloneTaskMap(metadata),
		Execution: cloneTaskMap(execution), ExpectedUpdatedAt: proposal.UpdatedAt,
	}, proposalAuthority(principal))
}

func (s *ProposalService) resolveApprovalPrincipal(ctx context.Context, proposal agentsdk.AgentProposal, requestPrincipal modulehost.Principal) (modulehost.Principal, error) {
	principal, err := s.host.ResolveProposalPrincipal(ctx, proposal.UserID, proposal.Role)
	if err != nil {
		return modulehost.Principal{}, err
	}
	if !principal.Known || strings.TrimSpace(principal.WorkspaceID) != strings.TrimSpace(proposal.WorkspaceID) || strings.TrimSpace(principal.UserID) != strings.TrimSpace(proposal.UserID) || strings.TrimSpace(principal.RoleKey) != strings.TrimSpace(proposal.Role) {
		return modulehost.Principal{}, forbidden("agent.authorization.approval_principal_revoked")
	}
	principal.RequestID, principal.CorrelationID, principal.CausationID = requestPrincipal.RequestID, requestPrincipal.CorrelationID, requestPrincipal.CausationID
	return principal, nil
}

func (s *ProposalService) resolveTask(ctx context.Context, proposal agentsdk.AgentProposal, principal modulehost.Principal) error {
	if s == nil || s.tasks == nil {
		return nil
	}
	return s.tasks.ResolveProposal(ctx, proposal, principal)
}

func (s *ProposalService) execute(ctx context.Context, proposal agentsdk.AgentProposal, principal modulehost.Principal) map[string]any {
	if proposal.Status != "approved" {
		return nil
	}
	if binding := proposalMap(proposal.Proposed["action_binding"]); len(binding) > 0 {
		objectKey, recordID, actionKey := proposalString(binding["object_key"]), proposalString(binding["record_id"]), proposalString(binding["action_key"])
		if objectKey == "" || actionKey == "" {
			return map[string]any{"status": "skipped", "kind": "action", "reason": "binding_incomplete"}
		}
		proposalID := strings.TrimSpace(proposal.ProposalID)
		if proposalID == "" {
			return map[string]any{"status": "failed", "kind": "action", "reason": "proposal_id_required"}
		}
		invocation, err := s.host.InvokeProposalAction(ctx, modulehost.ProposalActionRequest{
			ActionKey: actionKey, ObjectKey: objectKey, RecordID: recordID, Input: proposalMap(binding["data"]),
			Principal: principal, IdempotencyKey: "agent-proposal:" + proposalID,
		})
		kind, result := "action", invocation.Record
		if recordID == "" {
			kind, result = "object_action", invocation.Object
		}
		if err != nil {
			return map[string]any{"status": "failed", "kind": kind, "object_key": objectKey, "record_id": recordID, "action_key": actionKey, "error_code": proposalErrorCode(err)}
		}
		return map[string]any{"status": "applied", "kind": kind, "object_key": objectKey, "record_id": recordID, "action_key": actionKey, "result": result}
	}
	if binding := proposalMap(proposal.Proposed["workflow_binding"]); len(binding) > 0 {
		key := proposalString(binding["workflow_key"])
		if key == "" {
			return map[string]any{"status": "skipped", "kind": "workflow", "reason": "binding_incomplete"}
		}
		result, err := s.host.RunProposalWorkflow(ctx, modulehost.ProposalWorkflowRequest{WorkflowKey: key, Payload: proposalMap(binding["payload"]), Principal: principal})
		if err != nil {
			return map[string]any{"status": "failed", "kind": "workflow", "workflow_key": key, "error_code": proposalErrorCode(err)}
		}
		return map[string]any{"status": "applied", "kind": "workflow", "workflow_key": key, "result": result}
	}
	return map[string]any{"status": "skipped", "kind": "none", "reason": "no_binding"}
}

func proposalAuthority(principal modulehost.Principal) agentsdk.AgentAuthority {
	return agentsdk.AgentAuthority{WorkspaceID: strings.TrimSpace(principal.WorkspaceID), UserID: strings.TrimSpace(principal.UserID), RoleKey: strings.TrimSpace(principal.RoleKey)}
}

func proposalMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return cloneTaskMap(typed)
	}
	return map[string]any{}
}

func proposalString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstProposalString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := proposalString(values[key]); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func proposalErrorCode(err error) string {
	if err == nil {
		return ""
	}
	type coded interface{ ErrorCode() string }
	if value, ok := err.(coded); ok && strings.TrimSpace(value.ErrorCode()) != "" {
		return strings.TrimSpace(value.ErrorCode())
	}
	return "backend.internal"
}
