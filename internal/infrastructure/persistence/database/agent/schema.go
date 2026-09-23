package agent

import (
	"fmt"
	ormschema "github.com/domainry/domainry-orm/schema"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormdialect "github.com/domainry/domainry-orm/dialect"
)

const SchemaVersion uint = 1

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
	statements := make([]string, 0, 12)
	// Migration statement order is part of the host-owned checksum. Keep this
	// source-owned history deterministic; ranging over a map here made the same
	// migration drift between Runtime instances sharing one ledger.
	for _, table := range []struct {
		name    string
		builder *ormschema.TableBuilder
	}{
		{name: "_agent_runtime_states", builder: runtimeStateTable(renderer)},
		{name: agentRunTable, builder: agentRunTableBuilder(renderer)},
		{name: "_worker_scopes", builder: workerScopeTable(renderer)},
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
		{name: "idx_agent_run_task_claim_v1", columns: []string{"run_kind", "workspace_id", "status", "next_attempt_at", "lease_expires_at", "created_at"}},
		{name: "idx_agent_run_task_process_v1", columns: []string{"run_kind", "workspace_id", "process_id", "status"}},
		{name: "idx_agent_run_task_key_v1", columns: []string{"run_kind", "workspace_id", "task_key", "status"}},
		{name: "idx_agent_run_interactive_list_v1", columns: []string{"run_kind", "workspace_id", "user_id", "role_key", "status", "created_at"}},
	} {
		statement, _, buildErr := ormschema.NewIndex(renderer, index.name, agentRunTable).Columns(index.columns...).Build()
		if buildErr != nil {
			return nil, fmt.Errorf("build Agent run index %s: %w", index.name, buildErr)
		}
		statements = append(statements, statement)
	}
	conversation, err := conversationMigration(renderer)
	if err != nil {
		return nil, err
	}
	execution, err := conversationExecutionMigration(renderer)
	if err != nil {
		return nil, err
	}
	interactions, err := conversationInteractionMigration(renderer)
	if err != nil {
		return nil, err
	}
	todos, err := conversationTodoMigration(renderer)
	if err != nil {
		return nil, err
	}
	artifacts, err := conversationArtifactMigration(renderer)
	if err != nil {
		return nil, err
	}
	libraries, err := knowledgeLibraryMigration(renderer)
	if err != nil {
		return nil, err
	}
	documents, err := knowledgeDocumentMigration(renderer)
	if err != nil {
		return nil, err
	}
	datasources, err := knowledgeDatasourceMigration(renderer)
	if err != nil {
		return nil, err
	}
	parses, err := documentParseMigration(renderer)
	if err != nil {
		return nil, err
	}
	retired, err := retireDocumentParsingMigration(renderer)
	if err != nil {
		return nil, err
	}
	attachmentIndex, err := attachmentIndexMigration(renderer)
	if err != nil {
		return nil, err
	}
	tasks, err := conversationTaskMigration(renderer)
	if err != nil {
		return nil, err
	}
	subjects, err := subjectLifecycleMigration(renderer)
	if err != nil {
		return nil, err
	}
	capacity, err := conversationCapacityMigration(renderer)
	if err != nil {
		return nil, err
	}
	collaboration, err := conversationCollaborationMigration(renderer)
	if err != nil {
		return nil, err
	}
	agreements, err := conversationAgreementMigration(renderer)
	if err != nil {
		return nil, err
	}
	assignments, err := conversationAssignmentMigration(renderer)
	if err != nil {
		return nil, err
	}
	deliveries, err := conversationDeliveryRecordMigration(renderer)
	if err != nil {
		return nil, err
	}
	disagreements, err := conversationDisagreementMigration(renderer)
	if err != nil {
		return nil, err
	}
	agentSharing, err := conversationAgentSharingMigration(renderer)
	if err != nil {
		return nil, err
	}
	participants, err := conversationParticipantsMigration(renderer)
	if err != nil {
		return nil, err
	}
	subjectsBinding, err := conversationDelegationSubjectsMigration(renderer)
	if err != nil {
		return nil, err
	}
	sourceReleases, err := conversationSourceReleasesMigration(renderer)
	if err != nil {
		return nil, err
	}
	contractPublications, err := conversationContractPublicationMigration(renderer)
	if err != nil {
		return nil, err
	}
	workBudgets, err := conversationWorkBudgetMigration(renderer)
	if err != nil {
		return nil, err
	}
	forks, err := conversationForkMigration(renderer)
	if err != nil {
		return nil, err
	}
	improvements, err := conversationImprovementMigration(renderer)
	if err != nil {
		return nil, err
	}
	memories, err := conversationMemoryChangeMigration(renderer)
	if err != nil {
		return nil, err
	}
	return []modulehost.SchemaMigration{{Version: SchemaVersion, Name: "agent_foundation", Statements: statements}, conversation, execution, interactions, todos, artifacts, libraries, documents, datasources, parses, retired, attachmentIndex, tasks, subjects, capacity, collaboration, agreements, assignments, deliveries, disagreements, agentSharing, participants, subjectsBinding, sourceReleases, contractPublications, workBudgets, forks, improvements, memories}, nil
}

func workerScopeTable(renderer modulehost.Dialect) *ormschema.TableBuilder {
	return ormschema.NewTable(renderer, "_worker_scopes").IfNotExists().Columns(
		required("id", ormschema.TextKey(255)),
		required("owner", ormschema.TextKey(191)),
		required("scope_key", ormschema.TextKey(191)),
		ormschema.Column("cursor", ormschema.TextKey(255)).NotNull().DefaultValue(""),
		ormschema.Column("checkpoint", ormschema.BigInt()).NotNull().DefaultValue(0),
		ormschema.Column("capacity", ormschema.BigInt()).NotNull().DefaultValue(0),
		ormschema.Column("lease_owner", ormschema.TextKey(255)).NotNull().DefaultValue(""),
		ormschema.Column("lease_expires_at", ormschema.TextKey(255)).NotNull().DefaultValue(""),
		ormschema.Column("fencing_token", ormschema.BigInt()).NotNull().DefaultValue(0),
		ormschema.Column("last_started_at", ormschema.TextKey(255)).NotNull().DefaultValue(""),
		ormschema.Column("last_completed_at", ormschema.TextKey(255)).NotNull().DefaultValue(""),
		ormschema.Column("last_error", ormschema.LongText()).NotNull().DefaultValue(""),
		ormschema.Column("updated_at", ormschema.TextKey(255)).NotNull().DefaultValue(""),
	).PrimaryKey("id").Unique("owner", "scope_key")
}

func runtimeStateTable(renderer modulehost.Dialect) *ormschema.TableBuilder {
	return ormschema.NewTable(renderer, "_agent_runtime_states").IfNotExists().Columns(
		required("kind", ormschema.TextKey(255)), required("state_key", ormschema.TextKey(255)),
		required("workspace_id", ormschema.TextKey(255)), required("user_id", ormschema.TextKey(255)),
		required("role_key", ormschema.TextKey(255)), required("payload_json", ormschema.LongText()),
		required("updated_at", ormschema.BigInt()),
	).PrimaryKey("workspace_id", "kind", "state_key")
}

const (
	agentRunTable            = "_agent_runs"
	agentRunKindTask         = "task"
	agentRunKindInteractive  = "interactive"
	agentRunKindConversation = "conversation"
)

// agentRunTableBuilder stores every mutable Agent run in one typed physical
// table. run_kind and scope_key keep each run family's identity, idempotency,
// claim and lifecycle semantics isolated even though storage is shared.
func agentRunTableBuilder(renderer modulehost.Dialect) *ormschema.TableBuilder {
	return ormschema.NewTable(renderer, agentRunTable).IfNotExists().Columns(
		required("run_kind", ormschema.TextKey(32)),
		required("scope_key", ormschema.TextKey(191)),
		required("workspace_id", ormschema.TextKey(255)),
		required("run_id", ormschema.TextKey(255)),
		required("idempotency_key", ormschema.TextKey(255)),
		optional("owner_key", ormschema.TextKey(64)),
		optional("conversation_id", ormschema.TextKey(96)),
		optional("runtime_id", ormschema.TextKey(255)),
		optional("authority_json", ormschema.LongText()),
		optional("request_hash", ormschema.LongText()),
		optional("task_key", ormschema.TextKey(255)),
		optional("process_id", ormschema.TextKey(255)),
		optional("session_id", ormschema.TextKey(255)),
		optional("user_id", ormschema.TextKey(255)),
		optional("role_key", ormschema.TextKey(255)),
		optional("task_run_id", ormschema.TextKey(255)),
		required("status", ormschema.TextKey(255)),
		required("lease_owner", ormschema.TextKey(255)).DefaultValue(""),
		required("fencing_token", ormschema.BigInt()).DefaultValue(0),
		required("lease_expires_at", ormschema.BigInt()).DefaultValue(0),
		required("next_attempt_at", ormschema.BigInt()).DefaultValue(0),
		required("event_seq", ormschema.BigInt()).DefaultValue(0),
		required("payload_json", ormschema.LongText()),
		required("created_at", ormschema.BigInt()),
		required("updated_at", ormschema.BigInt()),
	).PrimaryKey("run_kind", "scope_key", "run_id").Unique("run_kind", "scope_key", "idempotency_key")
}

func required(name string, kind ormschema.ColumnType) ormschema.ColumnDefinition {
	return ormschema.Column(name, kind).NotNull()
}

func optional(name string, kind ormschema.ColumnType) ormschema.ColumnDefinition {
	return ormschema.Column(name, kind)
}
