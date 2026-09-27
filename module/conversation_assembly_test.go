package module

import (
	"context"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type conversationRuntimeHostStub struct {
	code agentsdk.ConversationCodeRuntime
}

func (conversationRuntimeHostStub) ConversationAuthorizer() agentsdk.ConversationToolAuthorizer {
	return nil
}

func (conversationRuntimeHostStub) ConversationBusinessSource() agentsdk.ConversationBusinessSource {
	return nil
}

func (host conversationRuntimeHostStub) ConversationCodeRuntime() agentsdk.ConversationCodeRuntime {
	return host.code
}

type conversationCodeRuntimeStub struct{}

func (conversationCodeRuntimeStub) ExecuteConversationCode(context.Context, agentsdk.ConversationCodeExecution, agentsdk.ConversationCodeDispatcher) (agentsdk.ConversationCodeResult, error) {
	return agentsdk.ConversationCodeResult{}, nil
}

type toolCapableConversationModelStub struct{}

func (toolCapableConversationModelStub) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, nil
}

func (toolCapableConversationModelStub) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "test", Model: "test"}
}

func (toolCapableConversationModelStub) StreamConversationStep(context.Context, agentsdk.ConversationStepRequest, func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	return agentsdk.ConversationStepResult{}, nil
}

func TestHostExecutionRuntimesRequireToolCapableModel(t *testing.T) {
	code := conversationCodeRuntimeStub{}
	host := conversationRuntimeHostStub{code: code}

	withoutModel := ConversationOptions{}
	bindHostExecutionRuntimes(&withoutModel, nil, host)
	if withoutModel.CodeRuntime != nil {
		t.Fatal("management-only conversation surface inherited an unusable code Runtime")
	}

	withModel := ConversationOptions{}
	bindHostExecutionRuntimes(&withModel, toolCapableConversationModelStub{}, host)
	if withModel.CodeRuntime == nil {
		t.Fatal("tool-capable conversation model did not inherit the host code Runtime")
	}
}
