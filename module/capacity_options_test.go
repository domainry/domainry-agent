package module

import (
	"testing"
	"time"
)

func TestOptionsFromEnvironmentLoadsConversationCapacityLimits(t *testing.T) {
	t.Setenv("AGENT_CONVERSATION_WORKERS", "4")
	t.Setenv("AGENT_CONVERSATION_MAX_PARALLEL_TOOLS", "3")
	t.Setenv("AGENT_CONVERSATION_MAX_QUEUED_PER_USER", "9")
	t.Setenv("AGENT_CONVERSATION_MAX_QUEUED_PER_WORKSPACE", "90")
	t.Setenv("AGENT_CONVERSATION_MAX_RUNNING_PER_USER", "2")
	t.Setenv("AGENT_CONVERSATION_MAX_RUNNING_PER_WORKSPACE", "4")
	t.Setenv("AGENT_CONVERSATION_RUN_TIMEOUT", "6m")
	t.Setenv("AGENT_CONVERSATION_EXTERNAL_CALL_TIMEOUT", "50s")
	t.Setenv("AGENT_CONVERSATION_MODEL_MAX_ATTEMPTS", "4")
	t.Setenv("AGENT_CONVERSATION_MODEL_RETRY_BASE_DELAY", "100ms")
	t.Setenv("AGENT_CONVERSATION_MODEL_RETRY_MAX_DELAY", "12s")
	options := OptionsFromEnvironment().ConversationOptions
	if options.Workers != 4 || options.MaxParallelTools != 3 || options.MaxQueuedPerUser != 9 || options.MaxQueuedPerWorkspace != 90 || options.MaxRunningPerUser != 2 || options.MaxRunningPerWorkspace != 4 || options.RunTimeout != 6*time.Minute || options.ExternalCallTimeout != 50*time.Second || options.MaxModelAttempts != 4 || options.ModelRetryBaseDelay != 100*time.Millisecond || options.ModelRetryMaxDelay != 12*time.Second {
		t.Fatalf("options=%+v", options)
	}
	t.Setenv("AGENT_CONVERSATION_WORKERS", "invalid")
	if OptionsFromEnvironment().ConversationOptions.Workers >= 0 {
		t.Fatal("invalid worker limit did not remain invalid for service validation")
	}
}

func TestOptionsFromEnvironmentLoadsTaskModel(t *testing.T) {
	t.Setenv("AGENT_TASK_MODEL_PROVIDER", "gateway")
	t.Setenv("AGENT_TASK_MODEL_PROTOCOL", "responses")
	t.Setenv("AGENT_TASK_MODEL_BASE_URL", "https://models.example.test")
	t.Setenv("AGENT_PROVIDER_API_KEY", "shared-secret")
	t.Setenv("AGENT_TASK_MODEL", "vision-model")
	t.Setenv("AGENT_TASK_MODEL_CONTEXT_TOKENS", "128000")
	t.Setenv("AGENT_TASK_MODEL_IMAGE_INPUT", "true")
	t.Setenv("AGENT_TASK_MODEL_STRUCTURED_OUTPUT", "true")
	t.Setenv("AGENT_TASK_MODEL_REASONING_EFFORTS", "low, high")
	t.Setenv("AGENT_TASK_MODEL_REASONING_EFFORT", "high")
	options := OptionsFromEnvironment()
	if options.TaskModelProviderName != "gateway" || options.TaskModelProtocol != "responses" || options.TaskModelBaseURL != "https://models.example.test" || options.TaskModelAPIKey != "shared-secret" || options.TaskModelName != "vision-model" || options.TaskModelContextTokenLimit != 128000 || !options.TaskModelImageInput || !options.TaskModelStructuredOutput || len(options.TaskModelReasoningEfforts) != 2 || options.TaskModelDefaultReasoningEffort != "high" {
		t.Fatalf("task model options=%+v", options)
	}
}
