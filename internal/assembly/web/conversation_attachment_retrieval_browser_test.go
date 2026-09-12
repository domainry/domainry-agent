package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
)

type privateAttachmentWebModel struct{ libraryKnowledgeWebModel }

func (privateAttachmentWebModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "test", Protocol: "chat_completions", Model: "private-attachment-fixture", Fingerprint: "private-attachment-fixture-v1"}
}
func (privateAttachmentWebModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	last := in.Messages[len(in.Messages)-1]
	call := func(name, id string, args any) (agentsdk.ConversationStepResult, error) {
		raw, _ := json.Marshal(args)
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{Name: name, ID: id, Arguments: string(raw)}}}, FinishReason: "tool_calls"}, nil
	}
	if last.Role == "user" {
		return call("attachment_search", "search", map[string]string{"query": "附件检索"})
	}
	var result agentsdk.ConversationToolResult
	var evidence agentsdk.ConversationKnowledgeResult
	if json.Unmarshal([]byte(last.Content), &result) != nil || result.Status != "completed" || json.Unmarshal(result.Content, &evidence) != nil || len(evidence.Citations) != 1 {
		return agentsdk.ConversationStepResult{}, fmt.Errorf("private attachment fixture evidence unavailable")
	}
	if last.ToolCallID == "search" {
		return call("attachment_read", "read", map[string]string{"attachment_id": evidence.Citations[0].DocumentID})
	}
	text := evidence.Citations[0].Excerpt + " [[cite:" + evidence.Citations[0].ID + "]]"
	if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: text}); err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: text}, FinishReason: "stop"}, nil
}

// Opt-in browser exercise of the exact built client against the same real
// Identity, Module, SQLite and Connector protocol fixture as the HTTP test.
func runPrivateAttachmentCitationBrowser(t *testing.T, host *Host, options Options, conversation, attachment string, original []byte, grant func(string)) {
	t.Helper()
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	output := os.Getenv("AGENT_UI_TEST_OUTPUT")
	if output == "" {
		t.Fatal("browser evidence output directory required")
	}
	if err = os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(output, "original.pdf"), original, 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Files: os.DirFS(filepath.Join(project, "frontend/dist"))})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__acceptance/") {
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			switch r.URL.Path {
			case "/__acceptance/revoke":
				grant("attachments_download")
			case "/__acceptance/restore":
				grant("")
			default:
				w.WriteHeader(404)
				return
			}
			w.WriteHeader(204)
			return
		}
		handler.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	node := os.Getenv("AGENT_NODE_BINARY")
	if node == "" {
		t.Fatal("AGENT_NODE_BINARY required")
	}
	command := exec.CommandContext(ctx, node, filepath.Join(project, "frontend/tests/attachment-citations.browser.mjs"))
	command.Dir = project
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_CONVERSATION="+conversation, "AGENT_UI_ATTACHMENT="+attachment)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal("private citation browser failed", err)
	}
}
