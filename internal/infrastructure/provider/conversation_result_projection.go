package provider

import (
	"encoding/json"
	"fmt"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Model-hidden fields are a trusted output-schema annotation, not a model
// argument. Durable receipts and frozen messages remain byte-identical for
// evidence validation and result references; only provider serialization omits
// server-only metadata. The exact same projection is used for byte sizing.
func projectModelToolContent(content string, definition agentsdk.ConversationToolDefinition) (string, error) {
	var schema struct {
		Hidden []string `json:"x-agent-model-hidden"`
	}
	if json.Unmarshal(definition.OutputSchema, &schema) != nil || len(schema.Hidden) == 0 {
		return content, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		return "", fmt.Errorf("invalid tool result envelope: %w", err)
	}
	raw, exists := envelope["content"]
	if !exists {
		return content, nil
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("invalid tool result content: %w", err)
	}
	if result == nil {
		return content, nil
	}
	for _, key := range schema.Hidden {
		delete(result, key)
	}
	envelope["content"], _ = json.Marshal(result)
	encoded, err := json.Marshal(envelope)
	return string(encoded), err
}
