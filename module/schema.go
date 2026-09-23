package module

import (
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-foundation/schemaownership"
)

// SchemaOwnership returns only Agent-owned physical tables. Shared Foundation,
// Knowledge and Todo tables remain registered by their source-owning modules.
func SchemaOwnership() []schemaownership.Table { return agentstore.SchemaOwnership() }

func OwnedTables() []string { return schemaownership.Names(SchemaOwnership()) }
