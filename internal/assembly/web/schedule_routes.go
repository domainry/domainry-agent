package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

// ScheduleRoutes exposes the product plan surface through the same bounded
// tool adapter used by conversations. The browser cannot supply owner scope,
// Scheduler targets, Action keys, provider routing, credentials, or raw cron.
func (h *Host) ScheduleRoutes() map[string]http.Handler {
	if h == nil || h.scheduleToolHost == nil {
		return nil
	}
	handler := http.HandlerFunc(h.handleSchedule)
	return map[string]http.Handler{
		"GET /app/product/plans":                  handler,
		"POST /app/product/plans":                 handler,
		"GET /app/product/plans/{planID}":         handler,
		"PUT /app/product/plans/{planID}":         handler,
		"POST /app/product/plans/{planID}/pause":  handler,
		"POST /app/product/plans/{planID}/resume": handler,
		"DELETE /app/product/plans/{planID}":      handler,
	}
}

func (h *Host) handleSchedule(w http.ResponseWriter, r *http.Request) {
	current, ok := identitysdk.RequestIdentityFromContext(r.Context())
	if !ok {
		writeScheduleError(w, http.StatusUnauthorized, "schedule.login_required")
		return
	}
	authority := toolsdk.Authority{Known: true, RuntimeID: h.runtimeID, WorkspaceID: current.Principal.WorkspaceID, UserID: current.Principal.UserID, RoleKey: current.Principal.RoleKey}
	key, arguments, write, err := scheduleHTTPRequest(r)
	if err != nil {
		writeScheduleError(w, http.StatusBadRequest, "schedule.arguments_invalid")
		return
	}
	definition, found := scheduleDefinition(key)
	if !found {
		writeScheduleError(w, http.StatusNotFound, "schedule.operation_not_found")
		return
	}
	idempotency := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if write && (!validScheduleHeader(idempotency) || len(idempotency) > 191) {
		writeScheduleError(w, http.StatusBadRequest, "schedule.idempotency_key_required")
		return
	}
	request := toolsdk.Request{
		Authority: authority, Call: toolsdk.Call{ID: "product-plan-" + idempotency, Name: key, Arguments: string(arguments)},
		Definition: definition, IdempotencyKey: idempotency,
	}
	var result toolsdk.Result
	if write {
		result, err = h.scheduleToolHost.ReconcileConversationTool(r.Context(), request)
	} else {
		result, err = h.scheduleToolHost.InvokeConversationTool(r.Context(), request)
	}
	if err != nil {
		writeScheduleToolError(w, err)
		return
	}
	if result.Status != "completed" || !json.Valid(result.Content) {
		writeScheduleError(w, http.StatusServiceUnavailable, "schedule.result_uncertain")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Content)
}

func scheduleHTTPRequest(r *http.Request) (string, json.RawMessage, bool, error) {
	planID := strings.TrimSpace(r.PathValue("planID"))
	if planID != "" && !validScheduleHeader(planID) {
		return "", nil, false, fmt.Errorf("invalid plan id")
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && planID == "":
		limit := 0
		if raw := r.URL.Query().Get("limit"); raw != "" {
			var err error
			limit, err = strconv.Atoi(raw)
			if err != nil {
				return "", nil, false, err
			}
		}
		return marshalScheduleArguments(map[string]any{"status": r.URL.Query().Get("status"), "cursor": r.URL.Query().Get("cursor"), "limit": limit})
	case r.Method == http.MethodPost && planID == "":
		body, err := readScheduleBody(r)
		return toolsdk.ScheduleCreateToolKey, body, true, err
	case r.Method == http.MethodGet:
		body, _ := json.Marshal(map[string]any{"plan_id": planID})
		return toolsdk.ScheduleGetToolKey, body, false, nil
	case r.Method == http.MethodPut:
		return mergeSchedulePlanID(r, toolsdk.ScheduleUpdateToolKey, planID)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/pause"):
		return mergeSchedulePlanID(r, toolsdk.SchedulePauseToolKey, planID)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/resume"):
		return mergeSchedulePlanID(r, toolsdk.ScheduleResumeToolKey, planID)
	case r.Method == http.MethodDelete:
		return mergeSchedulePlanID(r, toolsdk.ScheduleDeleteToolKey, planID)
	default:
		return "", nil, false, fmt.Errorf("unsupported schedule route")
	}
}

func mergeSchedulePlanID(r *http.Request, key, planID string) (string, json.RawMessage, bool, error) {
	body, err := readScheduleBody(r)
	if err != nil {
		return "", nil, false, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return "", nil, false, fmt.Errorf("invalid body")
	}
	if _, supplied := fields["plan_id"]; supplied {
		return "", nil, false, fmt.Errorf("plan id is path-owned")
	}
	fields["plan_id"], _ = json.Marshal(planID)
	merged, _ := json.Marshal(fields)
	return key, merged, true, nil
}

func readScheduleBody(r *http.Request) (json.RawMessage, error) {
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil || len(rawBody) > 64<<10 {
		return nil, fmt.Errorf("body too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	decoder.UseNumber()
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil || len(raw) == 0 || !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return raw, nil
}

func marshalScheduleArguments(fields map[string]any) (string, json.RawMessage, bool, error) {
	for key, value := range fields {
		switch typed := value.(type) {
		case string:
			if typed == "" {
				delete(fields, key)
			}
		case int:
			if typed == 0 {
				delete(fields, key)
			}
		}
	}
	raw, err := json.Marshal(fields)
	return toolsdk.ScheduleListToolKey, raw, false, err
}

func scheduleDefinition(key string) (toolsdk.Definition, bool) {
	for _, definition := range toolmodule.ScheduleDefinitions() {
		if definition.Key == key {
			return definition, true
		}
	}
	return toolsdk.Definition{}, false
}

func validScheduleHeader(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character == 0x7f {
			return false
		}
	}
	return true
}

func writeScheduleToolError(w http.ResponseWriter, err error) {
	var toolError *toolsdk.Error
	if !errors.As(err, &toolError) {
		writeScheduleError(w, http.StatusServiceUnavailable, "schedule.service_unavailable")
		return
	}
	status := http.StatusServiceUnavailable
	switch toolError.Class {
	case "bad_request":
		status = http.StatusBadRequest
	case "forbidden":
		status = http.StatusForbidden
	case "not_found":
		status = http.StatusNotFound
	case "conflict":
		status = http.StatusConflict
	}
	writeScheduleError(w, status, toolError.Code)
}

func writeScheduleError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
}
