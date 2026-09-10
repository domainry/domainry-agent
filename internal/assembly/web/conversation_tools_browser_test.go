package web

import (
	"context"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
)

// Optional manual browser acceptance against the compiled product UI, real
// Identity module and temporary SQLite. The caller selects either a protocol
// fixture or an opt-in real model. It never changes the database or grants of
// a running deployment.
func servePersonalToolAcceptance(t *testing.T, host *Host, options Options, controls ...map[string]func()) {
	servePersonalToolAcceptanceWithHost(t, func() *Host { return host }, options, controls...)
}

// A fixture can close/reopen its actual host and return the replacement here.
// Rebuilding the HTTP composition after a test control keeps browser cookies
// and the listening address while exercising real database/storage reopening.
func servePersonalToolAcceptanceWithHost(t *testing.T, currentHost func() *Host, options Options, controls ...map[string]func()) {
	if os.Getenv("AGENT_TOOL_UI_ACCEPTANCE") != "1" {
		return
	}
	t.Helper()
	const address = "127.0.0.1:8092"
	modelLabel := "Local tool protocol fixture"
	if os.Getenv("AGENT_DAILY_WORK_LIVE") == "1" || os.Getenv("AGENT_KNOWLEDGE_LIVE") == "1" {
		modelLabel = options.Agent.ConversationModel
	}
	buildHandler := func() (http.Handler, error) {
		host := currentHost()
		return webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://" + address, Model: modelLabel, Files: os.DirFS("../../../frontend/dist")})
	}
	handler, err := buildHandler()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	done := make(chan struct{})
	mux := http.NewServeMux()
	var handlerMu sync.RWMutex
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerMu.RLock()
		current := handler
		handlerMu.RUnlock()
		current.ServeHTTP(w, r)
	}))
	mux.HandleFunc("POST /__acceptance/finish", func(w http.ResponseWriter, r *http.Request) { once.Do(func() { close(done) }); w.WriteHeader(204) })
	// Test-only controls exercise a live policy change while the product page
	// stays open. They are never mounted by the production web host.
	var controlMu sync.Mutex
	for _, group := range controls {
		for name, apply := range group {
			mux.HandleFunc("POST /__acceptance/"+name, func(w http.ResponseWriter, r *http.Request) {
				controlMu.Lock()
				defer controlMu.Unlock()
				apply()
				next, err := buildHandler()
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				handlerMu.Lock()
				handler = next
				handlerMu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			})
		}
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	t.Log("Temporary browser acceptance ready at http://127.0.0.1:8092")
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("browser acceptance cancelled")
	case <-time.After(15 * time.Minute):
		t.Fatal("browser acceptance timed out")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
