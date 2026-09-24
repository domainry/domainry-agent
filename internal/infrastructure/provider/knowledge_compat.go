package provider

import knowledgeprovider "github.com/domainry/domainry-knowledge-sdk/provider"

// Provider configuration is an SDK contract. Concrete source construction is
// selected by the outer composition root and injected into Agent's module.
type KnowledgeCitationMapping = knowledgeprovider.CitationMapping
type KnowledgeResponseMapping = knowledgeprovider.ResponseMapping
type KnowledgeConfig = knowledgeprovider.Config

var KnowledgeConfigFromEnvironment = knowledgeprovider.ConfigFromEnvironment
