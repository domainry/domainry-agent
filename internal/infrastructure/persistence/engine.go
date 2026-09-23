package persistence

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/migrationhost"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/sqlite"
	shareddefinition "github.com/domainry/domainry-foundation/definition"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
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

func NewAgentStore(database modulehost.Database, renderer modulehost.Dialect, driver, installationID string) (*agentstore.Store, error) {
	profile, _, err := engineFor(driver)
	if err != nil {
		return nil, err
	}
	return agentstore.NewStore(database, renderer, profile, installationID)
}

// EnsureSchema installs shared Foundation kernels and Agent-owned tables into
// a standalone SaaS database through the same owner-aware migration ledger.
// Embedded Module deployments use the Runtime host's registrar instead.
func EnsureSchema(ctx context.Context, database modulehost.Database, driver, schema string) error {
	renderer, err := Renderer(driver, schema)
	if err != nil {
		return err
	}
	profile, _, err := engineFor(driver)
	if err != nil {
		return err
	}
	registrar := &migrationhost.Registrar{DatabaseDriver: driver, Namespace: strings.TrimSpace(schema), Profile: profile, DB: database, Renderer: renderer}
	if err := registrar.Prepare(ctx); err != nil {
		return err
	}
	definitionDialect, ok := renderer.(shareddefinition.Dialect)
	if !ok {
		return fmt.Errorf("Agent database dialect does not support shared Definitions")
	}
	definitionMigrations, err := shareddefinition.SchemaMigrationsForDialect(definitionDialect)
	if err != nil {
		return err
	}
	if err := registrar.ApplyOwnedMigrations(ctx, shareddefinition.MigrationOwner, definitionMigrations); err != nil {
		return fmt.Errorf("apply shared Definition migrations: %w", err)
	}
	agentMigrations, err := agentstore.SchemaMigrations(driver, schema)
	if err != nil {
		return err
	}
	if err := registrar.ApplyOwnedMigrations(ctx, "agent", agentMigrations); err != nil {
		return fmt.Errorf("apply Agent migrations: %w", err)
	}
	return nil
}
