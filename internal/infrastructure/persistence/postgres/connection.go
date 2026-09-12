package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/base"
	"github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-orm/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"time"
)

func OpenConnection(ctx context.Context, options base.ConnectionOptions) (_ *base.Connection, resultErr error) {
	if options.DSN == "" {
		return nil, fmt.Errorf("PostgreSQL DatabaseDSN is required")
	}
	config, err := pgx.ParseConfig(options.DSN)
	if err != nil {
		return nil, fmt.Errorf("invalid PostgreSQL DSN")
	}
	namespace := options.Schema
	if namespace == "" {
		namespace = "public"
	}
	d, _ := dialect.New(dialect.Postgres)
	config.RuntimeParams["search_path"] = d.Identifier(namespace)
	c := &base.Connection{DB: stdlib.OpenDB(*config), Driver: "postgres", Schema: namespace}
	defer func() {
		if resultErr != nil {
			c.Close()
		}
	}()
	c.DB.SetMaxOpenConns(8)
	c.DB.SetMaxIdleConns(8)
	c.DB.SetConnMaxLifetime(5 * time.Minute)
	if err = c.DB.PingContext(ctx); err != nil {
		return nil, err
	}
	key := migration.NamespacedLockKey("domainry_host", namespace)
	// ORM has no advisory-lock or CREATE SCHEMA builder. Only this adapter owns
	// these dialect operations, with a pinned session and quoted identifiers.
	c.ReleaseStartup, err = base.AcquireStartup(ctx, c.DB, func(ctx context.Context, conn *sql.Conn) (bool, error) {
		var acquired bool
		err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", key).Scan(&acquired)
		return acquired, err
	}, func(ctx context.Context, conn *sql.Conn) error {
		var released bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", key).Scan(&released); err != nil {
			return err
		}
		if !released {
			return fmt.Errorf("host migration lock was lost")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, err = c.DB.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+d.Identifier(namespace)); err != nil {
		return nil, err
	}
	return c, nil
}
