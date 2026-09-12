package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/base"
	ormsqlite "github.com/domainry/domainry-orm/sqlite"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
)

func OpenConnection(ctx context.Context, options base.ConnectionOptions) (_ *base.Connection, resultErr error) {
	if options.Path == "" || options.DSN != "" || options.Schema != "" {
		return nil, fmt.Errorf("SQLite requires DatabasePath and no DSN/schema")
	}
	path, err := filepath.Abs(options.Path)
	if err != nil {
		return nil, err
	}
	release, err := base.LockFile(path + ".lock")
	if err != nil {
		return nil, err
	}
	c := &base.Connection{Driver: "sqlite", FilePath: path}
	c.SetLocalRelease(release)
	defer func() {
		if resultErr != nil {
			c.Close()
		}
	}()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	file.Close()
	config := ormsqlite.DefaultOwnedConnectionConfig(path)
	config.MaxOpenConnections, config.MaxIdleConnections = 1, 1
	dsn, err := config.DSN()
	if err != nil {
		return nil, err
	}
	c.DB, err = sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err = ormsqlite.InitializeOwned(ctx, c.DB, config); err != nil {
		return nil, err
	}
	return c, nil
}
