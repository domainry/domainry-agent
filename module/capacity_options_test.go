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
