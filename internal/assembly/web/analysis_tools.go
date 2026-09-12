package web

import (
	"fmt"

	agent "github.com/domainry/domainry-agent-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

func configureAnalysisDefinitions(options *Options) error {
	if !options.AnalysisTools {
		return nil
	}
	definitions := toolmodule.AnalysisDefinitions()
	for _, definition := range definitions {
		for _, existing := range append(append([]agent.ConversationToolDefinition{}, options.ToolDefinitions...), options.Agent.ConversationOptions.ToolDefinitions...) {
			if existing.Key == definition.Key {
				return fmt.Errorf("analysis tool is already defined: %s", definition.Key)
			}
		}
	}
	options.ToolDefinitions = append(append([]agent.ConversationToolDefinition{}, options.ToolDefinitions...), definitions...)
	options.Agent.ConversationOptions.ToolDefinitions = append(append([]agent.ConversationToolDefinition{}, options.Agent.ConversationOptions.ToolDefinitions...), definitions...)
	return nil
}
func (h *Host) bindAnalysisTools(options *Options) error {
	adapter := &toolmodule.AnalysisAdapter{Source: func() toolmodule.AnalysisSource {
		source, _ := h.businessSource.(toolmodule.AnalysisSource)
		return source
	}, Authorize: h.AuthorizeConversationTool}
	registry := toolmodule.NewRegistry()
	if err := adapter.Register(registry); err != nil {
		return err
	}
	keys := []string{}
	for _, definition := range toolmodule.AnalysisDefinitions() {
		keys = append(keys, definition.Key)
	}
	selected, err := registry.Select(keys)
	if err != nil {
		return err
	}
	h.analysisToolAvailability = adapter
	previous := options.Agent.ConversationOptions.AssembleTools
	options.Agent.ConversationOptions.AssembleTools = func(base agent.ConversationToolHost) (agent.ConversationToolHost, error) {
		if previous != nil {
			var err error
			base, err = previous(base)
			if err != nil {
				return nil, err
			}
		}
		return toolmodule.Combine(base, selected, keys)
	}
	return nil
}
