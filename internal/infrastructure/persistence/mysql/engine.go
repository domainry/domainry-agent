package mysql

import (
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormmysql "github.com/domainry/domainry-orm/mysql"
)

func NewEngine() ormdriver.Profile { return ormmysql.NewProfile() }
