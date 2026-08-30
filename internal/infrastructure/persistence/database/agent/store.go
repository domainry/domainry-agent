package agent

import (
	"context"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormmigration "github.com/domainry/domainry-orm/migration"
	ormmysql "github.com/domainry/domainry-orm/mysql"
	ormpostgres "github.com/domainry/domainry-orm/postgres"
	ormsqlite "github.com/domainry/domainry-orm/sqlite"
)

func agentError(class, code string) error {
	return &agentsdk.Error{Class: class, Code: code, Message: code, Retryable: class == "rate_limited"}
}

// Store is the Agent-owned persistence façade shared by Module and SaaS. It
// contains no Runtime repository or service dependency.
type Store struct {
	database modulehost.Database
	renderer modulehost.Dialect
	profile  ormdriver.Profile
}

func NewStore(database modulehost.Database, renderer modulehost.Dialect, driver string) (*Store, error) {
	if database == nil || renderer == nil {
		return nil, fmt.Errorf("Agent database and dialect are required")
	}
	parsed, err := ormdialect.Parse(driver)
	if err != nil {
		return nil, fmt.Errorf("Agent database driver %q is unsupported: %w", driver, err)
	}
	var profile ormdriver.Profile
	switch parsed.Name() {
	case ormdialect.SQLite:
		profile = ormsqlite.NewProfile()
	case ormdialect.MySQL:
		profile = ormmysql.NewProfile()
	case ormdialect.Postgres:
		profile = ormpostgres.NewProfile()
	default:
		return nil, fmt.Errorf("Agent database driver %q is unsupported", driver)
	}
	return &Store{database: database, renderer: renderer, profile: profile}, nil
}

func Renderer(driver, schema string) (modulehost.Dialect, error) {
	parsed, err := ormdialect.Parse(driver)
	if err != nil {
		return nil, err
	}
	dialect, err := ormdialect.New(parsed.Name())
	if err != nil {
		return nil, err
	}
	return dialect.WithSchema(schema), nil
}

// EnsureSchema applies Agent-owned migrations to a standalone SaaS database.
// Embedded Module deployments must use the host MigrationRegistrar instead.
func EnsureSchema(ctx context.Context, database modulehost.Database, driver, schema string) error {
	renderer, err := Renderer(driver, schema)
	if err != nil {
		return err
	}
	runner, err := ormmigration.NewRunner(database, renderer, ormmigration.Options{InsertConflict: func(err error) bool {
		if err == nil {
			return false
		}
		value := strings.ToLower(err.Error())
		return strings.Contains(value, "unique") || strings.Contains(value, "duplicate") || strings.Contains(value, "constraint")
	}})
	if err != nil {
		return err
	}
	migrations, err := SchemaMigrations(driver, schema)
	if err != nil {
		return err
	}
	return runner.Apply(ctx, migrations)
}

func (s *Store) Database() modulehost.Database { return s.database }
func (s *Store) Renderer() modulehost.Dialect  { return s.renderer }
func (s *Store) Profile() ormdriver.Profile    { return s.profile }

func (s *Store) IsTransientError(err error) bool {
	if s == nil || s.profile == nil || err == nil {
		return false
	}
	switch s.profile.ClassifyError(err) {
	case ormdriver.ErrorSerialization, ormdriver.ErrorDeadlock, ormdriver.ErrorUnavailable, ormdriver.ErrorTimeout:
		return true
	default:
		return false
	}
}

func (s *Store) registerWorkerScope(ctx context.Context, executor modulehost.Executor, workspaceID string, updatedAt int64) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return fmt.Errorf("Agent workspace is required")
	}
	insert := ormbuilder.NewInsertBuilder(s.renderer, "agent_worker_scopes").Columns("workspace_id", "updated_at").Values(workspaceID, updatedAt)
	insert, err := s.profile.ApplyUpsert(insert, []string{"workspace_id"}, ormbuilder.AssignExpression("updated_at", ormbuilder.InsertedValue("updated_at")))
	if err != nil {
		return err
	}
	statement, args, err := insert.Build()
	if err != nil {
		return err
	}
	if executor == nil {
		executor = s.database
	}
	_, err = executor.ExecContext(ctx, statement, args...)
	return err
}

func (s *Store) workerScopePage(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 64
	}
	statement, args, err := ormbuilder.NewSelectBuilder(s.renderer, "agent_worker_scopes").Columns("workspace_id").OrderBy(ormbuilder.Descending("updated_at")).Limit(limit).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.database.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			return nil, err
		}
		values = append(values, workspaceID)
	}
	return values, rows.Err()
}
