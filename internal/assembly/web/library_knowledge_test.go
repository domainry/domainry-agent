package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type libraryKnowledgeWebModel struct{}

func (libraryKnowledgeWebModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{Content: "目录和资料需要重新核对。", Model: "library-fixture"}, nil
}
func (libraryKnowledgeWebModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "test", Protocol: "chat_completions", Model: "library-fixture", Fingerprint: "library-fixture-v1"}
}
func (libraryKnowledgeWebModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	last := in.Messages[len(in.Messages)-1]
	call := func(name, id string, args any) (agentsdk.ConversationStepResult, error) {
		raw, _ := json.Marshal(args)
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: id, Name: name, Arguments: string(raw)}}}, FinishReason: "tool_calls"}, nil
	}
	answer := func(text string) (agentsdk.ConversationStepResult, error) {
		if e := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: text}); e != nil {
			return agentsdk.ConversationStepResult{}, e
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: text}, FinishReason: "stop"}, nil
	}
	if last.Role == "user" {
		return call("knowledge_libraries", "catalog", map[string]any{})
	}
	var result agentsdk.ConversationToolResult
	var evidence agentsdk.ConversationKnowledgeResult
	if json.Unmarshal([]byte(last.Content), &result) != nil || result.Status != "completed" || json.Unmarshal(result.Content, &evidence) != nil {
		return answer("当前无法读取该资料，请核对权限。")
	}
	switch last.ToolCallID {
	case "catalog":
		var page agentsdk.KnowledgeLibraryPage
		if json.Unmarshal(evidence.Data, &page) != nil {
			return answer("资料库目录无效。")
		}
		for _, library := range page.Items {
			if library.Kind == "shared" {
				return call("knowledge_search", "search", map[string]string{"library_id": library.ID, "query": "费用规则"})
			}
		}
		return answer("没有当前可读的共享资料库。")
	case "search":
		if len(evidence.Citations) == 0 {
			return answer("没有找到可核对的资料片段。")
		}
		return call("knowledge_read", "read", map[string]string{"library_id": evidence.LibraryID, "doc_id": evidence.Citations[0].DocumentID})
	case "read":
		if len(evidence.Citations) != 1 {
			return answer("资料来源不完整。")
		}
		return answer("共享资料记载：" + evidence.Citations[0].Excerpt + "。[[cite:" + evidence.Citations[0].ID + "]]")
	}
	return answer("请提供需要查询的资料。")
}

func TestLibraryKnowledgeIdentityMembershipHTTPAndRestart(t *testing.T) {
	const initial, changed = "Initial-Library-Knowledge!2", "Changed-Library-Knowledge!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "library-knowledge-test-signing-secret")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "library-knowledge-test-encryption-secret")
	t.Setenv("APP_ENV", "development")
	var sharedCalls, privateCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args struct {
			KBID   string `json:"kb_id"`
			DocID  string `json:"doc_id"`
			TeamID string `json:"team_id"`
		}
		if json.NewDecoder(r.Body).Decode(&args) != nil {
			t.Error("invalid knowledge request")
		}
		if args.TeamID != "team" {
			t.Error("wrong remote team")
		}
		body := "资料金额：123.45 元"
		if args.KBID == "shared" {
			sharedCalls.Add(1)
		} else {
			privateCalls.Add(1)
			body = "PRIVATE-UNRELATED-LIBRARY"
		}
		if r.URL.Path == "/v1/kb/search" {
			fmt.Fprintf(w, `{"hits":[{"doc_id":"policy","title":"共享费用规则","body":%q}]}`, body)
		} else {
			if args.DocID != "policy" {
				t.Error("wrong scoped document")
			}
			fmt.Fprintf(w, `{"doc_id":"policy","title":"共享费用规则","body":%q}`, body)
		}
	}))
	defer upstream.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "library-knowledge.db"), RuntimeID: "library-knowledge-runtime", WorkspaceID: "library-knowledge-workspace", ApplicationKey: "library-knowledge-app", Agent: agentmodule.Options{ConversationProvider: libraryKnowledgeWebModel{}, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	newBrowser := func() *browser {
		handler, e := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if e != nil {
			t.Fatal(e)
		}
		return &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	a := newBrowser()
	a.login("admin@example.com", initial)
	a.changePassword(initial, changed)
	grant := func(user string) {
		mutateTestRolePermissions(t, host, a, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, p := range previous {
				if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationActionPrefix+"libraries_") && !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationToolActionPrefix+"knowledge_") {
					out = append(out, p)
				}
			}
			for _, op := range agentsdk.ConversationHTTPDefinitions() {
				if p := agentsdk.KnowledgeLibraryPermission(op.Operation); p != nil {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: p.Key, DataScope: identitysdk.DataScopeAll})
				}
			}
			for _, tool := range agentsdk.LibraryKnowledgeConversationTools() {
				out = append(out, identitysdk.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identitysdk.DataScopeOwner})
			}
			return out
		}, user)
	}
	grant("admin")
	decode := func(raw []byte) agentsdk.KnowledgeLibrary {
		var value agentsdk.KnowledgeLibrary
		if e := json.Unmarshal(raw, &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	shared := decode(a.call("POST", "/agent/knowledge-libraries", `{"client_id":"shared","kind":"shared","name":"共享规则库"}`, 200).Body.Bytes())
	private := decode(a.call("POST", "/agent/knowledge-libraries", `{"client_id":"private","kind":"personal","name":"个人规则库"}`, 200).Body.Bytes())
	b := newBrowser()
	b.login("system_administrator@example.com", initial)
	b.changePassword(initial, changed)
	second := b.readSession()["user_id"].(string)
	grant(second)
	for _, binding := range []struct{ ID, KB string }{{shared.ID, "shared"}, {private.ID, "private"}} {
		options.Agent.KnowledgeLibraries = append(options.Agent.KnowledgeLibraries, agentmodule.KnowledgeLibraryConfig{LibraryID: binding.ID, Knowledge: agentmodule.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "test-only-library-key", TeamID: "team", KBID: binding.KB, WorkspaceID: options.WorkspaceID, ResponseMapping: &agentmodule.KnowledgeResponseMapping{Search: &agentmodule.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}, Fetch: &agentmodule.KnowledgeCitationMapping{DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}}}})
	}
	reopen := func() {
		t.Helper()
		if e := host.Close(context.Background()); e != nil {
			t.Fatal(e)
		}
		host = nil
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		a = newBrowser()
		a.login("admin@example.com", changed)
		b = newBrowser()
		b.login("system_administrator@example.com", changed)
	}
	reopen()
	path := "/agent/knowledge-libraries/" + shared.ID
	if got := decode(a.call("GET", path, "", 200).Body.Bytes()); !got.KnowledgeConfigured {
		t.Fatal("library binding absent in public status")
	}
	send := func(client *browser, id string) (string, agentsdk.ConversationRun) {
		t.Helper()
		var c agentsdk.Conversation
		raw, _ := json.Marshal(agentsdk.ConversationCreate{ClientID: id, Title: "共享资料检索验收"})
		if e := json.Unmarshal(client.call("POST", "/agent/conversations", string(raw), 200).Body.Bytes(), &c); e != nil {
			t.Fatal(e)
		}
		base := "/agent/conversations/" + c.ID
		var run agentsdk.ConversationRun
		_ = json.Unmarshal(client.call("POST", base+"/messages", `{"client_message_id":"query","message":"查共享费用规则"}`, 202).Body.Bytes(), &run)
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			_ = json.Unmarshal(client.call("GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
			if run.Terminal() {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if run.Status != "completed" {
			t.Fatalf("library run not completed: %s %s", run.Status, run.ErrorCode)
		}
		return base, run
	}
	_, none := send(b, "no-membership")
	if sharedCalls.Load() != 0 || privateCalls.Load() != 0 || len(none.Steps) != 2 {
		t.Fatal("non-member reached knowledge source")
	}
	shared = decode(a.call("PUT", path+"/members/"+second, fmt.Sprintf(`{"role":"reader","expected_revision":%d}`, shared.Revision), 200).Body.Bytes())
	base, run := send(b, "readable")
	if len(run.Steps) != 4 || sharedCalls.Load() == 0 || privateCalls.Load() != 0 {
		t.Fatal("library search/read did not follow membership")
	}
	if len(run.Steps[2].Calls[0].Citations) != 1 || run.Steps[2].Calls[0].Citations[0].LibraryID != shared.ID {
		t.Fatal("library citation lost")
	}
	if body := b.call("GET", base+"/messages", "", 200).Body.String(); !strings.Contains(body, "123.45") || !strings.Contains(body, shared.ID) {
		t.Fatal("source not visible")
	}
	reopen()
	if body := b.call("GET", base+"/messages", "", 200).Body.String(); !strings.Contains(body, "123.45") {
		t.Fatal("restart lost valid source")
	}
	mutateTestRolePermissions(t, host, a, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		out := []identitysdk.ProjectRolePermission{}
		for _, p := range previous {
			if p.PermissionKey != agentsdk.ConversationActionPrefix+"libraries_get" {
				out = append(out, p)
			}
		}
		return out
	}, second)
	before := sharedCalls.Load()
	if body := b.call("GET", base+"/messages", "", 200).Body.String(); strings.Contains(body, "123.45") || strings.Contains(body, `"citations":[{`) {
		t.Fatal("library member bypassed revoked Identity read action")
	}
	if sharedCalls.Load() != before {
		t.Fatal("revoked library action reached remote source")
	}
	grant(second)
	if body := b.call("GET", base+"/messages", "", 200).Body.String(); !strings.Contains(body, "123.45") {
		t.Fatal("restored Identity action cannot read original source")
	}
	shared = decode(a.call("DELETE", path+"/members/"+second+"?expected_revision="+fmt.Sprint(shared.Revision), "", 200).Body.Bytes())
	before = sharedCalls.Load()
	if body := b.call("GET", base+"/messages", "", 200).Body.String(); strings.Contains(body, "123.45") || strings.Contains(body, `"citations":[{`) {
		t.Fatal("removed member read historical source")
	}
	if sharedCalls.Load() != before {
		t.Fatal("removed member remote request")
	}
	shared = decode(a.call("PUT", path+"/members/"+second, fmt.Sprintf(`{"role":"reader","expected_revision":%d}`, shared.Revision), 200).Body.Bytes())
	if body := b.call("GET", base+"/messages", "", 200).Body.String(); !strings.Contains(body, "123.45") {
		t.Fatal("restored member cannot read original reply")
	}
	setArchived := func(archived bool) {
		shared = decode(a.call("PATCH", path, fmt.Sprintf(`{"name":"共享规则库","archived":%t,"expected_revision":%d}`, archived, shared.Revision), 200).Body.Bytes())
	}
	setArchived(true)
	if body := b.call("GET", base+"/messages", "", 200).Body.String(); strings.Contains(body, "123.45") {
		t.Fatal("archived source remains readable")
	}
	setArchived(false)
	a.call("POST", "/agent/conversations", `{"client_id":"library-ui","title":"资料库检索网页验收"}`, 200)
	servePersonalToolAcceptance(t, host, options, map[string]func(){"archive_shared_library": func() { setArchived(true) }, "restore_shared_library": func() { setArchived(false) }})
}
