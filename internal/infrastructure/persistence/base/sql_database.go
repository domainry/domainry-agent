// Package base owns the engine-neutral database foundation shared by Agent
// persistence adapters. It contains no Agent repository behavior.
package base

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormdriver "github.com/domainry/domainry-orm/driver"
)

type SQLDatabase struct {
	DB          modulehost.Database
	SQLRenderer modulehost.Dialect
	Engine      ormdriver.Profile
}

func NewSQLDatabase(database modulehost.Database, renderer modulehost.Dialect, engine ormdriver.Profile) *SQLDatabase {
	return &SQLDatabase{DB: database, SQLRenderer: renderer, Engine: engine}
}

func (d *SQLDatabase) Database() modulehost.Database { return d.DB }
func (d *SQLDatabase) Renderer() modulehost.Dialect  { return d.SQLRenderer }
func (d *SQLDatabase) Profile() ormdriver.Profile    { return d.Engine }

func (d *SQLDatabase) IsTransientError(err error) bool {
	if d == nil || d.Engine == nil || err == nil {
		return false
	}
	switch d.Engine.ClassifyError(err) {
	case ormdriver.ErrorSerialization, ormdriver.ErrorDeadlock, ormdriver.ErrorUnavailable, ormdriver.ErrorTimeout:
		return true
	default:
		return false
	}
}
