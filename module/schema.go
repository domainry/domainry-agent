package module

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-foundation/schemaownership"
)

// SchemaOwnership returns only Agent-owned physical tables. Shared Foundation,
// Knowledge and Todo tables remain registered by their source-owning modules.
func SchemaOwnership() []schemaownership.Table { return agentstore.SchemaOwnership() }

func OwnedTables() []string { return schemaownership.Names(SchemaOwnership()) }

// SchemaMigrations exposes Agent's canonical source-owned DDL for composition
// verification. Module startup calls the same function before constructing its
// private Store; callers do not receive or inject a Store object.
func SchemaMigrations(driver, schema string) ([]modulehost.SchemaMigration, error) {
	return agentstore.SchemaMigrations(driver, schema)
}
