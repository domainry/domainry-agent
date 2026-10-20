// Package databasetest runs the same acceptance contract on SQLite and opt-in
// real MySQL/PostgreSQL servers. Every run creates and removes its own namespace.
package databasetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"github.com/domainry/domainry-orm/dialect"
	mysqldriver "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type Options struct{ Driver, DSN, Schema, Path, StoragePath string }

func ForEach(t *testing.T, verify func(*testing.T, Options)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { verify(t, Options{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "host.db")}) })
	t.Run("mysql", func(t *testing.T) {
		dsn := os.Getenv("DOMAINRY_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("set DOMAINRY_TEST_MYSQL_DSN to enable real MySQL acceptance")
		}
		config, err := mysqldriver.ParseDSN(dsn)
		if err != nil {
			t.Fatal("invalid test MySQL DSN")
		}
		admin := open(t, "mysql", dsn)
		name := namespace(t)
		d, _ := dialect.New(dialect.MySQL)
		if _, err = admin.ExecContext(t.Context(), "CREATE DATABASE "+d.Identifier(name)+" CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
			t.Fatal(err)
		}
		cleanup(t, admin, "DROP DATABASE "+d.Identifier(name))
		config.DBName = name
		verify(t, Options{Driver: "mysql", DSN: config.FormatDSN(), Schema: name, StoragePath: filepath.Join(t.TempDir(), "files")})
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("DOMAINRY_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set DOMAINRY_TEST_POSTGRES_DSN to enable real PostgreSQL acceptance")
		}
		admin := open(t, "pgx", dsn)
		name := namespace(t)
		d, _ := dialect.New(dialect.Postgres)
		if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+d.Identifier(name)); err != nil {
			t.Fatal(err)
		}
		cleanup(t, admin, "DROP SCHEMA "+d.Identifier(name)+" CASCADE")
		verify(t, Options{Driver: "postgres", DSN: dsn, Schema: name, StoragePath: filepath.Join(t.TempDir(), "files")})
	})
}
func namespace(t *testing.T) string {
	t.Helper()
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return "domainry_test_" + hex.EncodeToString(id[:])
}
func open(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}
func cleanup(t *testing.T, db *sql.DB, statement string) {
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Errorf("clean test namespace: %v", err)
		}
	})
}
