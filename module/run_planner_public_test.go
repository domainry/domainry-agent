package module_test

import (
	"context"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
)

type publishedRunPlanner struct{}

func (publishedRunPlanner) PlanConversationRun(_ context.Context, request agentsdk.ConversationRunPlanningRequest) (agentsdk.ConversationRunPlan, error) {
	return agentsdk.ConversationRunPlan{
		Key:   "product_plan",
		Label: "Product-owned plan",
		Steps: []agentsdk.ConversationRunPlanStep{{
			Key:     "understand",
			Label:   "Understand request",
			Binding: agentsdk.ConversationRunPlanBinding{Kind: agentsdk.ConversationRunPlanBindingModel},
		}},
	}, nil
}

func TestPublishedModuleAcceptsProductOwnedRunPlanner(t *testing.T) {
	var planner agentsdk.ConversationRunPlanner = publishedRunPlanner{}
	options := agentmodule.ConversationOptions{RunPlanner: planner}
	if options.RunPlanner == nil {
		t.Fatal("public module dropped the product run planner")
	}
}

