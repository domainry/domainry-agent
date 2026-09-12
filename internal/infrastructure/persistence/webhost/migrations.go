// Package webhost owns the host pool and single migration ledger for the
// standalone browser host. Embedded modules retain ownership of their schema.
package webhost

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-orm/driver"
	"github.com/domainry/domainry-orm/migration"
	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-orm/schema"
)

// Registrar is used during sequential startup under the host's exclusive
// startup database lock. Identity callbacks may submit nested Metadata/Audit
// migrations; no transaction or mutex is held across those callbacks.
type Registrar struct {
	DatabaseDriver, Namespace string
	Profile                   driver.Profile
	DB                        *sql.DB
	Renderer                  modulehost.Dialect
}

func (r *Registrar) Driver() string {
	if r.DatabaseDriver == "" {
		return "sqlite"
	}
	return r.DatabaseDriver
}
func (r *Registrar) Schema() string { return r.Namespace }

func (r *Registrar) Prepare(ctx context.Context) error {
	statement, args, err := schema.NewTable(r.Renderer, "_schema_migrations").IfNotExists().Columns(
		schema.Column("owner", schema.TextKey(191)).NotNull(),
		schema.Column("version", schema.BigInt()).NotNull(),
		schema.Column("name", schema.TextKey(191)).NotNull(),
		schema.Column("checksum", schema.TextKey(64)).NotNull(),
		schema.Column("dirty", schema.Boolean()).NotNull(),
		schema.Column("applied_at", schema.TextKey(40)).NotNull(),
	).PrimaryKey("owner", "version").Build()
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, statement, args...)
	return err
}

func (r *Registrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []modulehost.SchemaMigration) error {
	for _, m := range migrations {
		if err := r.ApplyOwnedMigration(ctx, owner, m.Version, m.Name, migration.Checksum(m), func(ctx context.Context) error {
			if r.Profile != nil && !r.Profile.Capabilities().TransactionalDDL {
				// Engines with implicit DDL commits retain the dirty ledger on failure.
				for _, statement := range m.Statements {
					if _, err := r.DB.ExecContext(ctx, statement); err != nil {
						return err
					}
				}
				return nil
			}
			tx, err := r.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			// Source modules have already rendered these statements with ORM.
			for _, statement := range m.Statements {
				if _, err := tx.ExecContext(ctx, statement); err != nil {
					return err
				}
			}
			return tx.Commit()
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registrar) ApplyOwnedMigration(ctx context.Context, owner string, version uint, name, checksum string, apply func(context.Context) error) error {
	if strings.TrimSpace(owner) == "" || version == 0 || name == "" || checksum == "" || apply == nil {
		return fmt.Errorf("invalid host migration")
	}
	predicate := query.And(query.Equal("owner", owner), query.Equal("version", version))
	statement, args, err := query.NewSelectBuilder(r.Renderer, "_schema_migrations").Columns("name", "checksum", "dirty").Where(predicate).Build()
	if err != nil {
		return err
	}
	var previousName, previousChecksum string
	var dirty bool
	err = r.DB.QueryRowContext(ctx, statement, args...).Scan(&previousName, &previousChecksum, &dirty)
	if err == nil {
		if dirty {
			return fmt.Errorf("migration %s/%d is dirty; inspect the failed migration before restarting", owner, version)
		}
		if name != previousName || checksum != previousChecksum {
			return fmt.Errorf("migration %s/%d checksum drift", owner, version)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	statement, args, err = query.NewInsertBuilder(r.Renderer, "_schema_migrations").Columns("owner", "version", "name", "checksum", "dirty", "applied_at").Values(owner, version, name, checksum, true, "").Build()
	if err != nil {
		return err
	}
	if _, err = r.DB.ExecContext(ctx, statement, args...); err != nil {
		return err
	}
	if err = apply(ctx); err != nil {
		return fmt.Errorf("apply %s/%d: %w", owner, version, err)
	}
	statement, args, err = query.NewUpdateBuilder(r.Renderer, "_schema_migrations").Set("dirty", false).Set("applied_at", time.Now().UTC().Format(time.RFC3339Nano)).Where(predicate).Build()
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, statement, args...)
	return err
}
