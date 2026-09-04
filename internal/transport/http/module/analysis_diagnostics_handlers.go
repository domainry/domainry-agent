package module

import (
	"net/http"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
)

type analysisQueryRequest struct {
	Intent     string         `json:"intent"`
	SQL        string         `json:"sql,omitempty"`
	MetricSpec map[string]any `json:"metric_spec,omitempty"`
	DryRun     bool           `json:"dry_run,omitempty"`
	MaxRows    int            `json:"max_rows,omitempty"`
}

func (s *adapter) queryAnalysis(w http.ResponseWriter, r *http.Request) {
	var payload analysisQueryRequest
	if !decode(w, r, &payload) {
		return
	}
	principal, ok := proposalPrincipal(r)
	if !ok {
		writeCode(w, http.StatusForbidden, "backend.workspace_scope_required")
		return
	}
	result, err := s.analysis.Query(r.Context(), agentapplication.AnalysisQuery{
		Intent: payload.Intent, SQL: payload.SQL, MetricSpec: cloneMap(payload.MetricSpec), DryRun: payload.DryRun, MaxRows: payload.MaxRows, Principal: principal,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *adapter) inspectDiagnostics(w http.ResponseWriter, r *http.Request) {
	principal, ok := authorizedActionPrincipal(r, agentsdk.ActionAgentDiagnosticsRead)
	if !ok {
		writeCode(w, http.StatusForbidden, "agent.authorization.action_denied")
		return
	}
	values := r.URL.Query()
	result, err := s.diagnostics.Inspect(r.Context(), agentapplication.DiagnosticsRequest{
		Principal: principal, Workspace: values.Get("workspace"), ModuleKey: values.Get("module_key"), ViewKey: values.Get("view_key"),
		ObjectKey: strings.TrimSpace(values.Get("object_key")), RecordID: values.Get("record_id"), DataScopeNote: values.Get("data_scope_note"),
		AgentMode: values.Get("agent_mode"), RunMode: values.Get("run_mode"), RiskLevel: values.Get("risk_level"),
		InteractiveConfigured: s.execution != nil, ExecutionState: s.tasks != nil && s.interactive != nil,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
