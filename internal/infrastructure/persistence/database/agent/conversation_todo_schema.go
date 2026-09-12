package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	todomodule "github.com/domainry/domainry-todo/module"
)

// Preserve the original ledger checksum while implementation ownership moves.
func conversationTodoMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	return todomodule.LegacyMigration(d)
}
