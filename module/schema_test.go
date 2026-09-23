package module

import (
	"slices"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/schemaownership"
)

func TestModulePublishesOnlyAgentOwnedSchema(t *testing.T) {
	tables := SchemaOwnership()
	if err := schemaownership.ValidateAll(tables); err != nil {
		t.Fatal(err)
	}
	if len(tables) != 19 || !slices.Equal(OwnedTables(), schemaownership.Names(tables)) {
		t.Fatalf("Agent schema ownership=%d tables=%v", len(tables), OwnedTables())
	}
	for _, table := range tables {
		if table.Owner != "agent" || !strings.HasPrefix(table.Name, "_agent_") {
			t.Fatalf("Agent Module claimed foreign table: %+v", table)
		}
	}
}
