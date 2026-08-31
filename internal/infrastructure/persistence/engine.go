package persistence

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/sqlite"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormmigration "github.com/domainry/domainry-orm/migration"
)

var engineRegistry = map[ormdialect.Name]func() ormdriver.Profile{
	ormdialect.SQLite:   sqlite.NewEngine,
	ormdialect.MySQL:    mysql.NewEngine,
	ormdialect.Postgres: postgres.NewEngine,
}

func engineFor(driver string) (ormdriver.Profile, ormdialect.Name, error) {
	parsed, err := ormdialect.Parse(driver)
	if err != nil {
		return nil, "", fmt.Errorf("Agent database driver %q is unsupported: %w", driver, err)
	}
	factory := engineRegistry[parsed.Name()]
	if factory == nil {
		return nil, "", fmt.Errorf("Agent database driver %q is unsupported", driver)
	}
	return factory(), parsed.Name(), nil
}

func Renderer(driver, schema string) (modulehost.Dialect, error) {
	_, name, err := engineFor(driver)
	if err != nil {
		return nil, err
	}
	dialect, err := ormdialect.New(name)
	if err != nil {
		return nil, err
	}
	return dialect.WithSchema(strings.TrimSpace(schema)), nil
}

func NewAgentStore(database modulehost.Database, renderer modulehost.Dialect, driver string) (*agentstore.Store, error) {
	profile, _, err := engineFor(driver)
	if err != nil {
		return nil, err
	}
	return agentstore.NewStore(database, renderer, profile)
}

// EnsureSchema applies Agent-owned migrations to a standalone SaaS database.
// Embedded Module deployments use the host MigrationRegistrar instead.
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
	migrations, err := agentstore.SchemaMigrations(driver, schema)
	if err != nil {
		return err
	}
	return runner.Apply(ctx, migrations)
}
