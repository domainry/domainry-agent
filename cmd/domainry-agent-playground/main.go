// Command domainry-agent-playground hosts the real conversation application
// with a loopback-only browser test fixture. It is not a production login host.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/domainry/domainry-agent/internal/application"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	persistence "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	"github.com/domainry/domainry-agent/internal/transport/http/playground"
	knowledgehttpapi "github.com/domainry/domainry-connectors/providers/knowledge_base/http_api"
	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/processhost"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	_ "modernc.org/sqlite"
)

func main() {
	if run() != nil {
		os.Exit(1)
	}
}
func run() (runErr error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	observability, err := processhost.Open(ctx, processhost.Config{ServiceName: "domainry-agent-playground"})
	if err != nil {
		return err
	}
	started := false
	startupFailure := logging.Failure{Stage: "provider_initialize", Dependency: "conversation_model"}
	defer func() {
		if !started && runErr != nil {
			observability.StartFailed(context.Background(), runErr, startupFailure)
			runErr = errors.Join(runErr, observability.Close(context.Background(), nil))
			return
		}
		runErr = observability.Finish(context.Background(), nil, runErr)
	}()
	config := provider.ConversationModelConfigFromEnvironment()
	model, err := provider.NewConversationModel(config)
	if err != nil {
		return err
	}
	address := os.Getenv("AGENT_PLAYGROUND_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8090"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("playground must listen on a literal loopback IP")
	}
	startupFailure = logging.Failure{Stage: "listener_bind", Dependency: "public_listener"}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	path := os.Getenv("AGENT_PLAYGROUND_DB")
	if path == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		path = filepath.Join(cache, "domainry-agent", "playground", "agent.db")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	startupFailure = logging.Failure{Stage: "database_initialize", Dependency: "agent_database"}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = persistence.EnsureSchema(context.Background(), db, "sqlite", ""); err != nil {
		return err
	}
	renderer, err := persistence.Renderer("sqlite", "")
	if err != nil {
		return err
	}
	store, err := persistence.NewAgentStore(db, renderer, "sqlite", "agent-playground")
	if err != nil {
		return err
	}
	knowledgeConfig := provider.KnowledgeConfigFromEnvironment()
	knowledgeConfig.RuntimeID = "agent-playground"
	knowledge, err := knowledgemodule.NewKnowledge(knowledgeConfig, knowledgehttpapi.New)
	if err != nil {
		return err
	}
	options := application.ConversationOptions{}
	if knowledge != nil {
		options.Knowledge = knowledge
	}
	startupFailure = logging.Failure{Stage: "module_assembly"}
	conversations, err := conversationassembly.NewService(agentstore.NewConversationStore(store), model, "agent-playground", options)
	if err != nil {
		return err
	}
	defer conversations.Close()
	frontend := os.Getenv("AGENT_PLAYGROUND_FRONTEND")
	if frontend == "" {
		frontend = filepath.Join("frontend", "dist")
	}
	if _, err := os.Stat(filepath.Join(frontend, "index.html")); err != nil {
		return fmt.Errorf("build the chat UI with npm --prefix frontend run build: %w", err)
	}
	handler, err := playground.NewHandler(conversations, listener.Addr().String(), config.Model, os.DirFS(frontend))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: observability.Handler("public", handler), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	started = true
	observability.Started(ctx, map[string]any{"listener_group": "public", "mode": "playground"})
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		observability.Stopping(context.Background(), map[string]any{"reason": "signal"})
		conversations.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
