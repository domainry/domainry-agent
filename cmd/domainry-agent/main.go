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

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	agentpersistence "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
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
	if err := agentpersistence.EnsureSchema(context.Background(), database, driver, schema); err != nil {
		return err
	}
	renderer, err := agentpersistence.Renderer(driver, schema)
	if err != nil {
		return err
	}
	store, err := agentpersistence.NewAgentStore(database, renderer, driver)
	if err != nil {
		return err
	}
	service, closeService, err := openService(store)
	if err != nil {
		return err
	}
	defer closeService()
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

// openService is shared by the executable and startup acceptance tests. A
// Conversation deployment does not construct or validate the legacy runner.
func openService(store *agentstore.Store) (*server.Server, func(), error) {
	closeService := func() {}
	saasKey := strings.TrimSpace(os.Getenv("AGENT_SAAS_API_KEY"))
	if saasKey == "" {
		return nil, closeService, errors.New("AGENT_SAAS_API_KEY is required")
	}
	config := server.Config{APIKey: saasKey}
	baseURL, apiKey, rawID := strings.TrimSpace(os.Getenv("AGENT_HTTP_BASE_URL")), strings.TrimSpace(os.Getenv("AGENT_HTTP_API_KEY")), strings.TrimSpace(os.Getenv("AGENT_HTTP_AGENT_ID"))
	if baseURL != "" || apiKey != "" || rawID != "" {
		agentID, err := strconv.Atoi(rawID)
		if err != nil {
			return nil, closeService, errors.New("AGENT_HTTP_AGENT_ID must be a positive integer when legacy provider is configured")
		}
		runner := provider.New(provider.Config{BaseURL: baseURL, APIKey: apiKey, AgentID: agentID, Timeout: 120 * time.Second})
		if err := runner.Validate(); err != nil {
			return nil, closeService, err
		}
		repositories := agentstore.NewRepositories(store)
		config.Runner, config.Interactive = runner, runner
		config.DialogState = agentapplication.NewDialogStateService(repositories.AgentStateRepository())
		config.Repositories, config.Lifecycle = repositories, repositories.AgentLifecycleRepository()
	}
	runtimeID := strings.TrimSpace(os.Getenv("AGENT_SAAS_RUNTIME_ID"))
	modelConfig := provider.ConversationModelConfigFromEnvironment()
	knowledgeConfig := provider.KnowledgeConfigFromEnvironment()
	if runtimeID == "" && (modelConfig.Configured() || knowledgeConfig.Configured()) {
		return nil, closeService, errors.New("AGENT_SAAS_RUNTIME_ID is required for conversations")
	}
	if runtimeID != "" {
		var model agentsdk.ConversationModel
		if modelConfig.Configured() {
			configured, err := provider.NewConversationModel(modelConfig)
			if err != nil {
				return nil, closeService, err
			}
			model = configured
		}
		knowledge, err := provider.NewKnowledge(knowledgeConfig)
		if err != nil {
			return nil, closeService, err
		}
		options := agentapplication.ConversationOptions{}
		if knowledge != nil {
			options.Knowledge = knowledge
		}
		conversations, err := agentapplication.NewConversationService(agentstore.NewConversationStore(store), model, runtimeID, options)
		if err != nil {
			return nil, closeService, err
		}
		closeService = conversations.Close
		config.Conversations, config.ConversationRuntimeID = conversations, runtimeID
	}
	if config.Runner == nil && config.Conversations == nil {
		return nil, closeService, errors.New("configure conversations or the legacy Agent provider")
	}
	service, err := server.New(config)
	if err != nil {
		closeService()
	}
	return service, closeService, err
}
