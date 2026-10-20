package server

import (
	"errors"
	"net/http"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/httpresponse"
)

// Preserve domain failures through the repository RPC boundary. A quota or
// stale fence is not a transient database failure; raw database/host error
// text must not become a client-visible response.
func writeRepositoryError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "agent.saas.repository_failed"
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
			code = failure.Code
		}
	}
	httpresponse.WriteError(w, nil, httpresponse.Failure{Status: status, Code: code, Cause: err})
}
