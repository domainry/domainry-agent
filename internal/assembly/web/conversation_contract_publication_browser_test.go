package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
)

func exerciseContractPublicationBrowser(t *testing.T, host *Host, options Options, conversation, delegation, password string, changeShare func(bool)) {
	t.Helper()
	if os.Getenv("AGENT_CONTRACT_PUBLICATION_BROWSER") != "1" {
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
	type change struct {
		allowed bool
		done    chan struct{}
	}
	changes := make(chan change)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/fixture/contract-publication-share" {
			request := change{allowed: r.URL.Query().Get("allowed") == "true", done: make(chan struct{})}
			select {
			case changes <- request:
			case <-r.Context().Done():
				return
			}
			select {
			case <-request.done:
				w.WriteHeader(204)
			case <-r.Context().Done():
			}
			return
		}
		ui.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()
	command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/contract-publication.browser.mjs"))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_CONVERSATION="+conversation, "AGENT_UI_DELEGATION="+delegation, "AGENT_UI_PASSWORD="+password)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	for {
		select {
		case request := <-changes:
			changeShare(request.allowed)
			close(request.done)
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
