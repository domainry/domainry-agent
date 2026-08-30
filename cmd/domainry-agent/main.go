package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/domainry/domainry-agent/internal/provider"
	"github.com/domainry/domainry-agent/server"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	agentID, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("AGENT_HTTP_AGENT_ID")))
	runner := provider.New(provider.Config{BaseURL: os.Getenv("AGENT_HTTP_BASE_URL"), APIKey: os.Getenv("AGENT_HTTP_API_KEY"), AgentID: agentID, Timeout: 120 * time.Second})
	if err := runner.Validate(); err != nil {
		return err
	}
	saasKey := strings.TrimSpace(os.Getenv("AGENT_SAAS_API_KEY"))
	if saasKey == "" {
		return errors.New("AGENT_SAAS_API_KEY is required")
	}
	service := server.New(server.Config{APIKey: saasKey, Runner: runner, Interactive: runner})
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
