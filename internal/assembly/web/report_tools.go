package web

import (
	"fmt"

	agent "github.com/domainry/domainry-agent-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

func configureReportDefinitions(options *Options) error {
	if !options.ReportTools {
		return nil
	}
	definitions := toolmodule.ReportDefinitions()
	for _, d := range definitions {
		for _, existing := range append(append([]agent.ConversationToolDefinition{}, options.ToolDefinitions...), options.Agent.ConversationOptions.ToolDefinitions...) {
			if existing.Key == d.Key {
				return fmt.Errorf("report tool is already defined: %s", d.Key)
			}
		}
	}
	options.ToolDefinitions = append(append([]agent.ConversationToolDefinition{}, options.ToolDefinitions...), definitions...)
	options.Agent.ConversationOptions.ToolDefinitions = append(append([]agent.ConversationToolDefinition{}, options.Agent.ConversationOptions.ToolDefinitions...), definitions...)
	return nil
}

func (h *Host) bindReportTools(options *Options) error {
	adapter := &toolmodule.ReportAdapter{Source: func() toolmodule.ReportSource {
		source, _ := h.businessSource.(toolmodule.ReportSource)
		return source
	}, Authorize: h.AuthorizeConversationTool}
	registry := toolmodule.NewRegistry()
	if err := adapter.Register(registry); err != nil {
		return err
	}
	keys := []string{}
	for _, d := range toolmodule.ReportDefinitions() {
		keys = append(keys, d.Key)
	}
	selected, err := registry.Select(keys)
	if err != nil {
		return err
	}
	h.reportToolAvailability = adapter
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
