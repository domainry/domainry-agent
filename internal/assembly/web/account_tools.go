package web

import (
	"context"
	"fmt"

	agent "github.com/domainry/domainry-agent-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	sdk "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

// Account tool families are opt-in at the product host. The execution engine
// receives ordinary registered tools and never imports provider protocols.
func selectedAccountDefinitions(options Options) []sdk.Definition {
	definitions := []sdk.Definition{}
	if options.CalendarTools {
		definitions = append(definitions, toolmodule.CalendarDefinitions()...)
	}
	if options.MailTools {
		definitions = append(definitions, toolmodule.MailDefinitions()...)
	}
	if options.WebTools {
		definitions = append(definitions, toolmodule.WebDefinitions()...)
	}
	if options.CalendarWriteTools {
		definitions = append(definitions, toolmodule.CalendarWriteDefinitions()...)
	}
	if options.MailWriteTools {
		definitions = append(definitions, toolmodule.MailWriteDefinitions()...)
	}
	return definitions
}
func configureAccountDefinitions(options *Options) error {
	if !options.CalendarTools && !options.MailTools && !options.WebTools && !options.CalendarWriteTools && !options.MailWriteTools {
		return nil
	}
	definitions := selectedAccountDefinitions(*options)
	for _, d := range definitions {
		for _, existing := range options.ToolDefinitions {
			if existing.Key == d.Key {
				return fmt.Errorf("account tool is already defined: %s", d.Key)
			}
		}
		for _, existing := range options.Agent.ConversationOptions.ToolDefinitions {
			if existing.Key == d.Key {
				return fmt.Errorf("account execution tool is already defined: %s", d.Key)
			}
		}
	}
	options.ToolDefinitions = append(append([]agent.ConversationToolDefinition(nil), options.ToolDefinitions...), definitions...)
	options.Agent.ConversationOptions.ToolDefinitions = append(append([]agent.ConversationToolDefinition(nil), options.Agent.ConversationOptions.ToolDefinitions...), definitions...)
	if (options.CalendarWriteTools || options.MailWriteTools) && options.Agent.ConversationOptions.MaxArgumentBytes == 0 {
		options.Agent.ConversationOptions.MaxArgumentBytes = 1 << 20
	}
	if (options.CalendarWriteTools || options.MailWriteTools) && options.Agent.ConversationOptions.ContextBytes == 0 {
		// Keep full approved arguments in the next model step alongside the
		// catalog, discovery evidence and receipts. The generic engine's smaller
		// default remains appropriate for hosts without these bounded writes.
		options.Agent.ConversationOptions.ContextBytes = 2 << 20
	}
	return nil
}

type accountToolAdapter interface {
	sdk.Availability
	Register(*toolmodule.Registry) error
}

func (h *Host) bindAccountTools(options *Options) error {
	var accounts integration.ConnectionAccounts
	var reads integration.ConnectionAccountReads
	var writes integration.ConnectionAccountWrites
	if h.Integration != nil {
		accountBinding, hasAccounts := h.Integration.(integration.ConnectionAccountsBinding)
		readBinding, hasReads := h.Integration.(integration.ConnectionAccountReadsBinding)
		if !hasAccounts || !hasReads || accountBinding.ConnectionAccounts() == nil || readBinding.ConnectionAccountReads() == nil {
			return fmt.Errorf("account tools require Integration current-account read binding")
		}
		accounts, reads = accountBinding.ConnectionAccounts(), readBinding.ConnectionAccountReads()
		if options.CalendarWriteTools || options.MailWriteTools {
			writeBinding, ok := h.Integration.(integration.ConnectionAccountWritesBinding)
			if !ok || writeBinding.ConnectionAccountWrites() == nil {
				return fmt.Errorf("account write tools require Integration write and receipt binding")
			}
			writes = writeBinding.ConnectionAccountWrites()
		}
	}
	registry := toolmodule.NewRegistry()
	h.accountToolAvailability = map[string]sdk.Availability{}
	bind := func(adapter accountToolAdapter, definitions []sdk.Definition) error {
		if err := adapter.Register(registry); err != nil {
			return err
		}
		for _, d := range definitions {
			h.accountToolAvailability[d.Key] = adapter
		}
		return nil
	}
	if options.CalendarTools {
		if err := bind(&toolmodule.CalendarAdapter{Accounts: accounts, Reads: reads, Subject: h.connectionAccountSubject, Authorize: h.AuthorizeConversationTool}, toolmodule.CalendarDefinitions()); err != nil {
			return err
		}
	}
	if options.MailTools {
		if err := bind(&toolmodule.MailAdapter{Accounts: accounts, Reads: reads, Subject: h.connectionAccountSubject, Authorize: h.AuthorizeConversationTool}, toolmodule.MailDefinitions()); err != nil {
			return err
		}
	}
	if options.WebTools {
		if err := bind(&toolmodule.WebAdapter{Accounts: accounts, Reads: reads, Subject: h.connectionAccountSubject, Authorize: h.AuthorizeConversationTool, ConnectionKey: options.WebConnectionKey}, toolmodule.WebDefinitions()); err != nil {
			return err
		}
	}
	h.accountToolDefinitions = selectedAccountDefinitions(*options)
	readOptions := *options
	readOptions.CalendarWriteTools, readOptions.MailWriteTools = false, false
	keys := []string{}
	for _, d := range selectedAccountDefinitions(readOptions) {
		keys = append(keys, d.Key)
	}
	selected, err := registry.Select(keys)
	if err != nil {
		return err
	}
	previous := options.Agent.ConversationOptions.AssembleTools
	writeOptions := Options{CalendarWriteTools: options.CalendarWriteTools, MailWriteTools: options.MailWriteTools}
	options.Agent.ConversationOptions.AssembleTools = func(base agent.ConversationToolHost) (agent.ConversationToolHost, error) {
		// Capture the execution owner's port before another product assembler
		// wraps the base host. The verifier never crosses into model arguments.
		verifier, _ := base.(sdk.ConfirmationVerifier)
		if previous != nil {
			var err error
			base, err = previous(base)
			if err != nil {
				return nil, err
			}
		}
		combined, err := toolmodule.Combine(base, selected, keys)
		if err != nil {
			return nil, err
		}
		return h.assembleAccountWrites(combined, verifier, writeOptions, accounts, reads, writes)
	}
	return nil
}

func (h *Host) assembleAccountWrites(base agent.ConversationToolHost, verifier sdk.ConfirmationVerifier, options Options, accounts integration.ConnectionAccounts, reads integration.ConnectionAccountReads, writes integration.ConnectionAccountWrites) (agent.ConversationToolHost, error) {
	if !options.CalendarWriteTools && !options.MailWriteTools {
		return base, nil
	}
	if verifier == nil {
		return nil, fmt.Errorf("account write tools require the Agent confirmation verification port")
	}
	registry := toolmodule.NewRegistry()
	keys := []string{}
	availability := map[string]sdk.Availability{}
	bind := func(adapter accountToolAdapter, definitions []sdk.Definition) error {
		if err := adapter.Register(registry); err != nil {
			return err
		}
		for _, d := range definitions {
			keys = append(keys, d.Key)
			availability[d.Key] = adapter
		}
		return nil
	}
	if options.CalendarWriteTools {
		if err := bind(&toolmodule.CalendarWriteAdapter{Accounts: accounts, Reads: reads, Writes: writes, Subject: h.connectionAccountSubject, Authorize: h.AuthorizeConversationTool, Confirmation: verifier}, toolmodule.CalendarWriteDefinitions()); err != nil {
			return nil, err
		}
	}
	if options.MailWriteTools {
		if err := bind(&toolmodule.MailWriteAdapter{Accounts: accounts, Writes: writes, Subject: h.connectionAccountSubject, Authorize: h.AuthorizeConversationTool, Confirmation: verifier}, toolmodule.MailWriteDefinitions()); err != nil {
			return nil, err
		}
	}
	selected, err := registry.Select(keys)
	if err != nil {
		return nil, err
	}
	combined, err := toolmodule.Combine(base, selected, keys)
	if err != nil {
		return nil, err
	}
	// Startup composition completes before the worker and tool settings policy
	// are exposed. Each host owns its adapter instances and availability map.
	for key, adapter := range availability {
		h.accountToolAvailability[key] = adapter
	}
	return combined, nil
}

func (h *Host) hasAccountWrites() bool {
	for _, d := range h.accountToolDefinitions {
		if d.Effect == "write" {
			return true
		}
	}
	return false
}

func (h *Host) accountToolAvailable(ctx context.Context, a sdk.Authority, key string) (available, selected bool, err error) {
	adapter, selected := h.accountToolAvailability[key]
	if !selected {
		return false, false, nil
	}
	available, err = adapter.ConversationToolAvailable(ctx, a, key)
	return available, true, err
}
