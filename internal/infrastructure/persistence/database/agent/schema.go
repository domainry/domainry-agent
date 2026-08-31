package agent

import (
	"fmt"
	ormschema "github.com/domainry/domainry-orm/schema"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormdialect "github.com/domainry/domainry-orm/dialect"
)

const SchemaVersion uint = 1

var schemaDefinitionTables = []string{
	"_agent_skill_definitions",
	"_agent_definitions",
	"_agent_task_definitions",
	"_agent_entrypoint_definitions",
	"_agent_service_principal_definitions",
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
	statements := make([]string, 0, len(schemaDefinitionTables)+7)
	for _, table := range schemaDefinitionTables {
		statement, _, buildErr := definitionTable(renderer, table).Build()
		if buildErr != nil {
			return nil, fmt.Errorf("build Agent definition table %s: %w", table, buildErr)
		}
		statements = append(statements, statement)
	}
	// Migration statement order is part of the host-owned checksum. Keep this
	// source-owned history deterministic; ranging over a map here made the same
	// migration drift between Runtime instances sharing one ledger.
	for _, table := range []struct {
		name    string
		builder *ormschema.TableBuilder
	}{
		{name: "_agent_runtime_states", builder: runtimeStateTable(renderer)},
		{name: "_agent_task_runs", builder: taskRunTable(renderer)},
		{name: "_agent_interactive_runs", builder: interactiveRunTable(renderer)},
		{name: "_agent_worker_scopes", builder: workerScopeTable(renderer)},
	} {
		statement, _, buildErr := table.builder.Build()
		if buildErr != nil {
			return nil, fmt.Errorf("build Agent state table %s: %w", table.name, buildErr)
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
		statement, _, buildErr := ormschema.NewIndex(renderer, index.name, "_agent_task_runs").Columns(index.columns...).Build()
		if buildErr != nil {
			return nil, fmt.Errorf("build Agent task index %s: %w", index.name, buildErr)
		}
		statements = append(statements, statement)
	}
	return []modulehost.SchemaMigration{{Version: SchemaVersion, Name: "agent_foundation", Statements: statements}}, nil
}

func workerScopeTable(renderer modulehost.Dialect) *ormschema.TableBuilder {
	return ormschema.NewTable(renderer, "_agent_worker_scopes").IfNotExists().Columns(
		required("workspace_id", ormschema.TextKey(255)), required("updated_at", ormschema.BigInt()),
	).PrimaryKey("workspace_id")
}

func definitionTable(renderer modulehost.Dialect, name string) *ormschema.TableBuilder {
	return ormschema.NewTable(renderer, name).IfNotExists().Columns(
		required("id", ormschema.TextKey(255)),
		required("resource_key", ormschema.TextKey(255)),
		required("object_key", ormschema.TextKey(255)),
		required("name", ormschema.Text()),
		required("payload_json", ormschema.LongText()),
		required("schema_version", ormschema.TextKey(255)),
		required("schema_hash", ormschema.TextKey(255)),
		required("source_kind", ormschema.TextKey(255)),
		required("source_id", ormschema.TextKey(255)),
		optional("disabled_at", ormschema.TextKey(255)),
		required("created_at", ormschema.TextKey(255)),
		required("updated_at", ormschema.TextKey(255)),
	).PrimaryKey("id").Unique("resource_key")
}

func runtimeStateTable(renderer modulehost.Dialect) *ormschema.TableBuilder {
	return ormschema.NewTable(renderer, "_agent_runtime_states").IfNotExists().Columns(
		required("kind", ormschema.TextKey(255)), required("state_key", ormschema.TextKey(255)),
		required("workspace_id", ormschema.TextKey(255)), required("user_id", ormschema.TextKey(255)),
		required("role_key", ormschema.TextKey(255)), required("payload_json", ormschema.LongText()),
		required("updated_at", ormschema.BigInt()),
	).PrimaryKey("workspace_id", "kind", "state_key")
}

func taskRunTable(renderer modulehost.Dialect) *ormschema.TableBuilder {
	return ormschema.NewTable(renderer, "_agent_task_runs").IfNotExists().Columns(
		required("workspace_id", ormschema.TextKey(255)), required("run_id", ormschema.TextKey(255)),
		required("idempotency_key", ormschema.TextKey(255)), required("task_key", ormschema.TextKey(255)),
		required("process_id", ormschema.TextKey(255)), required("status", ormschema.TextKey(255)),
		required("lease_owner", ormschema.TextKey(255)), required("fencing_token", ormschema.BigInt()),
		required("lease_expires_at", ormschema.BigInt()), required("next_attempt_at", ormschema.BigInt()),
		required("payload_json", ormschema.LongText()), required("created_at", ormschema.BigInt()),
		required("updated_at", ormschema.BigInt()),
	).PrimaryKey("workspace_id", "run_id").Unique("workspace_id", "idempotency_key")
}

func interactiveRunTable(renderer modulehost.Dialect) *ormschema.TableBuilder {
	return ormschema.NewTable(renderer, "_agent_interactive_runs").IfNotExists().Columns(
		required("workspace_id", ormschema.TextKey(255)), required("run_id", ormschema.TextKey(255)),
		required("session_id", ormschema.TextKey(255)), required("user_id", ormschema.TextKey(255)),
		required("role_key", ormschema.TextKey(255)),
		required("status", ormschema.TextKey(255)), required("idempotency_key", ormschema.TextKey(255)),
		required("process_id", ormschema.TextKey(255)), required("task_run_id", ormschema.TextKey(255)),
		required("payload_json", ormschema.LongText()), required("created_at", ormschema.BigInt()),
		required("updated_at", ormschema.BigInt()),
	).PrimaryKey("workspace_id", "run_id").Unique("workspace_id", "idempotency_key")
}

func required(name string, kind ormschema.ColumnType) ormschema.ColumnDefinition {
	return ormschema.Column(name, kind).NotNull()
}

func optional(name string, kind ormschema.ColumnType) ormschema.ColumnDefinition {
	return ormschema.Column(name, kind)
}
