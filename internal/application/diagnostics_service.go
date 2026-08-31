package application

import (
	"context"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
)

type DiagnosticsRequest struct {
	Principal                             modulehost.Principal
	Workspace, ModuleKey, ViewKey         string
	ObjectKey, RecordID, DataScopeNote    string
	AgentMode, RunMode, RiskLevel         string
	InteractiveConfigured, ExecutionState bool
}

type DiagnosticsService struct {
	state agentsdk.AgentDialogStateService
	audit modulehost.AuditHost
}

func NewDiagnosticsService(state agentsdk.AgentDialogStateService, audit modulehost.AuditHost) *DiagnosticsService {
	return &DiagnosticsService{state: state, audit: audit}
}

func (s *DiagnosticsService) Inspect(ctx context.Context, request DiagnosticsRequest) (map[string]any, error) {
	if s == nil || s.state == nil || s.audit == nil {
		return nil, unavailable("agent.diagnostics.unavailable")
	}
	events, eventErr := s.audit.ListAgentAudit(ctx, request.Principal, 50)
	filtered := make([]modulehost.AuditEvent, 0, len(events))
	for _, event := range events {
		if !strings.HasPrefix(event.Event, "agent_dialog") && !strings.HasPrefix(event.Event, "agent_analysis") {
			continue
		}
		if request.ObjectKey != "" && event.ObjectKey != request.ObjectKey && stringFromMap(event.Metadata, "object_key") != request.ObjectKey && !strings.HasPrefix(event.Event, "agent_dialog_proposal_") {
			continue
		}
		filtered = append(filtered, event)
	}
	queue, _ := s.state.ListProposals(ctx, "", agentsdk.AgentAuthority{WorkspaceID: request.Principal.WorkspaceID, UserID: request.Principal.UserID, RoleKey: request.Principal.RoleKey})
	if request.ObjectKey != "" {
		visible := queue[:0]
		for _, proposal := range queue {
			if stringFromMap(proposal.Metadata, "object_key") == request.ObjectKey {
				visible = append(visible, proposal)
			}
		}
		queue = visible
	}
	runMode := valueOrDefault(request.RunMode, "suggested_write")
	return map[string]any{
		"runtime_context_preview": map[string]any{
			"workspace": request.Workspace, "module_key": request.ModuleKey, "view_key": request.ViewKey,
			"object_key": request.ObjectKey, "record_id": request.RecordID, "data_scope_note": request.DataScopeNote,
			"principal": request.Principal.Reference(),
		},
		"agent_runner": map[string]any{"configured": request.InteractiveConfigured, "context_resolver": true, "execution_state": request.ExecutionState, "transport": "domainry-agent-sdk"},
		"skill_bindings": []map[string]any{
			{"family": "domain-flow", "skill": "agents/skills/domain-flow", "default_run_mode": "suggested_write"},
			{"family": "data-analysis", "skill": "agents/skills/data-analysis", "default_run_mode": "read_only"},
			{"family": "system-ops", "skill": "agents/skills/system-ops", "default_run_mode": "read_only"},
		},
		"effective_tools": []map[string]any{
			diagnosticTool("generated_api_read", "generated_api", "read_only", "low", false, ""),
			diagnosticTool("analysis_query_gateway", "analysis", "read_only", "medium", false, ""),
			diagnosticTool("mysql_readonly_query", "database", "read_only", "medium", false, "fallback_diagnosis_only"),
			diagnosticPolicyTool("create_proposal", "medium", false, runMode, request.RiskLevel),
			diagnosticPolicyTool("approve_proposal", "high", true, runMode, request.RiskLevel),
			diagnosticPolicyTool("reject_proposal", "medium", true, runMode, request.RiskLevel),
		},
		"proposal_queue": queue, "execution_logs": filtered, "execution_log_error": errorText(eventErr),
		"guardrails": []string{"admin_only", "no_secret_material", "server_principal_scoped", "proposal_actions_require_suggested_write", "database_access_wrapper_only"},
	}, nil
}

func diagnosticTool(key, family, runMode, risk string, approval bool, denial string) map[string]any {
	return map[string]any{"key": key, "family": family, "run_mode": runMode, "risk_level": risk, "approval_required": approval, "allowed": denial == "", "denial_reason": denial, "policy_source": "agent/tool-risk-registry"}
}
func diagnosticPolicyTool(key, risk string, approval bool, runMode, requestedRisk string) map[string]any {
	denial := ""
	if runMode == "read_only" {
		denial = "agent_dialog.policy_read_only"
	} else if runMode != "suggested_write" {
		denial = "agent_dialog.policy_run_mode_denied"
	} else if key != "create_proposal" && valueOrDefault(requestedRisk, risk) == "critical" {
		denial = "agent_dialog.policy_risk_denied"
	}
	return diagnosticTool(key, "proposal", "suggested_write", risk, approval, denial)
}
func stringFromMap(values map[string]any, key string) string {
	text, _ := values[key].(string)
	return strings.TrimSpace(text)
}
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
