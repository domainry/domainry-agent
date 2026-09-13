package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
)

func exercisePeerPermissionBrowser(t *testing.T, host *Host, b *browser, options Options, source, delegation string) {
	t.Helper()
	if os.Getenv("AGENT_PEER_PERMISSIONS_BROWSER") != "1" {
		return
	}
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Model: "peer", Files: os.DirFS(filepath.Join(project, "frontend/dist")), ApplicationRoutes: host.CollaborationSetupRoutes()})
	if err != nil {
		t.Fatal(err)
	}
	type permissionChange struct {
		profile string
		done    chan struct{}
	}
	changes := make(chan permissionChange)
	profiles := map[string][]string{"all": sdk.ConversationCollaborationOperations(), "view": {"view"}, "delivery": {"view", "delivery_read"}, "manage": {"view", "manage"}, "none": {}}
	// This fixture control exists only on this isolated test listener. Actual
	// permission changes use Identity's public role-authoring HTTP contract.
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/fixture/collaboration-permissions" {
			profile := r.URL.Query().Get("profile")
			if _, ok := profiles[profile]; !ok {
				w.WriteHeader(400)
				return
			}
			change := permissionChange{profile: profile, done: make(chan struct{})}
			select {
			case changes <- change:
			case <-r.Context().Done():
				return
			}
			select {
			case <-change.done:
				w.WriteHeader(204)
			case <-r.Context().Done():
			}
			return
		}
		ui.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()
	command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/peer-permissions.browser.mjs"))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_CONVERSATION="+source, "AGENT_UI_DELEGATION="+delegation)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	for {
		select {
		case change := <-changes:
			setTestCollaborationPermissions(t, host, b, profiles[change.profile]...)
			close(change.done)
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}
