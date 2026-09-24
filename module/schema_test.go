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
		1:  {"agent_foundation", "a6f761aa87b491a7788b0a1e19554ccccfdb2de55e8b261eb2794f58ac0fede4"},
		2:  {"agent_conversations", "f83a11e8320667854be8846ced0ded49b6b39e756b658fde823fa7ada11dfdda"},
		3:  {"agent_conversation_execution", "35344c91e542b76af702336cb678dfd56c7e93e004a93c12093f245a06896030"},
		4:  {"agent_conversation_interactions", "4b72cfdf094240387cecbebe3d9a4b035908e59fac3ba64c9063b5ff49a88290"},
		15: {"agent_tasks", "85a085c498b25da07edbaf7b9c4cc2c0b2e5455464a24977903f31c66b1177fb"},
		19: {"agent_conversation_capacity", "a2b3b7f72aba5eb8da92965c31e0d5bd87e795c526ceedd9c3043a1da24e492f"},
		20: {"agent_peer_collaboration", "654e02dd9e920ea47555bbde08c8e5743f3e4d5d3adc18b88c11ac6a673a3ced"},
		27: {"delegation_execution_subjects", "13c80cbe4bfcc2a100ef1ca7b0d77e3a02ff9754b8bd32442cda8636551ea44a"},
		28: {"delegation_source_releases", "1d5d4b08d77bc39386f9fe999ac30d69e457c426e2d1e1cd22e2fce1cda38ebb"},
		29: {"agent_contract_publications", "4c20800a59d397f10b6183ee1ac7add774e0e95776eca3fd388d77ae6a6d06d6"},
		30: {"agent_conversation_work_budget", "040ba0412480ede97af7c19491bc38508cf5944674592eee4edc7866d6e21a73"},
		34: {"agent_conversation_forks", "c5a8d8542d838db0d4930702c3caddb04b0dc627436fb07e86eb622dfe8b455f"},
		35: {"agent_capability_improvements", "8328d2b6ed5a2040e29a6f00a0cf37b4c3ef5eff8cc6fce09b38c1a057102ee8"},
		36: {"agent_scoped_memories", "94b4328adced03f2f3b1dc436591ed0b27a532775bf33bc83baf879ace9d4e2d"},
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
	if len(tables) != 19 || !slices.Equal(OwnedTables(), schemaownership.Names(tables)) {
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
