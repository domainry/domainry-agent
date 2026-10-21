package module

import (
	"net/http/httptest"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type classificationCapture struct {
	*httptest.ResponseRecorder
	kind string
	code string
}

func (capture *classificationCapture) RecordError(kind, code string) {
	capture.kind, capture.code = kind, code
}

func TestAgentErrorClassificationMatrix(t *testing.T) {
	tests := []struct {
		class agentsdk.ErrorClass
		code  string
		kind  string
	}{
		{"unavailable", "agent.database_unavailable", "unavailable"},
		{"unavailable", "agent.dependency_unavailable", "unavailable"},
		{"conflict", "agent.task_fence_rejected", "conflict"},
		{"bad_request", "agent.validation_failed", "bad_request"},
		{"forbidden", "agent.permission_denied", "forbidden"},
	}
	for _, test := range tests {
		capture := &classificationCapture{ResponseRecorder: httptest.NewRecorder()}
		writeError(capture, &agentsdk.Error{Class: test.class, Code: test.code})
		if capture.kind != test.kind || capture.code != test.code {
			t.Fatalf("class=%q kind=%q code=%q", test.class, capture.kind, capture.code)
		}
	}
}
