package web

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
)

func exerciseParticipantDeliveryBrowser(t *testing.T, host *Host, conversation, readerID, readerEmail, readerPassword string) {
	t.Helper()
	if os.Getenv("AGENT_PARTICIPANT_DELIVERY_BROWSER") != "1" {
		return
	}
	exerciseParticipantBrowser(t, host, "participant-delivery.browser.mjs", conversation, readerID, readerEmail, readerPassword)
}

func exerciseParticipantBrowser(t *testing.T, host *Host, script, conversation, readerID, readerEmail, readerPassword string) {
	t.Helper()
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: host.runtimeID, WorkspaceID: string(host.application.WorkspaceID), ApplicationKey: string(host.application.ApplicationKey), Origin: origin, Model: "peer", Files: os.DirFS(filepath.Join(project, "frontend/dist")), ApplicationRoutes: host.CollaborationSetupRoutes()})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = ui
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests", script))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_CONVERSATION="+conversation, "AGENT_UI_READER="+readerID, "AGENT_UI_READER_EMAIL="+readerEmail, "AGENT_UI_READER_PASSWORD="+readerPassword)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
}
