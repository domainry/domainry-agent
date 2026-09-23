// Package operationkernel adapts the canonical Foundation Operations schema
// to Agent's standalone migration assembly. It owns no table definition.
package operationkernel

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	ormdialect "github.com/domainry/domainry-orm/dialect"
)

func SchemaMigration(renderer modulehost.Dialect) (modulehost.SchemaMigration, error) {
	migrations, err := sharedoperation.SchemaMigrationsForDialect(schemaDialect{Dialect: renderer})
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	if len(migrations) != 1 {
		return modulehost.SchemaMigration{}, fmt.Errorf("Foundation Operations schema returned %d migrations", len(migrations))
	}
	return migrations[0], nil
}

type schemaDialect struct{ modulehost.Dialect }

func (d schemaDialect) Name() ormdialect.Name {
	if named, ok := d.Dialect.(interface{ Name() ormdialect.Name }); ok {
		return named.Name()
	}
	return ""
}

func (d schemaDialect) Insert(table string, columns []string) string {
	quoted := make([]string, len(columns))
	values := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = d.Identifier(column)
		values[index] = d.Placeholder(index + 1)
	}
	return "INSERT INTO " + d.Table(table) + " (" + strings.Join(quoted, ", ") + ") VALUES (" + strings.Join(values, ", ") + ")"
}
