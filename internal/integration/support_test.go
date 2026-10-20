package integration_test

import (
	"context"
	"encoding/json"
	"sync/atomic"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
)

func newAgentModuleFactory(options agentmodule.Options) *agentmodule.Factory {
	return agentmodule.NewFactory(options)
}

func mustJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

type executionPauseGate struct{ pause atomic.Bool }

func (gate *executionPauseGate) AuthorizeConversationExecution(ctx context.Context, in agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
	if in.Stage == "execute" && gate.pause.Load() {
		<-ctx.Done()
		return false, ctx.Err()
	}
	return in.Authority == conversationAuthority(), nil
}
