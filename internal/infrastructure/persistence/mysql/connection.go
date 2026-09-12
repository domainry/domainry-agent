package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/base"
	"github.com/domainry/domainry-orm/migration"
	mysqldriver "github.com/go-sql-driver/mysql"
	"time"
)

func OpenConnection(ctx context.Context, options base.ConnectionOptions) (_ *base.Connection, resultErr error) {
	if options.DSN == "" {
		return nil, fmt.Errorf("MySQL DatabaseDSN is required")
	}
	config, err := mysqldriver.ParseDSN(options.DSN)
	if err != nil {
		return nil, fmt.Errorf("invalid MySQL DSN")
	}
	if config.DBName == "" {
		return nil, fmt.Errorf("MySQL DSN must select an existing database")
	}
	if options.Schema != "" && options.Schema != config.DBName {
		return nil, fmt.Errorf("MySQL schema must match the DSN database")
	}
	config.ParseTime = true
	// Domainry uses RowsAffected to distinguish a matched row from absence.
	// Match PostgreSQL/SQLite semantics even when an UPDATE keeps the same values.
	config.ClientFoundRows = true
	// Binary collation preserves case-sensitive IDs and idempotency keys, as in
	// SQLite/PostgreSQL. The database must also use utf8mb4_bin for indexed keys.
	if err = config.Apply(mysqldriver.Charset("utf8mb4", "utf8mb4_bin")); err != nil {
		return nil, err
	}
	if config.Params == nil {
		config.Params = map[string]string{}
	}
	config.Params["default_storage_engine"] = "'InnoDB'"
	connector, err := mysqldriver.NewConnector(config)
	if err != nil {
		return nil, fmt.Errorf("invalid MySQL connector configuration")
	}
	// Domainry's MySQL modules use the DSN database and an empty schema name.
	c := &base.Connection{DB: sql.OpenDB(connector), Driver: "mysql"}
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
	var collation string
	if err = c.DB.QueryRowContext(ctx, "SELECT DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", config.DBName).Scan(&collation); err != nil {
		return nil, err
	}
	if collation != "utf8mb4_bin" {
		return nil, fmt.Errorf("MySQL database must use utf8mb4_bin collation; found %s", collation)
	}
	key := migration.NamespacedLockKey("domainry_host", config.DBName)
	// ORM does not model session advisory locks or database catalog inspection.
	// These engine-specific operations stay on a pinned physical connection.
	c.ReleaseStartup, err = base.AcquireStartup(ctx, c.DB, func(ctx context.Context, conn *sql.Conn) (bool, error) {
		var acquired sql.NullInt64
		err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", key).Scan(&acquired)
		return acquired.Valid && acquired.Int64 == 1, err
	}, func(ctx context.Context, conn *sql.Conn) error {
		var released sql.NullInt64
		if err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", key).Scan(&released); err != nil {
			return err
		}
		if !released.Valid || released.Int64 != 1 {
			return fmt.Errorf("host migration lock was lost")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}
