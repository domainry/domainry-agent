package persistence

import (
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
)

func TestEngineRegistrySupportsAgentDatabaseDrivers(t *testing.T) {
	tests := []struct {
		driver string
		want   ormdialect.Name
	}{
		{driver: "sqlite", want: ormdialect.SQLite},
		{driver: "sqlite3", want: ormdialect.SQLite},
		{driver: "mysql", want: ormdialect.MySQL},
		{driver: "postgres", want: ormdialect.Postgres},
		{driver: "postgresql", want: ormdialect.Postgres},
		{driver: "pgx", want: ormdialect.Postgres},
	}
	for _, test := range tests {
		t.Run(test.driver, func(t *testing.T) {
			profile, name, err := engineFor(test.driver)
			if err != nil {
				t.Fatal(err)
			}
			if name != test.want || profile.Name() != test.want {
				t.Fatalf("engine name=%q profile=%q want=%q", name, profile.Name(), test.want)
			}
		})
	}
}

func TestEngineRegistryRejectsUnsupportedDriver(t *testing.T) {
	if _, _, err := engineFor("oracle"); err == nil {
		t.Fatal("unsupported Agent database driver must fail")
	}
}
