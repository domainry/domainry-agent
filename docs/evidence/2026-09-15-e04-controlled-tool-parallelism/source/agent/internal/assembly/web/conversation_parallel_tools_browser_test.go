package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

func TestControlledToolParallelismBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_E04_BROWSER") != "1" {
		t.Skip("opt-in controlled tool parallelism browser acceptance")
	}
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	output := os.Getenv("AGENT_UI_TEST_OUTPUT")
	if output == "" || os.Getenv("AGENT_NODE_BINARY") == "" {
		t.Fatal("AGENT_UI_TEST_OUTPUT and AGENT_NODE_BINARY are required")
	}
	if err = os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	const initial, changed = "Initial-Parallel-Browser!2", "Changed-Parallel-Browser!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "parallel-browser-signing-secret-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "parallel-browser-data-secret-long-enough")
	t.Setenv("APP_ENV", "development")
	fixture := newParallelReadHost(false, true)
	options := Options{
		DatabasePath:   filepath.Join(t.TempDir(), "parallel-browser.db"),
		RuntimeID:      "parallel-browser-runtime",
		WorkspaceID:    "parallel-browser-workspace",
		ApplicationKey: "parallel-browser-app",
		Agent: agentmodule.Options{
			ConversationProvider: parallelReadModel{calls: 2},
			ConversationOptions: agentmodule.ConversationOptions{
				ToolHost:         fixture,
				Poll:             5 * time.Millisecond,
				MaxParallelTools: 2,
			},
		},
	}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	files := os.DirFS(filepath.Join(project, "frontend/dist"))
	bootstrap, err := webhttp.NewHandler(webhttp.Options{
		Identity:       host.Identity,
		Agent:          host.Agent,
		RuntimeID:      options.RuntimeID,
		WorkspaceID:    options.WorkspaceID,
		ApplicationKey: options.ApplicationKey,
		Origin:         "http://127.0.0.1:8091",
		Model:          "parallel-fixture",
		Files:          files,
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := &browser{t: t, handler: bootstrap, cookies: map[string]*http.Cookie{}}
	admin.login("admin@example.com", initial)
	admin.changePassword(initial, changed)
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler, err := webhttp.NewHandler(webhttp.Options{
		Identity:       host.Identity,
		Agent:          host.Agent,
		RuntimeID:      options.RuntimeID,
		WorkspaceID:    options.WorkspaceID,
		ApplicationKey: options.ApplicationKey,
		Origin:         origin,
		Model:          "parallel-fixture",
		Files:          files,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/tool-parallelism.browser.mjs"))
	command.Dir = project
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_TEST_OUTPUT="+output, "AGENT_UI_PASSWORD="+changed)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal("controlled tool parallelism browser", err)
	}
	fixture.mu.Lock()
	peak := fixture.peak
	fixture.mu.Unlock()
	if peak != 2 || fixture.authChecks.Load() < 2 {
		t.Fatalf("parallel browser peak=%d authorization_checks=%d", peak, fixture.authChecks.Load())
	}
	raw, _ := json.MarshalIndent(map[string]any{
		"complete":                       true,
		"compiled_frontend":              true,
		"real_identity_http":             true,
		"real_sqlite":                    true,
		"explicit_read_calls":            2,
		"execution_authorization_checks": 2,
		"host_authorization_calls_including_result_reads": fixture.authChecks.Load(),
		"observed_parallel_peak":                          peak,
		"ordered_result_projection":                       true,
	}, "", "  ")
	if err = os.WriteFile(filepath.Join(output, "host-parallelism.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
