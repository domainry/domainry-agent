package application

import (
	"encoding/json"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestArgumentFeedbackLocatesRealArtifactEditSchemaFailure(t *testing.T) {
	catalog, err := compileConversationTools(agentsdk.ArtifactConversationTools())
	if err != nil {
		t.Fatal(err)
	}
	err = validateToolJSON(catalog["artifact_edit"].input, []byte(`{"id":"art_test","expected_version":1,"patch":{"kind":"markdown","text":[{"find":"private original text","replace":"replacement"}]}}`))
	if err == nil {
		t.Fatal("invalid patch accepted")
	}
	result := conversationArgumentFailure(err)
	var out struct {
		Issues []struct {
			Path, Keyword string
			Properties    []string
		}
	}
	if json.Unmarshal(result.Content, &out) != nil || len(out.Issues) != 1 || out.Issues[0].Path != "/patch" || out.Issues[0].Keyword != "additionalProperties" || len(out.Issues[0].Properties) != 1 || out.Issues[0].Properties[0] != "kind" {
		t.Fatalf("missing actionable field feedback: %s", result.Content)
	}
	if strings.Contains(string(result.Content), "private original text") || strings.Contains(string(result.Content), "agent.invalid") {
		t.Fatal("feedback echoed values or internal schema location")
	}
}

func TestArgumentFeedbackBoundsManyErrorsAndRejectsInvalidJSON(t *testing.T) {
	schema, err := compileConversationSchema(json.RawMessage(`{"type":"array","items":{"type":"integer"}}`))
	if err != nil {
		t.Fatal(err)
	}
	values := make([]string, 100)
	for i := range values {
		values[i] = strings.Repeat("private", 100)
	}
	raw, _ := json.Marshal(values)
	result := conversationArgumentFailure(validateToolJSON(schema, raw))
	var out struct{ Issues []json.RawMessage }
	if json.Unmarshal(result.Content, &out) != nil || len(out.Issues) != 4 || len(result.Content) > 8192 || strings.Contains(string(result.Content), "private") {
		t.Fatal("unbounded or value-bearing feedback")
	}
	invalid := conversationArgumentFailure(validateToolJSON(schema, []byte(`{"password":"hidden-value"`)))
	if !json.Valid(invalid.Content) || strings.Contains(string(invalid.Content), "hidden-value") {
		t.Fatal("invalid JSON feedback echoed input")
	}
}
