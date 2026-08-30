package persistence

import (
	"fmt"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	ormdialect "github.com/domainry/domainry-orm/dialect"
)

const SchemaVersion uint = 1

var definitionTables = []string{
	"skill_definitions",
	"agent_definitions",
	"agent_task_definitions",
	"agent_entrypoint_definitions",
	"agent_service_principal_definitions",
}

// SchemaMigrations is the sole source of Agent-owned DDL. Embedded modules
// submit it to the host registrar; standalone SaaS applies the same history to
// its own database and migration ledger.
func SchemaMigrations(driver, schema string) ([]modulehost.SchemaMigration, error) {
	parsed, err := ormdialect.Parse(driver)
	if err != nil {
		return nil, fmt.Errorf("Agent database driver %q is unsupported: %w", driver, err)
	}
	dialect, err := ormdialect.New(parsed.Name())
	if err != nil {
		return nil, fmt.Errorf("create Agent database dialect: %w", err)
	}
	renderer := dialect.WithSchema(schema)
	statements := make([]string, 0, len(definitionTables)+7)
	for _, table := range definitionTables {
		statement, _, buildErr := definitionTable(renderer, table).Build()
		if buildErr != nil {
			return nil, fmt.Errorf("build Agent definition table %s: %w", table, buildErr)
		}
		statements = append(statements, statement)
	}
	for name, builder := range map[string]*ormbuilder.CreateTableBuilder{
		"agent_runtime_state":    runtimeStateTable(renderer),
		"agent_task_runs":        taskRunTable(renderer),
		"agent_interactive_runs": interactiveRunTable(renderer),
		"agent_worker_scopes":    workerScopeTable(renderer),
	} {
		statement, _, buildErr := builder.Build()
		if buildErr != nil {
			return nil, fmt.Errorf("build Agent state table %s: %w", name, buildErr)
		}
		statements = append(statements, statement)
	}
	for _, index := range []struct {
		name    string
		columns []string
	}{
		{name: "idx_agent_owned_task_claim_v1", columns: []string{"workspace_id", "status", "next_attempt_at", "lease_expires_at", "created_at"}},
		{name: "idx_agent_owned_task_process_v1", columns: []string{"workspace_id", "process_id", "status"}},
		{name: "idx_agent_owned_task_key_v1", columns: []string{"workspace_id", "task_key", "status"}},
	} {
		statement, _, buildErr := ormbuilder.NewCreateIndexBuilder(renderer, index.name, "agent_task_runs").Columns(index.columns...).Build()
		if buildErr != nil {
			return nil, fmt.Errorf("build Agent task index %s: %w", index.name, buildErr)
		}
		statements = append(statements, statement)
	}
	return []modulehost.SchemaMigration{{Version: SchemaVersion, Name: "agent_foundation", Statements: statements}}, nil
}

func workerScopeTable(renderer modulehost.Dialect) *ormbuilder.CreateTableBuilder {
	return ormbuilder.NewCreateTableBuilder(renderer, "agent_worker_scopes").WithoutSystemColumns().IfNotExists().Columns(
		required("workspace_id", ormbuilder.TextKeyType(255)), required("updated_at", ormbuilder.BigIntType()),
	).PrimaryKey("workspace_id")
}

func definitionTable(renderer modulehost.Dialect, name string) *ormbuilder.CreateTableBuilder {
	return ormbuilder.NewCreateTableBuilder(renderer, name).WithoutSystemColumns().IfNotExists().Columns(
		required("id", ormbuilder.TextKeyType(255)),
		required("resource_key", ormbuilder.TextKeyType(255)),
		required("object_key", ormbuilder.TextKeyType(255)),
		required("name", ormbuilder.TextType()),
		required("payload_json", ormbuilder.LongTextType()),
		required("schema_version", ormbuilder.TextKeyType(255)),
		required("schema_hash", ormbuilder.TextKeyType(255)),
		required("source_kind", ormbuilder.TextKeyType(255)),
		required("source_id", ormbuilder.TextKeyType(255)),
		optional("disabled_at", ormbuilder.TextKeyType(255)),
		required("created_at", ormbuilder.TextKeyType(255)),
		required("updated_at", ormbuilder.TextKeyType(255)),
	).PrimaryKey("id").Unique("resource_key")
}

func runtimeStateTable(renderer modulehost.Dialect) *ormbuilder.CreateTableBuilder {
	return ormbuilder.NewCreateTableBuilder(renderer, "agent_runtime_state").WithoutSystemColumns().IfNotExists().Columns(
		required("kind", ormbuilder.TextKeyType(255)), required("state_key", ormbuilder.TextKeyType(255)),
		required("workspace_id", ormbuilder.TextKeyType(255)), required("user_id", ormbuilder.TextKeyType(255)),
		required("role_key", ormbuilder.TextKeyType(255)), required("payload_json", ormbuilder.LongTextType()),
		required("updated_at", ormbuilder.BigIntType()),
	).PrimaryKey("workspace_id", "kind", "state_key")
}

func taskRunTable(renderer modulehost.Dialect) *ormbuilder.CreateTableBuilder {
	return ormbuilder.NewCreateTableBuilder(renderer, "agent_task_runs").WithoutSystemColumns().IfNotExists().Columns(
		required("workspace_id", ormbuilder.TextKeyType(255)), required("run_id", ormbuilder.TextKeyType(255)),
		required("idempotency_key", ormbuilder.TextKeyType(255)), required("task_key", ormbuilder.TextKeyType(255)),
		required("process_id", ormbuilder.TextKeyType(255)), required("status", ormbuilder.TextKeyType(255)),
		required("lease_owner", ormbuilder.TextKeyType(255)), required("fencing_token", ormbuilder.BigIntType()),
		required("lease_expires_at", ormbuilder.BigIntType()), required("next_attempt_at", ormbuilder.BigIntType()),
		required("payload_json", ormbuilder.LongTextType()), required("created_at", ormbuilder.BigIntType()),
		required("updated_at", ormbuilder.BigIntType()),
	).PrimaryKey("workspace_id", "run_id").Unique("workspace_id", "idempotency_key")
}

func interactiveRunTable(renderer modulehost.Dialect) *ormbuilder.CreateTableBuilder {
	return ormbuilder.NewCreateTableBuilder(renderer, "agent_interactive_runs").WithoutSystemColumns().IfNotExists().Columns(
		required("workspace_id", ormbuilder.TextKeyType(255)), required("run_id", ormbuilder.TextKeyType(255)),
		required("session_id", ormbuilder.TextKeyType(255)), required("user_id", ormbuilder.TextKeyType(255)),
		required("role_key", ormbuilder.TextKeyType(255)), required("surface", ormbuilder.TextKeyType(255)),
		required("status", ormbuilder.TextKeyType(255)), required("idempotency_key", ormbuilder.TextKeyType(255)),
		required("process_id", ormbuilder.TextKeyType(255)), required("task_run_id", ormbuilder.TextKeyType(255)),
		required("payload_json", ormbuilder.LongTextType()), required("created_at", ormbuilder.BigIntType()),
		required("updated_at", ormbuilder.BigIntType()),
	).PrimaryKey("workspace_id", "run_id").Unique("workspace_id", "idempotency_key")
}

func required(name string, kind ormbuilder.ColumnType) ormbuilder.SchemaColumn {
	return ormbuilder.DefineColumn(name, kind).NotNull()
}

func optional(name string, kind ormbuilder.ColumnType) ormbuilder.SchemaColumn {
	return ormbuilder.DefineColumn(name, kind)
}
