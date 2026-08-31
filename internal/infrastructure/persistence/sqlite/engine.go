package sqlite

import (
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormsqlite "github.com/domainry/domainry-orm/sqlite"
)

func NewEngine() ormdriver.Profile { return ormsqlite.NewProfile() }
