package module

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/schemaownership"
	ormmigration "github.com/domainry/domainry-orm/migration"
)

func TestPublishedMySQLMigrationChecksumsAreStable(t *testing.T) {
	expected := map[uint]struct {
		name     string
		checksum string
	}{
		1:  {"agent_foundation", "7f255d0945e22a86e75f7d843aeb594a6d0d9b272a2937da3056075a2119e72f"},
		2:  {"agent_conversations", "80b46ed628e1c3d717a5c1dcc097d33b0eca8d7ac9e9eab7f3630421833b33e4"},
		3:  {"agent_conversation_execution", "e7f1643c49043577fbe0cd868d906fec9b37ce8903735d8ea91d094c144b9616"},
		4:  {"agent_conversation_interactions", "73e3972bd8208cc5b0163bfb9b9d8e7298e54cbf87f69faec7965b91f6e9a65f"},
		6:  {"agent_conversation_artifacts", "bafb59a10077ce1e992d1d0a3d5dd69ad761bdb08d17e15766a1f30e1cba8d4b"},
		15: {"agent_tasks", "7354b6bc51069285d48a714fd929e4fd610c402c09bcead51c07a38261acd8cd"},
		19: {"agent_conversation_capacity", "a2b3b7f72aba5eb8da92965c31e0d5bd87e795c526ceedd9c3043a1da24e492f"},
		20: {"agent_peer_collaboration", "815626f43477ee32f65d51ef4e416623fa8687f7cb2023d3b4a4b9afa607de28"},
		27: {"delegation_execution_subjects", "673d55f4d86fb359f2656ea0b0299e69a7c435b64138a68f3df56fe0ee130a42"},
		28: {"delegation_source_releases", "3eeeaa199442d4d5a70a746cf41c09ba94cf68e82cf3f8e7cf73bf60df0a4ab1"},
		29: {"agent_contract_publications", "9694df4aba9b7e1f238c70ae96e2311ce900808b8151f8468f400e39be1dd651"},
		30: {"agent_conversation_work_budget", "cd6e2fe17d3533595c496b9eb6a4cb3d377a2b7aa1277baebf4dedd42c1dd68a"},
		34: {"agent_conversation_forks", "f8846555ef552729c7bc205bb82945a952d24d77a3b28e32e5fca6ee03d1b92f"},
		35: {"agent_capability_improvements", "aa59492e161822849191eb960c800129d55f85a673ee3bd479c25f5e93762e71"},
		36: {"agent_scoped_memories", "df24af04cafa726686981cc62aa49a88052a7d69058ee25a689a356bf7afc275"},
	}
	migrations, err := SchemaMigrations("mysql", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != len(expected) {
		t.Errorf("published migrations=%d want=%d", len(migrations), len(expected))
	}
	for _, migration := range migrations {
		want, found := expected[migration.Version]
		if !found {
			t.Errorf("migration v%d %q has no published checksum pin", migration.Version, migration.Name)
			continue
		}
		if migration.Name != want.name {
			t.Errorf("migration v%d name=%q want=%q", migration.Version, migration.Name, want.name)
		}
		if got := runtimeOwnedMigrationChecksum(migration); got != want.checksum {
			t.Errorf("migration v%d %s checksum=%s want=%s", migration.Version, migration.Name, got, want.checksum)
		}
	}
}

func runtimeOwnedMigrationChecksum(migration ormmigration.Migration) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%d\x00%s\x00", migration.Version, strings.TrimSpace(migration.Name))
	for _, statement := range migration.Statements {
		_, _ = fmt.Fprintf(hash, "%s\x00", statement)
	}
	if migration.Baseline != nil {
		for _, table := range migration.Baseline.Tables {
			_, _ = fmt.Fprintf(hash, "table\x00%s\x00", table.Name)
			for _, column := range table.Columns {
				_, _ = fmt.Fprintf(hash, "column\x00%s\x00%s\x00%t\x00%t\x00", column.Name, column.Type, column.Nullable, column.PrimaryKey)
			}
			for _, index := range table.Indexes {
				_, _ = fmt.Fprintf(hash, "index\x00%s\x00%t\x00%s\x00", index.Name, index.Unique, strings.Join(index.Columns, ","))
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func TestModulePublishesOnlyAgentOwnedSchema(t *testing.T) {
	tables := SchemaOwnership()
	if err := schemaownership.ValidateAll(tables); err != nil {
		t.Fatal(err)
	}
	if len(tables) != 26 || !slices.Equal(OwnedTables(), schemaownership.Names(tables)) {
		t.Fatalf("Agent schema ownership=%d tables=%v", len(tables), OwnedTables())
	}
	for _, table := range tables {
		if table.Owner != "agent" || !strings.HasPrefix(table.Name, "_agent_") {
			t.Fatalf("Agent Module claimed foreign table: %+v", table)
		}
	}
}

func TestModulePublishesCanonicalAgentMigrations(t *testing.T) {
	migrations, err := SchemaMigrations("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	statements := ""
	for _, migration := range migrations {
		statements += strings.Join(migration.Statements, "\n")
	}
	for _, table := range OwnedTables() {
		if !strings.Contains(statements, `CREATE TABLE IF NOT EXISTS "`+table+`"`) {
			t.Fatalf("canonical Agent migrations omit %s", table)
		}
	}
}
