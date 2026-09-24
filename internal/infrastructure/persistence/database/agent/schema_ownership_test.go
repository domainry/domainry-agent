package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/schemaownership"
)

func TestSchemaOwnershipMatchesEveryFreshAgentTableAndPrimaryKey(t *testing.T) {
	tables := SchemaOwnership()
	if err := schemaownership.ValidateAll(tables); err != nil {
		t.Fatal(err)
	}
	migrations, err := SchemaMigrations("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	created := map[string]string{}
	historicalWorkerScope := false
	for _, migration := range migrations {
		for _, statement := range migration.Statements {
			const prefix = `CREATE TABLE IF NOT EXISTS "`
			if !strings.HasPrefix(statement, prefix) {
				continue
			}
			name, _, found := strings.Cut(strings.TrimPrefix(statement, prefix), `"`)
			if !found || name == "" {
				t.Fatalf("invalid CREATE TABLE statement: %s", statement)
			}
			if name == "_worker_scopes" {
				if historicalWorkerScope {
					t.Fatal("historical Worker Scope table is created more than once")
				}
				historicalWorkerScope = true
				continue
			}
			if _, duplicate := created[name]; duplicate {
				t.Fatalf("Agent table %s is created more than once", name)
			}
			created[name] = statement
		}
	}
	if !historicalWorkerScope {
		t.Fatal("published Agent v1 lost its historical Worker Scope statement")
	}
	if len(created) != len(tables) {
		t.Fatalf("fresh Agent tables=%d ownership contracts=%d: created=%v owned=%v", len(created), len(tables), sortedKeys(created), OwnedTables())
	}
	for _, table := range tables {
		statement, found := created[table.Name]
		if !found {
			t.Fatalf("Agent table %s has ownership but no canonical DDL", table.Name)
		}
		quoted := make([]string, len(table.PrimaryKey))
		for index, column := range table.PrimaryKey {
			quoted[index] = `"` + column + `"`
		}
		if primaryKey := "PRIMARY KEY (" + strings.Join(quoted, ", ") + ")"; !strings.Contains(statement, primaryKey) {
			t.Fatalf("Agent table %s ownership primary key %v does not match DDL: %s", table.Name, table.PrimaryKey, statement)
		}
	}
}

func TestSchemaOwnershipReturnsIndependentValues(t *testing.T) {
	first, second := SchemaOwnership(), SchemaOwnership()
	if !slices.Equal(OwnedTables(), schemaownership.Names(second)) {
		t.Fatal("Agent owned table names drifted from ownership contracts")
	}
	first[0].PrimaryKey[0] = "changed"
	if second[0].PrimaryKey[0] == "changed" {
		t.Fatal("Agent ownership primary keys share mutable storage")
	}
}

func TestEveryOwnerScopedUserEraseTableParticipatesInSubjectErasure(t *testing.T) {
	indexed := agentSubjectIndexedTables()
	for _, table := range SchemaOwnership() {
		if table.RetentionClass != schemaownership.RetentionUserErase || len(table.PrimaryKey) == 0 || table.PrimaryKey[0] != "owner_key" {
			continue
		}
		if !slices.Contains(agentSubjectOwnerTables, table.Name) {
			if _, found := indexed[table.Name]; !found {
				t.Fatalf("user-erased Agent table %s is absent from subject erasure", table.Name)
			}
		}
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
