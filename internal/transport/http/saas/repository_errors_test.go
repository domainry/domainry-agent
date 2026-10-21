package saas

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/httpresponse"
)

func TestRepositoryErrorsPreservePolicyFailuresWithoutDriverDetails(t *testing.T) {
	for _, sample := range []struct {
		class  agentsdk.ErrorClass
		status int
	}{
		{"bad_request", 400}, {"forbidden", 403}, {"not_found", 404},
		{"conflict", 409}, {"rate_limited", 429}, {"unavailable", 503},
	} {
		t.Run(string(sample.class), func(t *testing.T) {
			response := httptest.NewRecorder()
			writeRepositoryError(response, fmt.Errorf("private driver wrapper: %w", &agentsdk.Error{Class: sample.class, Code: "agent.task.example", Message: "private driver detail"}))
			var result httpresponse.ErrorResponse
			if json.Unmarshal(response.Body.Bytes(), &result) != nil || response.Code != sample.status || result.Code != "agent.task.example" || result.Message == "" {
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
