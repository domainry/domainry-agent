package module

import (
	"testing"
	"time"
)

func TestPlanningModelConfigurationDoesNotChangeExecutionModel(t *testing.T) {
	t.Setenv("AGENT_CONVERSATION_PROVIDER", "gateway")
	t.Setenv("AGENT_CONVERSATION_MODEL", "execution-model")
	t.Setenv("AGENT_CONVERSATION_MAX_OUTPUT_TOKENS", "131072")
	t.Setenv("AGENT_CONVERSATION_PLANNING_PROVIDER", "gateway")
	t.Setenv("AGENT_CONVERSATION_PLANNING_PROTOCOL", "responses")
	t.Setenv("AGENT_CONVERSATION_PLANNING_BASE_URL", "https://models.example.test")
	t.Setenv("AGENT_CONVERSATION_PLANNING_MODEL", "planning-model")
	t.Setenv("AGENT_CONVERSATION_PLANNING_MAX_OUTPUT_TOKENS", "2048")
	t.Setenv("AGENT_CONVERSATION_PLANNING_REASONING_EFFORTS", "none")
	t.Setenv("AGENT_CONVERSATION_PLANNING_REASONING_EFFORT", "none")
	t.Setenv("AGENT_CONVERSATION_PLANNING_TIMEOUT", "10s")
	t.Setenv("AGENT_PROVIDER_API_KEY", "shared-test-secret")
	options := OptionsFromEnvironment()
	plan := options.ConversationPlanningModelConfig
	if options.ConversationModel != "execution-model" || options.ConversationMaxOutputTokens != 131072 {
		t.Fatal("planning configuration changed execution")
	}
	if plan.Provider != "gateway" || plan.Protocol != "responses" || plan.BaseURL != "https://models.example.test" || plan.Model != "planning-model" || plan.MaxOutputTokens != 2048 || plan.APIKey != "shared-test-secret" || plan.ImageInput || plan.DefaultReasoningEffort != "none" || len(plan.ReasoningEfforts) != 1 || plan.ReasoningEfforts[0] != "none" || options.ConversationOptions.RunPlanningTimeout != 10*time.Second {
		t.Fatal("planning configuration did not retain its independent provider, protocol, credentials and limits")
	}
	t.Setenv("AGENT_CONVERSATION_PLANNING_MODEL_API_KEY", "dedicated-test-secret")
	if OptionsFromEnvironment().ConversationPlanningModelConfig.APIKey != "dedicated-test-secret" {
		t.Fatal("dedicated planning credential was ignored")
	}
	t.Setenv("AGENT_CONVERSATION_PLANNING_MAX_OUTPUT_TOKENS", "invalid")
	if OptionsFromEnvironment().ConversationPlanningModelConfig.MaxOutputTokens != -1 {
		t.Fatal("invalid planning output budget was silently defaulted")
	}
}
