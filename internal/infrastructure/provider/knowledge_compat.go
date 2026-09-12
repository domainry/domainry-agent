package provider

import knowledgemodule "github.com/domainry/domainry-knowledge/module"

// Compatibility names; implementation is owned by domainry-knowledge.
type KnowledgeCitationMapping = knowledgemodule.KnowledgeCitationMapping
type KnowledgeResponseMapping = knowledgemodule.KnowledgeResponseMapping
type AttachmentKnowledge = knowledgemodule.AttachmentKnowledge

var NewAttachmentKnowledge = knowledgemodule.NewAttachmentKnowledge

type KnowledgeConfig = knowledgemodule.KnowledgeConfig
type Knowledge = knowledgemodule.Knowledge

var KnowledgeConfigFromEnvironment = knowledgemodule.KnowledgeConfigFromEnvironment
var NewKnowledge = knowledgemodule.NewKnowledge
