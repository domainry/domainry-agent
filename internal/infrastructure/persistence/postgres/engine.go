package postgres

import (
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormpostgres "github.com/domainry/domainry-orm/postgres"
)

func NewEngine() ormdriver.Profile { return ormpostgres.NewProfile() }
