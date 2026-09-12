package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

// Legacy API forwarding. Knowledge owns the data implementation.
const knowledgeDocumentTable = knowledgemodule.CompatKnowledgeDocumentTable

const knowledgeDocumentJobTable = knowledgemodule.CompatKnowledgeDocumentJobTable

const knowledgeDocumentSourceTable = knowledgemodule.CompatKnowledgeDocumentSourceTable

func knowledgeDocumentMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	return knowledgemodule.CompatKnowledgeDocumentMigration(d)
}
