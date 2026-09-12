package persistence

import (
	"context"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/base"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/sqlite"
	"github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-orm/driver"
)

type ConnectionOptions = base.ConnectionOptions
type Connection = base.Connection

var connectionRegistry = map[dialect.Name]func(context.Context, base.ConnectionOptions) (*base.Connection, error){
	dialect.SQLite:   sqlite.OpenConnection,
	dialect.MySQL:    mysql.OpenConnection,
	dialect.Postgres: postgres.OpenConnection,
}

func OpenConnection(ctx context.Context, name string, options ConnectionOptions) (*Connection, driver.Profile, error) {
	if name == "" {
		name = string(dialect.SQLite)
	}
	profile, engine, err := engineFor(name)
	if err != nil {
		return nil, nil, err
	}
	connection, err := connectionRegistry[engine](ctx, options)
	return connection, profile, err
}
