package module

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type adapter struct {
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
	openAPI     map[string]map[string]any
}

func (*adapter) ContractVersion() string { return modulehttp.ContractVersion }
func (*adapter) Owner() string           { return agentsdk.AgentHTTPAdapterOwner }
func (*adapter) Name() string            { return agentsdk.AgentHTTPAdapterName }
func (s *adapter) Handler() http.Handler { return s.mux }
func (s *adapter) Routes() []modulehttp.Route {
	return append([]modulehttp.Route(nil), s.routes...)
}

func NewAdapter(binding agentsdk.Binding, executions ...*agentapplication.InteractiveExecutionService) (modulehttp.Adapter, error) {
	var execution *agentapplication.InteractiveExecutionService
	if len(executions) > 0 {
		execution = executions[0]
	}
	return NewOwnedAdapter(binding, AdapterApplications{Interactive: execution})
}

func NewApplicationAdapter(binding agentsdk.Binding, execution *agentapplication.InteractiveExecutionService, proposals *agentapplication.ProposalService, taskOperations ...*agentapplication.TaskOperationsService) (modulehttp.Adapter, error) {
	applications := AdapterApplications{Interactive: execution, Proposals: proposals}
	if len(taskOperations) > 0 {
		applications.TaskOperations = taskOperations[0]
	}
	return NewOwnedAdapter(binding, applications)
}

type AdapterApplications struct {
	Interactive    *agentapplication.InteractiveExecutionService
	Proposals      *agentapplication.ProposalService
	TaskOperations *agentapplication.TaskOperationsService
	TaskTools      *agentapplication.TaskToolService
	Analysis       *agentapplication.AnalysisService
	Diagnostics    *agentapplication.DiagnosticsService
}

func NewOwnedAdapter(binding agentsdk.Binding, applications AdapterApplications) (modulehttp.Adapter, error) {
	stateBinding, ok := binding.(agentsdk.AgentDialogStateBinding)
	if !ok || stateBinding.DialogState() == nil {
		return nil, errors.New("Agent dialog state binding is unavailable")
	}
	executionBinding, ok := binding.(agentpersistence.ExecutionStateBinding)
	if !ok || executionBinding.AgentTaskState() == nil || executionBinding.AgentInteractiveState() == nil {
		return nil, errors.New("Agent execution state binding is unavailable")
	}
	s := &adapter{
		state: stateBinding.DialogState(), tasks: executionBinding.AgentTaskState(), interactive: executionBinding.AgentInteractiveState(),
		execution: applications.Interactive, proposals: applications.Proposals, operations: applications.TaskOperations,
		taskTools: applications.TaskTools, analysis: applications.Analysis, diagnostics: applications.Diagnostics,
		mux: http.NewServeMux(), openAPI: map[string]map[string]any{},
	}
	handlers := map[string]http.HandlerFunc{
		agentsdk.ActionAgentSessionsList:    s.listSessions,
		agentsdk.ActionAgentSessionsUpsert:  s.upsertSession,
		agentsdk.ActionAgentSessionsArchive: s.archiveSession,
		agentsdk.ActionAgentSessionsRestore: s.restoreSession,
		agentsdk.ActionAgentProposalsList:   s.listProposals,
		agentsdk.ActionAgentProposalsGet:    s.getProposal,
		agentsdk.ActionAgentRunsGet:         s.getInteractiveRun,
		agentsdk.ActionAgentTaskRunsGet:     s.getPrincipalTaskRun,
	}
	if s.execution != nil {
		handlers[agentsdk.ActionAgentRunsExecute] = s.runInteractive
		handlers[agentsdk.ActionAgentRunsStream] = s.streamInteractive
	}
	if s.proposals != nil {
		handlers[agentsdk.ActionAgentProposalsCreate] = s.createProposal
		handlers[agentsdk.ActionAgentProposalsApprove] = s.approveProposal
		handlers[agentsdk.ActionAgentProposalsReject] = s.rejectProposal
	}
	if s.operations != nil {
		handlers[agentsdk.ActionAgentTasksList] = s.listTaskRuns
		handlers[agentsdk.ActionAgentTasksGet] = s.getTaskRun
		handlers[agentsdk.ActionAgentTasksRetry] = s.retryTask
		handlers[agentsdk.ActionAgentTasksCancel] = s.cancelTask
		handlers[agentsdk.ActionAgentTasksResolve] = s.resolveTask
		handlers[agentsdk.ActionAgentTasksReconcile] = s.reconcileTask
	}
	if s.taskTools != nil {
		handlers[agentsdk.ActionAgentTaskToolsInvoke] = s.invokeTaskTool
	}
	if s.analysis != nil {
		handlers[agentsdk.ActionAgentAnalysisQuery] = s.queryAnalysis
	}
	if s.diagnostics != nil {
		handlers[agentsdk.ActionAgentDiagnosticsRead] = s.inspectDiagnostics
	}
	contract, err := agentsdk.CompileAgentHTTPAdapterContract()
	if err != nil {
		return nil, err
	}
	resolvedOperations := agentsdk.HTTPAdapterResolvedOpenAPIOperations()
	for _, source := range contract.Routes {
		handler, found := handlers[source.Action.Key]
		if !found {
			continue
		}
		route, err := modulehttp.RouteFromAction(source.Action)
		if err != nil {
			return nil, fmt.Errorf("project Agent HTTP Action %q: %w", source.Action.Key, err)
		}
		operation := resolvedOperations[route.Pattern()]
		if len(operation) == 0 {
			return nil, fmt.Errorf("Agent HTTP Action %q has no OpenAPI operation", source.Action.Key)
		}
		s.routes = append(s.routes, route)
		s.openAPI[route.Pattern()] = operation
		s.mux.HandleFunc(route.Pattern(), handler)
		delete(handlers, source.Action.Key)
	}
	if len(handlers) != 0 {
		keys := make([]string, 0, len(handlers))
		for key := range handlers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("Agent handlers have no source Actions: %s", strings.Join(keys, ", "))
	}
	return s, nil
}

func requestAuthority(r *http.Request) (agentsdk.AgentAuthority, error) {
	identity, ok := identitysdk.RequestIdentityFromContext(r.Context())
	if !ok {
		return agentsdk.AgentAuthority{}, &agentsdk.Error{Class: "forbidden", Code: "backend.workspace_scope_required"}
	}
	return agentsdk.AgentAuthority{WorkspaceID: strings.TrimSpace(identity.Principal.WorkspaceID), UserID: strings.TrimSpace(identity.Principal.UserID), RoleKey: strings.TrimSpace(identity.Principal.RoleKey)}, nil
}

func (s *adapter) listSessions(w http.ResponseWriter, r *http.Request) {
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

func (s *adapter) upsertSession(w http.ResponseWriter, r *http.Request) {
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

func (s *adapter) archiveSession(w http.ResponseWriter, r *http.Request) {
	s.setSessionArchived(w, r, true)
}

func (s *adapter) restoreSession(w http.ResponseWriter, r *http.Request) {
	s.setSessionArchived(w, r, false)
}

func (s *adapter) setSessionArchived(w http.ResponseWriter, r *http.Request, archived bool) {
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

func (s *adapter) listProposals(w http.ResponseWriter, r *http.Request) {
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

func (s *adapter) getProposal(w http.ResponseWriter, r *http.Request) {
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

func (s *adapter) getInteractiveRun(w http.ResponseWriter, r *http.Request) {
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

func (s *adapter) getPrincipalTaskRun(w http.ResponseWriter, r *http.Request) {
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

func (s *adapter) listTaskRuns(w http.ResponseWriter, r *http.Request) {
	principal, ok := authorizedActionPrincipal(r, agentsdk.ActionAgentTasksList)
	if !ok {
		writeCode(w, http.StatusForbidden, "agent.authorization.action_denied")
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
	runs, err := s.operations.List(r.Context(), principal.WorkspaceID, agentpersistence.AgentTaskRunFilter{Statuses: statuses, ProcessID: strings.TrimSpace(query.Get("process_id")), TaskKey: strings.TrimSpace(query.Get("task_key")), Limit: limit}, principal)
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

func (s *adapter) getTaskRun(w http.ResponseWriter, r *http.Request) {
	principal, ok := authorizedActionPrincipal(r, agentsdk.ActionAgentTasksGet)
	if !ok {
		writeCode(w, http.StatusForbidden, "agent.authorization.action_denied")
		return
	}
	run, found, err := s.operations.Get(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("taskRunID")), principal)
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

var _ modulehttp.Adapter = (*adapter)(nil)
var _ modulehttp.OpenAPIProvider = (*adapter)(nil)
