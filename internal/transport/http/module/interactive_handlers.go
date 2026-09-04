package module

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodulehost "github.com/domainry/domainry-agent-sdk/modulehost"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type interactiveRunRequest struct {
	Message           string         `json:"message"`
	ResponseMode      string         `json:"response_mode,omitempty"`
	TimeoutSeconds    int            `json:"timeout_seconds,omitempty"`
	NewSession        bool           `json:"new_session,omitempty"`
	ExternalSessionID string         `json:"external_session_id,omitempty"`
	Context           map[string]any `json:"context,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	IdempotencyKey    string         `json:"idempotency_key,omitempty"`
}

func (s *adapter) runInteractive(w http.ResponseWriter, r *http.Request) {
	var payload interactiveRunRequest
	if !decode(w, r, &payload) {
		return
	}
	mode := strings.TrimSpace(payload.ResponseMode)
	if mode == "" {
		mode = "blocking"
	}
	if mode != "blocking" {
		writeCode(w, http.StatusBadRequest, "agent.interactive.response_mode_unsupported")
		return
	}
	request, err := s.interactiveExecutionRequest(r, payload)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := s.execution.Execute(r.Context(), request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *adapter) streamInteractive(w http.ResponseWriter, r *http.Request) {
	var payload interactiveRunRequest
	if !decode(w, r, &payload) {
		return
	}
	request, err := s.interactiveExecutionRequest(r, payload)
	if err != nil {
		writeError(w, err)
		return
	}
	acceptedID := interactiveStreamEventID(request.Principal.WorkspaceID, request.Principal.UserID, request.IdempotencyKey, "accepted")
	if cursor := strings.TrimSpace(r.Header.Get("Last-Event-ID")); cursor != "" && cursor != acceptedID {
		writeCode(w, http.StatusConflict, "agent.interactive.stream_cursor_invalid")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if strings.TrimSpace(r.Header.Get("Last-Event-ID")) == "" {
		_, _ = w.Write([]byte("id: " + acceptedID + "\nevent: accepted\ndata: {\"status\":\"running\"}\n\n"))
		flushInteractiveEvent(w)
	}
	result, runErr := s.execution.Execute(r.Context(), request)
	event, body := "result", any(result)
	if runErr != nil {
		event, body = "error", map[string]any{"code": errorCode(runErr)}
	}
	encoded, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		encoded, event = []byte(`{"code":"agent.interactive.response_invalid"}`), "error"
	}
	eventID := interactiveStreamEventID(request.Principal.WorkspaceID, request.Principal.UserID, request.IdempotencyKey, event)
	_, _ = w.Write([]byte("id: " + eventID + "\nevent: " + event + "\ndata: " + string(encoded) + "\n\n"))
	flushInteractiveEvent(w)
}

func (s *adapter) interactiveExecutionRequest(r *http.Request, payload interactiveRunRequest) (agentapplication.InteractiveExecutionRequest, error) {
	identity, ok := identitysdk.RequestIdentityFromContext(r.Context())
	if !ok || !identity.Principal.Known {
		return agentapplication.InteractiveExecutionRequest{}, &agentsdk.Error{Class: "forbidden", Code: "backend.workspace_scope_required"}
	}
	principal := agentmodulehost.Principal{
		Known: true, WorkspaceID: strings.TrimSpace(identity.Principal.WorkspaceID), UserID: strings.TrimSpace(identity.Principal.UserID),
		RoleKey: strings.TrimSpace(identity.Principal.RoleKey), AuthorizationRevision: strings.TrimSpace(identity.Principal.AuthorizationRevision),
		RequestID: strings.TrimSpace(r.Header.Get("X-Request-ID")), CorrelationID: strings.TrimSpace(r.Header.Get("X-Correlation-ID")),
		CausationID: strings.TrimSpace(r.Header.Get("X-Causation-ID")),
	}
	context, err := s.execution.ResolveContext(r.Context(), agentmodulehost.InteractiveContextRequest{
		Principal:     principal,
		EntrypointKey: valueOr(strings.TrimSpace(r.Header.Get("X-Agent-Entrypoint-Key")), safeString(payload.Context["entrypoint_key"])),
		RouteKey:      valueOr(strings.TrimSpace(r.Header.Get("X-Route-Key")), safeString(payload.Context["route_key"])),
		ObjectKey:     safeString(payload.Context["object_key"]), RecordID: safeString(payload.Context["record_id"]),
		SelectedRecordIDs:     safeStringList(payload.Context, "selected_record_ids", "selected_records"),
		Locale:                valueOr(strings.TrimSpace(r.Header.Get("X-Locale")), safeString(payload.Context["locale"])),
		Timezone:              valueOr(strings.TrimSpace(r.Header.Get("X-Timezone")), safeString(payload.Context["timezone"])),
		AvailableOperationIDs: safeStringList(payload.Context, "available_operation_ids", "available_operations"),
	})
	if err != nil {
		return agentapplication.InteractiveExecutionRequest{}, err
	}
	idempotencyKey := valueOr(strings.TrimSpace(r.Header.Get("Idempotency-Key")), strings.TrimSpace(payload.IdempotencyKey))
	if idempotencyKey == "" {
		return agentapplication.InteractiveExecutionRequest{}, &agentsdk.Error{Class: "bad_request", Code: "agent.interactive.idempotency_required"}
	}
	return agentapplication.InteractiveExecutionRequest{
		SessionID: externalSessionID(payload.ExternalSessionID, principal), IdempotencyKey: idempotencyKey,
		Message: payload.Message, Context: context, Principal: principal,
	}, nil
}

func externalSessionID(requested string, principal agentmodulehost.Principal) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	return "workspace:" + sessionPart(principal.WorkspaceID) + ":user:" + sessionPart(principal.UserID) + ":dialog"
}

func sessionPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unspecified"
	}
	var out strings.Builder
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' || ch == '.' {
			out.WriteRune(ch)
		}
	}
	if out.Len() == 0 {
		return strconv.Itoa(len(value))
	}
	return out.String()
}

func safeString(value any) string {
	if typed, ok := value.(string); ok {
		return strings.TrimSpace(typed)
	}
	return ""
}

func safeStringList(values map[string]any, keys ...string) []string {
	for _, key := range keys {
		var out []string
		switch typed := values[key].(type) {
		case []string:
			out = append(out, typed...)
		case []any:
			for _, item := range typed {
				if value := safeString(item); value != "" {
					out = append(out, value)
				}
			}
		}
		if len(out) > 100 {
			out = out[:100]
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func interactiveStreamEventID(workspaceID, userID, key, event string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(key)))
	sequence := "1"
	if event != "accepted" {
		sequence = "2"
	}
	return "agent_" + hex.EncodeToString(digest[:16]) + ":" + sequence
}

func flushInteractiveEvent(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func errorCode(err error) string {
	if coded, ok := err.(interface{ ErrorCode() string }); ok {
		return coded.ErrorCode()
	}
	return "backend.internal"
}
