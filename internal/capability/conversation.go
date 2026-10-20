package capability

import (
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	actioncontract "github.com/domainry/domainry-foundation/action"
)

// ConversationTools is the Agent-owned built-in catalog. Product modules add
// their own capabilities through ConversationOptions.ToolDefinitions.
func ConversationTools() []agentsdk.ConversationToolDefinition {
	definitions := make([]agentsdk.ConversationToolDefinition, 0, 32)
	for _, item := range []struct{ key, version string }{
		{"ask_user", "1"},
		{"time_now", "1"},
		{"calculate", "1"},
		{"history_search", "1"},
		{"history_read", "1"},
		{"memory_search", "2"},
		{"memory_save", "2"},
		{"memory_forget", "2"},
	} {
		definition, found := agentsdk.PersonalConversationToolDefinition(item.key, item.version)
		if found {
			definitions = append(definitions, definition)
		}
	}
	definitions = append(definitions,
		agentsdk.ConversationToolResultReadDefinition(),
		agentsdk.ConversationExecutionReadDefinition(),
		agentsdk.ConversationCodeTool(),
		agentsdk.ConversationPlanUpdateTool(),
		agentsdk.ConversationTaskCompletionSubmitTool(),
		agentsdk.BackgroundTaskConversationTool(),
	)
	definitions = append(definitions, agentsdk.ConversationCodingTools()...)
	definitions = append(definitions, agentsdk.BackgroundTaskQueryConversationTools()...)
	definitions = append(definitions, agentsdk.BackgroundTaskControlConversationTools()...)
	return append(definitions, agentsdk.ConversationCollaborationTools()...)
}

// AuthorizationActions projects the shared SDK manifest down to capabilities
// implemented by Agent itself. Business hosts publish their injected tools as
// separate actions when they bind them.
func AuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	definitions, err := agentsdk.AgentAuthorizationActions()
	if err != nil {
		return nil, err
	}
	toolKeys := map[string]bool{}
	for _, definition := range append(ConversationTools(), agentsdk.ArtifactConversationTools()...) {
		toolKeys[definition.Key] = true
	}
	result := make([]actioncontract.ActionDefinition, 0, len(definitions))
	for _, definition := range definitions {
		switch {
		case strings.HasPrefix(definition.Key, agentsdk.ConversationActionPrefix):
			if ConversationOperationSupported(strings.TrimPrefix(definition.Key, agentsdk.ConversationActionPrefix)) {
				result = append(result, definition)
			}
		case strings.HasPrefix(definition.Key, agentsdk.ConversationToolActionPrefix):
			if toolKeys[strings.TrimPrefix(definition.Key, agentsdk.ConversationToolActionPrefix)] {
				result = append(result, definition)
			}
		default:
			result = append(result, definition)
		}
	}
	return result, nil
}

// ConversationOperationSupported reports whether an SDK HTTP definition is
// implemented by Agent. The allow-list prevents capabilities owned by another
// module from becoming Agent routes merely because they exist in a shared SDK.
func ConversationOperationSupported(operation string) bool {
	switch operation {
	case "create", "list", "get", "update", "delete", "send", "messages", "run",
		"provenance_publish", "conversation_fork", "result_read", "events", "stream",
		"cancel", "resume", "respond", "feedback_create":
		return true
	case "attachments_upload", "attachments_list", "attachments_get", "attachments_download", "attachments_delete":
		return true
	}
	for _, prefix := range []string{
		"agents_", "delegations_", "external_agent_", "skills_", "improvements_",
		"trajectory_", "memories_", "tasks_", "artifacts_",
	} {
		if len(operation) > len(prefix) && operation[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
