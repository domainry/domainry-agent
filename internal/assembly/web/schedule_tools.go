package web

import (
	"context"
	"fmt"

	agent "github.com/domainry/domainry-agent-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

func configureScheduleDefinitions(options *Options) error {
	if !options.ScheduleTools {
		return nil
	}
	if options.ScheduledPlans == nil {
		return fmt.Errorf("schedule tools require a Scheduler plan service")
	}
	definitions := toolmodule.ScheduleDefinitions()
	for _, definition := range definitions {
		for _, existing := range append(append([]agent.ConversationToolDefinition{}, options.ToolDefinitions...), options.Agent.ConversationOptions.ToolDefinitions...) {
			if existing.Key == definition.Key {
				return fmt.Errorf("schedule tool is already defined: %s", definition.Key)
			}
		}
	}
	options.ToolDefinitions = append(append([]agent.ConversationToolDefinition{}, options.ToolDefinitions...), definitions...)
	options.Agent.ConversationOptions.ToolDefinitions = append(append([]agent.ConversationToolDefinition{}, options.Agent.ConversationOptions.ToolDefinitions...), definitions...)
	return nil
}

func (h *Host) bindScheduleTools(options *Options) error {
	previous := options.Agent.ConversationOptions.AssembleTools
	options.Agent.ConversationOptions.AssembleTools = func(base agent.ConversationToolHost) (agent.ConversationToolHost, error) {
		if previous != nil {
			var err error
			base, err = previous(base)
			if err != nil {
				return nil, err
			}
		}
		// The adapter receives the already composed, currently authorized base
		// catalog. It can bind selected background tools to their Action keys
		// without knowing Agent or any concrete tool implementation.
		adapter := &toolmodule.ScheduleAdapter{
			Plans: options.ScheduledPlans, ProductKey: options.ApplicationKey,
			Catalog: base, Authorize: h.AuthorizeConversationTool,
		}
		registry := toolmodule.NewRegistry()
		if err := adapter.Register(registry); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(toolmodule.ScheduleDefinitions()))
		for _, definition := range toolmodule.ScheduleDefinitions() {
			keys = append(keys, definition.Key)
		}
		selected, err := registry.Select(keys)
		if err != nil {
			return nil, err
		}
		h.scheduleToolAvailability = adapter
		h.scheduleToolHost = selected
		return toolmodule.Combine(base, selected, keys)
	}
	return nil
}

func (h *Host) scheduleToolAvailable(ctx context.Context, authority toolsdk.Authority, key string) (bool, bool, error) {
	if h.scheduleToolAvailability == nil {
		return false, false, nil
	}
	for _, definition := range toolmodule.ScheduleDefinitions() {
		if definition.Key == key {
			available, err := h.scheduleToolAvailability.ConversationToolAvailable(ctx, authority, key)
			return available, true, err
		}
	}
	return false, false, nil
}
