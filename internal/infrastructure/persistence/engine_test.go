package persistence

import (
	"database/sql"
	"testing"

	sharedoperation "github.com/domainry/domainry-foundation/operation"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	_ "modernc.org/sqlite"
)

func TestEngineRegistrySupportsAgentDatabaseDrivers(t *testing.T) {
	tests := []struct {
		driver string
		want   ormdialect.Name
	}{
		{driver: "sqlite", want: ormdialect.SQLite},
		{driver: "sqlite3", want: ormdialect.SQLite},
		{driver: "mysql", want: ormdialect.MySQL},
		{driver: "postgres", want: ormdialect.Postgres},
		{driver: "postgresql", want: ormdialect.Postgres},
		{driver: "pgx", want: ormdialect.Postgres},
	}
	for _, test := range tests {
		t.Run(test.driver, func(t *testing.T) {
			profile, name, err := engineFor(test.driver)
			if err != nil {
				t.Fatal(err)
			}
			if name != test.want || profile.Name() != test.want {
				t.Fatalf("engine name=%q profile=%q want=%q", name, profile.Name(), test.want)
			}
		})
	}
}

func TestEnsureSchemaUsesOneOwnerAwareLedgerForFoundationAndAgent(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := EnsureSchema(t.Context(), database, "sqlite", ""); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"_definitions", "_definition_versions", sharedoperation.TableName, sharedworkerscope.TableName, "_subject_requests", "_subject_steps", "_agent_user_todos", "_agent_knowledge_libraries", "_agent_knowledge_library_members", "_agent_knowledge_documents", "_agent_knowledge_sources", "_agent_knowledge_document_jobs", "_agent_runtime_states"} {
		var count int
		if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
	for _, retired := range []string{"_agent_owner_operation_receipts", "_agent_collaboration_mutations", "_agent_todo_mutations", "_agent_artifact_mutations", "_knowledge_owner_operation_receipts", "_agent_knowledge_document_sources", "_agent_knowledge_datasource_bindings", "_agent_attachment_knowledge_sources"} {
		var count int
		if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, retired).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired Knowledge table %s count=%d err=%v", retired, count, err)
		}
	}
	for _, table := range []string{"_agent_skill_definitions", "_agent_definitions", "_agent_task_definitions", "_agent_entrypoint_definitions", "_agent_service_principal_definitions"} {
		var count int
		if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("private table %s count=%d err=%v", table, count, err)
		}
	}
	var owners int
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(DISTINCT owner) FROM _schema_migrations WHERE owner IN ('shared/definitions','shared/operations','shared/worker-scopes','shared/subject-lifecycle','todo','knowledge','agent')`).Scan(&owners); err != nil || owners != 7 {
		t.Fatalf("migration owners=%d err=%v", owners, err)
	}
}

func TestEngineRegistryRejectsUnsupportedDriver(t *testing.T) {
	if _, _, err := engineFor("oracle"); err == nil {
		t.Fatal("unsupported Agent database driver must fail")
	}
}
