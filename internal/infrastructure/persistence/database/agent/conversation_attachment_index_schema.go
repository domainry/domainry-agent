package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

// Legacy API forwarding. Knowledge owns the data implementation.
const attachmentKnowledgeSourceTable = knowledgemodule.CompatAttachmentKnowledgeSourceTable

const attachmentIndexJobTable = knowledgemodule.CompatAttachmentIndexJobTable

func attachmentIndexMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	return knowledgemodule.CompatAttachmentIndexMigration(d)
}
