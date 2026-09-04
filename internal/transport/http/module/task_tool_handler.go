package module

import (
	"net/http"
	"strings"

	agentapplication "github.com/domainry/domainry-agent/internal/application"
)

type taskToolInvokeRequest struct {
	Credential     string         `json:"credential"`
	WorkspaceID    string         `json:"workspace_id"`
	TaskRunID      string         `json:"task_run_id"`
	Tool           string         `json:"tool"`
	Input          map[string]any `json:"input"`
	IdempotencyKey string         `json:"idempotency_key"`
}

func (s *adapter) invokeTaskTool(w http.ResponseWriter, r *http.Request) {
	var payload taskToolInvokeRequest
	if !decode(w, r, &payload) {
		return
	}
	if strings.TrimSpace(payload.Credential) == "" || strings.TrimSpace(payload.WorkspaceID) == "" || strings.TrimSpace(payload.TaskRunID) == "" || strings.TrimSpace(payload.Tool) == "" || strings.TrimSpace(payload.IdempotencyKey) == "" {
		writeCode(w, http.StatusBadRequest, "agent.tool.request_invalid")
		return
	}
	result, err := s.taskTools.Invoke(r.Context(), agentapplication.TaskToolInvocation{
		Credential: payload.Credential, WorkspaceID: payload.WorkspaceID, TaskRunID: payload.TaskRunID,
		Tool: payload.Tool, Input: cloneMap(payload.Input), IdempotencyKey: payload.IdempotencyKey,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
