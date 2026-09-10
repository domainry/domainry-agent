package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestRepositoryErrorsPreservePolicyFailuresWithoutDriverDetails(t *testing.T) {
	for _, sample := range []struct {
		class     string
		status    int
		retryable bool
	}{
		{"bad_request", 400, false}, {"forbidden", 403, false}, {"not_found", 404, false},
		{"conflict", 409, false}, {"rate_limited", 429, false}, {"unavailable", 503, true},
	} {
		t.Run(sample.class, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeRepositoryError(response, fmt.Errorf("private driver wrapper: %w", &agentsdk.Error{Class: sample.class, Code: "agent.task.example", Message: "private driver detail", Retryable: sample.retryable}))
			var result struct {
				Code, Class string
				Retryable   *bool
			}
			if json.Unmarshal(response.Body.Bytes(), &result) != nil || response.Code != sample.status || result.Code != "agent.task.example" || result.Class != sample.class || result.Retryable == nil || *result.Retryable != sample.retryable {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private") {
				t.Fatal("driver detail leaked")
			}
		})
	}
	response := httptest.NewRecorder()
	writeRepositoryError(response, errors.New("private database connection details"))
	if response.Code != 500 || strings.Contains(response.Body.String(), "private") || !strings.Contains(response.Body.String(), "agent.saas.repository_failed") {
		t.Fatal("unknown error not sanitized")
	}
}
