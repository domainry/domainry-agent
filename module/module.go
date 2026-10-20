// Package module exposes Agent's two stable Runtime composition factories.
// Embedded and SaaS implementation details remain under internal/assembly.
package module

import (
	moduleassembly "github.com/domainry/domainry-agent/internal/assembly/module"
	saasassembly "github.com/domainry/domainry-agent/internal/assembly/saas"
)

type ConversationOptions = moduleassembly.ConversationOptions
type ConversationModelConfig = moduleassembly.ConversationModelConfig

type Options = moduleassembly.Options
type Factory = moduleassembly.Factory

func OptionsFromEnvironment() Options { return moduleassembly.OptionsFromEnvironment() }

func NewFactory(options Options) *Factory { return moduleassembly.NewFactory(options) }

type SaaSOptions = saasassembly.Options
type SaaSFactory = saasassembly.Factory

func SaaSOptionsFromEnvironment() SaaSOptions { return saasassembly.OptionsFromEnvironment() }

// NewSaaSFactory exposes the remote SaaS topology through the same stable
// product entry point as the embedded topology.
func NewSaaSFactory(options SaaSOptions) *SaaSFactory {
	return saasassembly.NewFactory(options)
}
