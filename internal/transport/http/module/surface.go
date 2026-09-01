package module

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type surface struct {
	state       agentsdk.AgentDialogStateService
	tasks       agentpersistence.AgentTaskStateService
	interactive agentpersistence.AgentInteractiveStateService
	execution   *agentapplication.InteractiveExecutionService
	proposals   *agentapplication.ProposalService
	operations  *agentapplication.TaskOperationsService
	taskTools   *agentapplication.TaskToolService
	analysis    *agentapplication.AnalysisService
	diagnostics *agentapplication.DiagnosticsService
	mux         *http.ServeMux
	routes      []modulehttp.Route
}

func (*surface) ContractVersion() string { return modulehttp.ContractVersion }
func (*surface) Owner() string           { return agentsdk.AgentHTTPSurfaceContract().Owner }
func (*surface) Name() string            { return agentsdk.AgentHTTPSurfaceContract().Name }
func (s *surface) Handler() http.Handler { return s.mux }
func (s *surface) Routes() []modulehttp.Route {
	return append([]modulehttp.Route(nil), s.routes...)
}

func NewSurface(binding agentsdk.Binding, executions ...*agentapplication.InteractiveExecutionService) (modulehttp.Surface, error) {
	var execution *agentapplication.InteractiveExecutionService
	if len(executions) > 0 {
		execution = executions[0]
	}
	return NewOwnedSurface(binding, SurfaceApplications{Interactive: execution})
}

func NewApplicationSurface(binding agentsdk.Binding, execution *agentapplication.InteractiveExecutionService, proposals *agentapplication.ProposalService, taskOperations ...*agentapplication.TaskOperationsService) (modulehttp.Surface, error) {
	applications := SurfaceApplications{Interactive: execution, Proposals: proposals}
	if len(taskOperations) > 0 {
		applications.TaskOperations = taskOperations[0]
	}
	return NewOwnedSurface(binding, applications)
}

type SurfaceApplications struct {
	Interactive    *agentapplication.InteractiveExecutionService
	Proposals      *agentapplication.ProposalService
	TaskOperations *agentapplication.TaskOperationsService
	TaskTools      *agentapplication.TaskToolService
	Analysis       *agentapplication.AnalysisService
	Diagnostics    *agentapplication.DiagnosticsService
}

func NewOwnedSurface(binding agentsdk.Binding, applications SurfaceApplications) (modulehttp.Surface, error) {
	stateBinding, ok := binding.(agentsdk.AgentDialogStateBinding)
	if !ok || stateBinding.DialogState() == nil {
		return nil, errors.New("Agent dialog state binding is unavailable")
	}
	executionBinding, ok := binding.(agentpersistence.ExecutionStateBinding)
	if !ok || executionBinding.AgentTaskState() == nil || executionBinding.AgentInteractiveState() == nil {
		return nil, errors.New("Agent execution state binding is unavailable")
	}
	s := &surface{state: stateBinding.DialogState(), tasks: executionBinding.AgentTaskState(), interactive: executionBinding.AgentInteractiveState(), execution: applications.Interactive, proposals: applications.Proposals, operations: applications.TaskOperations, taskTools: applications.TaskTools, analysis: applications.Analysis, diagnostics: applications.Diagnostics, mux: http.NewServeMux()}
	s.routes = []modulehttp.Route{
		dialogReadRoute("GET /agent-dialog/sessions"),
		dialogWriteRoute("POST /agent-dialog/sessions"),
		dialogWriteRoute("POST /agent-dialog/sessions/{externalSessionID}/archive"),
		dialogWriteRoute("POST /agent-dialog/sessions/{externalSessionID}/restore"),
		dialogReadRoute("GET /agent-dialog/proposals"),
		dialogReadRoute("GET /agent-dialog/proposals/{proposalID}"),
		dialogReadRoute("GET /agent-dialog/runs/{runID}"),
		dialogReadRoute("GET /agent-dialog/task-runs/{taskRunID}"),
		agentTaskOperationsReadRoute("GET /operations/agent/tasks"),
		agentTaskOperationsReadRoute("GET /operations/agent/tasks/{taskRunID}"),
	}
	if s.execution != nil {
		s.routes = append([]modulehttp.Route{dialogExecutionRoute("POST /agent-dialog/runs"), dialogExecutionRoute("POST /agent-dialog/runs/stream")}, s.routes...)
		s.mux.HandleFunc("POST /agent-dialog/runs", s.runInteractive)
		s.mux.HandleFunc("POST /agent-dialog/runs/stream", s.streamInteractive)
	}
	if s.proposals != nil {
		s.routes = append(s.routes,
			dialogWriteRoute("POST /agent-dialog/proposals"),
			dialogWriteRoute("POST /agent-dialog/proposals/{proposalID}/approve"),
			dialogWriteRoute("POST /agent-dialog/proposals/{proposalID}/reject"),
		)
		s.mux.HandleFunc("POST /agent-dialog/proposals", s.createProposal)
		s.mux.HandleFunc("POST /agent-dialog/proposals/{proposalID}/approve", s.approveProposal)
		s.mux.HandleFunc("POST /agent-dialog/proposals/{proposalID}/reject", s.rejectProposal)
	}
	if s.operations != nil {
		for _, operation := range []string{"retry", "cancel", "resolve", "reconcile"} {
			pattern := "POST /operations/agent/tasks/{taskRunID}/" + operation
			s.routes = append(s.routes, agentTaskOperationsWriteRoute(pattern))
		}
		s.mux.HandleFunc("POST /operations/agent/tasks/{taskRunID}/retry", s.retryTask)
		s.mux.HandleFunc("POST /operations/agent/tasks/{taskRunID}/cancel", s.cancelTask)
		s.mux.HandleFunc("POST /operations/agent/tasks/{taskRunID}/resolve", s.resolveTask)
		s.mux.HandleFunc("POST /operations/agent/tasks/{taskRunID}/reconcile", s.reconcileTask)
	}
	if s.taskTools != nil {
		s.routes = append(s.routes, taskToolCallbackRoute("POST /agent-dialog/task-tools/invoke"))
		s.mux.HandleFunc("POST /agent-dialog/task-tools/invoke", s.invokeTaskTool)
	}
	if s.analysis != nil {
		s.routes = append(s.routes, dialogAnalysisRoute("POST /agent-dialog/analysis/query"))
		s.mux.HandleFunc("POST /agent-dialog/analysis/query", s.queryAnalysis)
	}
	if s.diagnostics != nil {
		s.routes = append(s.routes, agentDiagnosticsRoute("GET /agent-dialog/diagnostics"))
		s.mux.HandleFunc("GET /agent-dialog/diagnostics", s.inspectDiagnostics)
	}
	s.mux.HandleFunc("GET /agent-dialog/sessions", s.listSessions)
	s.mux.HandleFunc("POST /agent-dialog/sessions", s.upsertSession)
	s.mux.HandleFunc("POST /agent-dialog/sessions/{externalSessionID}/archive", s.archiveSession)
	s.mux.HandleFunc("POST /agent-dialog/sessions/{externalSessionID}/restore", s.restoreSession)
	s.mux.HandleFunc("GET /agent-dialog/proposals", s.listProposals)
	s.mux.HandleFunc("GET /agent-dialog/proposals/{proposalID}", s.getProposal)
	s.mux.HandleFunc("GET /agent-dialog/runs/{runID}", s.getInteractiveRun)
	s.mux.HandleFunc("GET /agent-dialog/task-runs/{taskRunID}", s.getPrincipalTaskRun)
	s.mux.HandleFunc("GET /operations/agent/tasks", s.listTaskRuns)
	s.mux.HandleFunc("GET /operations/agent/tasks/{taskRunID}", s.getTaskRun)
	return s, nil
}

func dialogExecutionRoute(pattern string) modulehttp.Route {
	return agentHTTPRoute(pattern)
}

func taskToolCallbackRoute(pattern string) modulehttp.Route {
	return agentHTTPRoute(pattern)
}

func dialogAnalysisRoute(pattern string) modulehttp.Route {
	return agentHTTPRoute(pattern)
}

func agentDiagnosticsRoute(pattern string) modulehttp.Route {
	return agentHTTPRoute(pattern)
}

func agentTaskOperationsReadRoute(pattern string) modulehttp.Route {
	return agentHTTPRoute(pattern)
}

func agentTaskOperationsWriteRoute(pattern string) modulehttp.Route {
	return agentHTTPRoute(pattern)
}

func dialogReadRoute(pattern string) modulehttp.Route {
	return agentHTTPRoute(pattern)
}

func dialogWriteRoute(pattern string) modulehttp.Route {
	return agentHTTPRoute(pattern)
}

func agentHTTPRoute(pattern string) modulehttp.Route {
	for _, route := range agentsdk.AgentHTTPSurfaceContract().Routes {
		if route.Pattern != pattern {
			continue
		}
		exposures := make([]modulehttp.Exposure, len(route.Exposures))
		for index, exposure := range route.Exposures {
			exposures[index] = modulehttp.Exposure(exposure)
		}
		return modulehttp.Route{
			Pattern: route.Pattern, Exposures: exposures, Authentication: modulehttp.Authentication(route.Authentication), Permission: route.Permission,
			AnyPermissions: append([]string(nil), route.AnyPermissions...), PrincipalOnly: route.PrincipalOnly,
			Governance: &modulehttp.Governance{EffectClass: modulehttp.EffectClass(route.EffectClass), HighRiskPolicy: modulehttp.HighRiskPolicy(route.HighRiskPolicy), IdempotencyDecision: route.IdempotencyDecision, AuditClass: route.AuditClass},
		}
	}
	panic("Agent SDK HTTP route is unavailable: " + pattern)
}

func requestAuthority(r *http.Request) (agentsdk.AgentAuthority, error) {
	identity, ok := identitysdk.RequestIdentityFromContext(r.Context())
	if !ok {
		return agentsdk.AgentAuthority{}, &agentsdk.Error{Class: "forbidden", Code: "backend.workspace_scope_required"}
	}
	return agentsdk.AgentAuthority{WorkspaceID: strings.TrimSpace(identity.Principal.WorkspaceID), UserID: strings.TrimSpace(identity.Principal.UserID), RoleKey: strings.TrimSpace(identity.Principal.RoleKey)}, nil
}

func (s *surface) listSessions(w http.ResponseWriter, r *http.Request) {
	authority, err := requestAuthority(r)
	if err != nil {
		writeError(w, err)
		return
	}
	query := r.URL.Query()
	value, err := s.state.ListSessions(r.Context(), agentsdk.AgentSessionQuery{Search: query.Get("q"), ObjectKey: strings.TrimSpace(query.Get("object_key")), RecordID: strings.TrimSpace(query.Get("record_id")), IncludeArchived: query.Get("archived") == "true", Limit: sessionLimit(query.Get("limit"))}, authority)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": value})
}

func (s *surface) upsertSession(w http.ResponseWriter, r *http.Request) {
	authority, err := requestAuthority(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var input agentsdk.AgentSessionUpsertRequest
	if !decode(w, r, &input) {
		return
	}
	value, err := s.state.UpsertSession(r.Context(), input, authority)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *surface) archiveSession(w http.ResponseWriter, r *http.Request) {
	s.setSessionArchived(w, r, true)
}

func (s *surface) restoreSession(w http.ResponseWriter, r *http.Request) {
	s.setSessionArchived(w, r, false)
}

func (s *surface) setSessionArchived(w http.ResponseWriter, r *http.Request, archived bool) {
	authority, err := requestAuthority(r)
	if err != nil {
		writeError(w, err)
		return
	}
	value, err := s.state.SetSessionArchived(r.Context(), r.PathValue("externalSessionID"), archived, authority)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *surface) listProposals(w http.ResponseWriter, r *http.Request) {
	authority, err := requestAuthority(r)
	if err != nil {
		writeError(w, err)
		return
	}
	value, err := s.state.ListProposals(r.Context(), r.URL.Query().Get("status"), authority)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": value})
}

func (s *surface) getProposal(w http.ResponseWriter, r *http.Request) {
	authority, err := requestAuthority(r)
	if err != nil {
		writeError(w, err)
		return
	}
	value, err := s.state.GetProposal(r.Context(), r.PathValue("proposalID"), authority)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *surface) getInteractiveRun(w http.ResponseWriter, r *http.Request) {
	authority, err := requestInteractiveAuthority(r)
	if err != nil {
		writeError(w, err)
		return
	}
	run, found, err := s.interactive.Get(r.Context(), strings.TrimSpace(r.PathValue("runID")), authority)
	if err != nil {
		writeError(w, err)
		return
	}
	if !found {
		writeCode(w, http.StatusNotFound, "agent.interactive.not_found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *surface) getPrincipalTaskRun(w http.ResponseWriter, r *http.Request) {
	identity, ok := identitysdk.RequestIdentityFromContext(r.Context())
	if !ok {
		writeCode(w, http.StatusForbidden, "backend.workspace_scope_required")
		return
	}
	principal := identity.Principal
	run, found, err := s.tasks.Get(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("taskRunID")))
	if err != nil {
		writeError(w, err)
		return
	}
	if !found || run.WorkspaceID != principal.WorkspaceID || run.Identity.Initiator.UserID != principal.UserID || run.Identity.Initiator.RoleKey != principal.RoleKey || taskAuthorizationStale(run, principal.AuthorizationRevision) {
		writeCode(w, http.StatusNotFound, "agent.task.not_found")
		return
	}
	writeJSON(w, http.StatusOK, agentpersistence.ProjectAgentTaskRun(run))
}

func (s *surface) listTaskRuns(w http.ResponseWriter, r *http.Request) {
	authority, err := requestAuthority(r)
	if err != nil {
		writeError(w, err)
		return
	}
	query := r.URL.Query()
	statuses := make([]agentmodel.AgentTaskRunStatus, 0, len(query["status"]))
	for _, status := range query["status"] {
		if value := strings.TrimSpace(status); value != "" {
			statuses = append(statuses, agentmodel.AgentTaskRunStatus(value))
		}
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	runs, err := s.tasks.List(r.Context(), authority.WorkspaceID, agentpersistence.AgentTaskRunFilter{Statuses: statuses, ProcessID: strings.TrimSpace(query.Get("process_id")), TaskKey: strings.TrimSpace(query.Get("task_key")), Limit: limit})
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]agentpersistence.AgentTaskRunView, 0, len(runs))
	for _, run := range runs {
		items = append(items, agentpersistence.ProjectAgentTaskRun(run))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (s *surface) getTaskRun(w http.ResponseWriter, r *http.Request) {
	authority, err := requestAuthority(r)
	if err != nil {
		writeError(w, err)
		return
	}
	run, found, err := s.tasks.Get(r.Context(), authority.WorkspaceID, strings.TrimSpace(r.PathValue("taskRunID")))
	if err != nil {
		writeError(w, err)
		return
	}
	if !found {
		writeCode(w, http.StatusNotFound, "agent.task.not_found")
		return
	}
	writeJSON(w, http.StatusOK, agentpersistence.ProjectAgentTaskRun(run))
}

func requestInteractiveAuthority(r *http.Request) (agentpersistence.AgentInteractiveAuthority, error) {
	identity, ok := identitysdk.RequestIdentityFromContext(r.Context())
	if !ok {
		return agentpersistence.AgentInteractiveAuthority{}, &agentsdk.Error{Class: "forbidden", Code: "backend.workspace_scope_required"}
	}
	principal := identity.Principal
	return agentpersistence.AgentInteractiveAuthority{Known: principal.Known, WorkspaceID: strings.TrimSpace(principal.WorkspaceID), UserID: strings.TrimSpace(principal.UserID), RoleKey: strings.TrimSpace(principal.RoleKey), AuthorizationRevision: strings.TrimSpace(principal.AuthorizationRevision)}, nil
}

func taskAuthorizationStale(run agentmodel.AgentTaskRun, currentRevision string) bool {
	stored, current := strings.TrimSpace(run.Identity.Initiator.AuthorizationRevision), strings.TrimSpace(currentRevision)
	return stored != "" && current != "" && stored != current
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "backend.request.invalid_json"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "backend.internal"
	var sdkError *agentsdk.Error
	if errors.As(err, &sdkError) {
		code = sdkError.ErrorCode()
		switch sdkError.Class {
		case "bad_request":
			status = http.StatusBadRequest
		case "forbidden":
			status = http.StatusForbidden
		case "not_found":
			status = http.StatusNotFound
		case "conflict":
			status = http.StatusConflict
		case "unavailable":
			status = http.StatusServiceUnavailable
		}
	}
	writeJSON(w, status, map[string]string{"code": code})
}

func writeCode(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"code": code})
}

func sessionLimit(raw string) int {
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

var _ modulehttp.Surface = (*surface)(nil)
var _ modulehttp.OpenAPIProvider = (*surface)(nil)
