package application

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

func TestTaskCompletionValidatesTheFullOutputContract(t *testing.T) {
	var schema map[string]any
	_ = json.Unmarshal([]byte(`{"type":"object","properties":{"rows":{"type":"array","minItems":1,"items":{"type":"object","properties":{"amount":{"type":"integer","minimum":0},"currency":{"enum":["CNY","USD"]}},"required":["amount","currency"],"additionalProperties":false}},"day":{"type":"string","format":"date"}},"required":["rows","day"],"additionalProperties":false}`), &schema)
	authority := modulehost.TaskAuthorization{Task: agentsdk.AgentTaskDefinition{OutputSchema: schema}, Evidence: agentmodel.AgentAuthorizationEvidence{AllowedOutcomes: []string{"success"}}}
	for _, sample := range []struct {
		name, raw string
		valid     bool
	}{
		{"valid", `{"rows":[{"amount":123,"currency":"CNY"}],"day":"2026-09-10"}`, true},
		{"wrong nested type", `{"rows":[{"amount":"123","currency":"CNY"}],"day":"2026-09-10"}`, false},
		{"wrong enum", `{"rows":[{"amount":123,"currency":"EUR"}],"day":"2026-09-10"}`, false},
		{"negative amount", `{"rows":[{"amount":-1,"currency":"CNY"}],"day":"2026-09-10"}`, false},
		{"extra field", `{"rows":[{"amount":123,"currency":"CNY","hidden":true}],"day":"2026-09-10"}`, false},
		{"invalid date", `{"rows":[{"amount":123,"currency":"CNY"}],"day":"2026-02-30"}`, false},
		{"empty array", `{"rows":[],"day":"2026-09-10"}`, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var output map[string]any
			_ = json.Unmarshal([]byte(sample.raw), &output)
			_, err := taskCompletion(authority, agentsdk.TaskResult{Status: agentsdk.ProviderRunCompleted, Outcome: "success", Output: output}, "provider-run", agentmodel.AgentTaskExecutionEvidence{})
			if (err == nil) != sample.valid {
				t.Fatalf("valid=%t error=%v", sample.valid, err)
			}
			// Both entry points compile the same registered JSON Schema engine.
			compiled, err := compileConversationSchema(json.RawMessage(conversationJSONText(schema)))
			if err != nil {
				t.Fatal(err)
			}
			if valid := (validateToolJSON(compiled, []byte(sample.raw)) == nil); valid != sample.valid {
				t.Fatalf("conversation contract differs: %t", valid)
			}
		})
	}
	var fetched atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched.Add(1)
		_, _ = w.Write([]byte(`{"type":"integer"}`))
	}))
	defer remote.Close()
	authority.Task.OutputSchema = map[string]any{"type": "object", "properties": map[string]any{"amount": map[string]any{"$ref": remote.URL + "/changed-schema.json"}}}
	if _, err := taskCompletion(authority, agentsdk.TaskResult{Status: agentsdk.ProviderRunCompleted, Outcome: "success", Output: map[string]any{"amount": 1}}, "provider-run", agentmodel.AgentTaskExecutionEvidence{}); err == nil {
		t.Fatal("execution accepted a remote mutable schema")
	}
	if fetched.Load() != 0 {
		t.Fatal("execution fetched a remote schema")
	}
}
