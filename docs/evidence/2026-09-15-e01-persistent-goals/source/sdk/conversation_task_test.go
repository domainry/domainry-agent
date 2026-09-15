package agentsdk

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBackgroundTaskConversationToolContractIsExplicitAndClosed(t *testing.T) {
	definition := BackgroundTaskConversationTool()
	if definition.Key != "task_start" || definition.Effect != "write" || definition.Idempotency != "key" || definition.ActionKey != ConversationToolActionPrefix+"task_start" {
		t.Fatalf("unexpected task definition: %#v", definition)
	}
	var input map[string]any
	if err := json.Unmarshal(definition.InputSchema, &input); err != nil {
		t.Fatal(err)
	}
	if input["additionalProperties"] != false {
		t.Fatal("task input must reject undeclared fields")
	}
	required, _ := input["required"].([]any)
	if strings.Join(stringsFromAny(required), ",") != "goal,input,allowed_tools,budget" {
		t.Fatalf("task input must require goal, input, scope and budget: %v", required)
	}
	raw := string(definition.InputSchema)
	for _, legacy := range []string{"process_id", "task_definition", "task_host", "application_id"} {
		if strings.Contains(raw, legacy) {
			t.Fatalf("independent conversation tasks must not expose legacy runtime field %q", legacy)
		}
	}
	if !strings.Contains(raw, `"max_steps"`) || !strings.Contains(raw, `"max_tool_calls"`) || !strings.Contains(raw, `"max_output_bytes"`) || !strings.Contains(raw, `"timeout_seconds"`) {
		t.Fatal("all background execution budgets must be explicit")
	}
	var output map[string]any
	if err := json.Unmarshal(definition.OutputSchema, &output); err != nil || output["additionalProperties"] != false {
		t.Fatalf("task receipt schema must be a closed object: %v %#v", err, output)
	}
}

func TestBackgroundTaskScopeAndPrompt(t *testing.T) {
	if (&ConversationWriteScope{}).Allows("task_start") || !(&ConversationWriteScope{BackgroundTasks: true}).Allows("task_start") {
		t.Fatal("task_start must require its own per-run write scope")
	}
	tools := PersonalConversationTools()
	counts := map[string]int{}
	for _, tool := range tools {
		counts[tool.Key]++
	}
	for _, key := range []string{"task_start", "task_get", "task_list", "task_cancel", "task_resume", "task_update"} {
		if counts[key] != 1 {
			t.Fatalf("%s registration count = %d", key, counts[key])
		}
	}
	prompt := ConversationTaskPrompt(ConversationTask{Goal: "  verify release  ", Input: "build 42"})
	if !strings.Contains(prompt, "Goal:\nverify release") || !strings.Contains(prompt, "Input:\nbuild 42") {
		t.Fatalf("unexpected frozen task prompt: %q", prompt)
	}
}

func TestBackgroundTaskControlToolsRequireExplicitScopeAndRoutes(t *testing.T) {
	if (&ConversationWriteScope{}).Allows("task_cancel") || !(&ConversationWriteScope{BackgroundTasks: true}).Allows("task_cancel") || !(&ConversationWriteScope{BackgroundTasks: true}).Allows("task_resume") || !(&ConversationWriteScope{BackgroundTasks: true}).Allows("task_update") {
		t.Fatal("task control must use only the background-task write scope")
	}
	definitions := BackgroundTaskControlConversationTools()
	if len(definitions) != 3 || definitions[0].Key != "task_cancel" || definitions[1].Key != "task_resume" || definitions[2].Key != "task_update" {
		t.Fatalf("task control definitions=%+v", definitions)
	}
	for _, definition := range definitions {
		if definition.Effect != "write" || definition.Idempotency != "key" || definition.ActionKey != ConversationToolActionPrefix+definition.Key {
			t.Fatalf("task control contract=%+v", definition)
		}
	}
	if !strings.Contains(string(definitions[2].InputSchema), `"audience":{"type":"string","minLength":1`) {
		t.Fatal("task agreement must name its intended audience")
	}
	routes := map[string]ConversationHTTPDefinition{}
	for _, route := range ConversationHTTPDefinitions() {
		routes[route.Operation] = route
	}
	if routes["tasks_cancel"].Pattern != "POST /agent/conversation-tasks/{taskID}/cancel" || routes["tasks_resume"].Pattern != "POST /agent/conversation-tasks/{taskID}/resume" || routes["tasks_update"].Pattern != "PATCH /agent/conversation-tasks/{taskID}/agreement" {
		t.Fatalf("task control routes=%+v", routes)
	}
}

func TestBackgroundTaskQueryToolsAreReadOnlyAndHideExecutorScope(t *testing.T) {
	definitions := BackgroundTaskQueryConversationTools()
	if len(definitions) != 2 || definitions[0].Key != "task_get" || definitions[1].Key != "task_list" {
		t.Fatalf("task query definitions=%+v", definitions)
	}
	for _, definition := range definitions {
		if definition.Effect != "read" || definition.Idempotency != "natural" || definition.ActionKey != ConversationToolActionPrefix+definition.Key || definition.MaxOutputBytes != 65536 {
			t.Fatalf("task query contract=%+v", definition)
		}
		if strings.Contains(string(definition.InputSchema), "definition_hash") || strings.Contains(string(definition.OutputSchema), "authorization_revision") || strings.Contains(string(definition.OutputSchema), "tool_scope") {
			t.Fatalf("executor scope leaked in %s contract", definition.Key)
		}
	}
	routes := map[string]ConversationHTTPDefinition{}
	for _, route := range ConversationHTTPDefinitions() {
		routes[route.Operation] = route
	}
	if routes["tasks_list"].Pattern != "GET /agent/conversation-tasks" || routes["tasks_get"].Pattern != "GET /agent/conversation-tasks/{taskID}" {
		t.Fatalf("task HTTP routes=%+v", routes)
	}
}

func stringsFromAny(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, _ := value.(string)
		out = append(out, text)
	}
	return out
}
