package server

import (
	"errors"
	"net/http"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Preserve domain failures through the repository RPC boundary. A quota or
// stale fence is not a transient database failure; raw database/host error
// text must not become a client-visible response.
func writeRepositoryError(w http.ResponseWriter, err error) {
	status, code, class, retryable := http.StatusInternalServerError, "agent.saas.repository_failed", "internal", false
	var failure *agentsdk.Error
	if errors.As(err, &failure) {
		switch failure.Class {
		case "bad_request":
			status = http.StatusBadRequest
		case "forbidden":
			status = http.StatusForbidden
		case "not_found":
			status = http.StatusNotFound
		case "conflict":
			status = http.StatusConflict
		case "rate_limited":
			status = http.StatusTooManyRequests
		case "unavailable":
			status = http.StatusServiceUnavailable
		}
		if status != http.StatusInternalServerError {
			code, class, retryable = failure.Code, failure.Class, failure.Retryable
		}
	}
	writeJSON(w, status, struct {
		Code      string `json:"code"`
		Class     string `json:"class"`
		Retryable bool   `json:"retryable"`
	}{code, class, retryable})
}
