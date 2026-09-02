package module

import (
	"net/http"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *surface) retryTask(w http.ResponseWriter, r *http.Request) {
	s.operateTask(w, r, "retry", agentsdk.ActionAgentTasksRetry)
}
func (s *surface) resolveTask(w http.ResponseWriter, r *http.Request) {
	s.operateTask(w, r, "resolve", agentsdk.ActionAgentTasksResolve)
}
func (s *surface) reconcileTask(w http.ResponseWriter, r *http.Request) {
	s.operateTask(w, r, "reconcile", agentsdk.ActionAgentTasksReconcile)
}

func (s *surface) cancelTask(w http.ResponseWriter, r *http.Request) {
	principal, request, ok := taskOperationRequest(w, r, agentsdk.ActionAgentTasksCancel)
	if !ok {
		return
	}
	run, replayed, err := s.operations.Cancel(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("taskRunID")), request.Reason, principal)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": agentpersistence.ProjectAgentTaskRun(run), "replayed": replayed, "idempotency_key": strings.TrimSpace(r.Header.Get("Idempotency-Key"))})
}

func (s *surface) operateTask(w http.ResponseWriter, r *http.Request, kind, actionKey string) {
	principal, request, ok := taskOperationRequest(w, r, actionKey)
	if !ok {
		return
	}
	run, replayed, err := s.operations.Operate(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("taskRunID")), kind, strings.TrimSpace(r.Header.Get("Idempotency-Key")), request.Reason, principal)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": agentpersistence.ProjectAgentTaskRun(run), "replayed": replayed})
}

func taskOperationRequest(w http.ResponseWriter, r *http.Request, actionKey string) (principal modulehost.Principal, request struct{ Reason string }, ok bool) {
	principal, ok = authorizedActionPrincipal(r, actionKey)
	if !ok {
		writeCode(w, http.StatusForbidden, "agent.authorization.action_denied")
		return principal, request, false
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		writeCode(w, http.StatusBadRequest, "backend.idempotency.key_required")
		return principal, request, false
	}
	if r.ContentLength != 0 && !decode(w, r, &request) {
		return principal, request, false
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" {
		writeCode(w, http.StatusBadRequest, "agent.task.operation_evidence_required")
		return principal, request, false
	}
	return principal, request, true
}
