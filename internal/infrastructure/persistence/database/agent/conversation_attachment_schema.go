package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

// Legacy API forwarding. Knowledge owns the data implementation.
const attachmentTable = knowledgemodule.CompatAttachmentTable

const attachmentCleanupTable = knowledgemodule.CompatAttachmentCleanupTable

func conversationAttachmentMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	return knowledgemodule.CompatConversationAttachmentMigration(d)
}

func conversationAttachmentCleanupMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	return knowledgemodule.CompatConversationAttachmentCleanupMigration(d)
}
