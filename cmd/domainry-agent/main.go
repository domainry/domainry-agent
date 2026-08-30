package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/provider"
	"github.com/domainry/domainry-agent/server"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_SAAS_DB_DRIVER")))
	if driver == "" {
		driver = "sqlite"
	}
	dsn := strings.TrimSpace(os.Getenv("AGENT_SAAS_DB_DSN"))
	if dsn == "" && driver == "sqlite" {
		dsn = "agent.db"
	}
	if dsn == "" {
		return errors.New("AGENT_SAAS_DB_DSN is required")
	}
	sqlDriver := driver
	if driver == "postgres" || driver == "postgresql" {
		sqlDriver = "pgx"
	}
	database, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.PingContext(context.Background()); err != nil {
		return err
	}
	schema := strings.TrimSpace(os.Getenv("AGENT_SAAS_DB_SCHEMA"))
	if err := agentstore.EnsureSchema(context.Background(), database, driver, schema); err != nil {
		return err
	}
	renderer, err := agentstore.Renderer(driver, schema)
	if err != nil {
		return err
	}
	store, err := agentstore.NewStore(database, renderer, driver)
	if err != nil {
		return err
	}
	repositories := agentstore.NewRepositories(store)
	agentID, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("AGENT_HTTP_AGENT_ID")))
	runner := provider.New(provider.Config{BaseURL: os.Getenv("AGENT_HTTP_BASE_URL"), APIKey: os.Getenv("AGENT_HTTP_API_KEY"), AgentID: agentID, Timeout: 120 * time.Second})
	if err := runner.Validate(); err != nil {
		return err
	}
	saasKey := strings.TrimSpace(os.Getenv("AGENT_SAAS_API_KEY"))
	if saasKey == "" {
		return errors.New("AGENT_SAAS_API_KEY is required")
	}
	service := server.New(server.Config{APIKey: saasKey, Runner: runner, Interactive: runner, Repositories: repositories})
	address := strings.TrimSpace(os.Getenv("AGENT_SAAS_ADDRESS"))
	if address == "" {
		address = ":8090"
	}
	httpServer := &http.Server{Addr: address, Handler: service.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 130 * time.Second, WriteTimeout: 130 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 1)
	go func() { result <- httpServer.ListenAndServe() }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}
