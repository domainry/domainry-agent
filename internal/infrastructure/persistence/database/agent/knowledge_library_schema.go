package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

// Legacy API forwarding. Knowledge owns the data implementation.
const libraryTable = knowledgemodule.CompatLibraryTable

const libraryMemberTable = knowledgemodule.CompatLibraryMemberTable

func knowledgeLibraryMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	return knowledgemodule.CompatKnowledgeLibraryMigration(d)
}
