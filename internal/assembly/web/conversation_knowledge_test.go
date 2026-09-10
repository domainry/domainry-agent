package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func TestKnowledgeToolsUseLiveIdentityDocumentPermissionsAndStoredResultChecks(t *testing.T) {
	const initial, changed = "Initial-Knowledge-Test!2", "Changed-Knowledge-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "knowledge-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "knowledge-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	var mu sync.Mutex
	var requests [][]string
	knowledge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args struct {
			Permissions []string `json:"permission_ids"`
			DocID       string   `json:"doc_id"`
		}
		if json.NewDecoder(r.Body).Decode(&args) != nil {
			t.Error("bad knowledge request")
		}
		mu.Lock()
		requests = append(requests, append([]string(nil), args.Permissions...))
		mu.Unlock()
		allowed := len(args.Permissions) == 1 && args.Permissions[0] == "finance-upstream"
		if len(args.Permissions) > 0 && !allowed {
			t.Error("unexpected permission escalation")
		}
		if r.URL.Path == "/v1/kb/fetch" {
			if !allowed {
				http.Error(w, "private denied", 403)
				return
			}
			if args.DocID != "policy" {
				t.Error("wrong document fetched")
			}
			fmt.Fprint(w, `{"doc_id":"policy","title":"私有费用规则","url":"https://knowledge.example.test/policy","body":"PRIVATE-FINANCE-EVIDENCE"}`)
		} else if allowed {
			fmt.Fprint(w, `{"hits":[{"doc_id":"policy","title":"私有费用规则","url":"https://knowledge.example.test/policy","snippet":"PRIVATE-FINANCE-EVIDENCE"}]}`)
		} else {
			fmt.Fprint(w, `{"hits":[]}`)
		}
	}))
	defer knowledge.Close()
	var reference agentsdk.ConversationResultReference
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role, Content string
				CallID        string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.Messages) == 0 {
			t.Error("invalid model payload")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta any, finish string) {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		call := func(name, id string, args any) {
			raw, _ := json.Marshal(args)
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}, "")
			write(map[string]any{}, "tool_calls")
		}
		answer := func(text string) { write(map[string]any{"content": text}, ""); write(map[string]any{}, "stop") }
		last := payload.Messages[len(payload.Messages)-1]
		switch {
		case last.Role == "user" && last.Content == "重新读取刚才的原始结果":
			mu.Lock()
			ref := reference
			mu.Unlock()
			call("tool_result_read", "old", agentsdk.ConversationResultRead{Reference: ref})
		case last.Role == "user":
			call("knowledge_search", "search", map[string]string{"query": "费用规则"})
		case last.CallID == "search" && strings.Contains(last.Content, "PRIVATE-FINANCE-EVIDENCE"):
			call("knowledge_read", "read", map[string]string{"doc_id": "policy"})
		case last.CallID == "read":
			if !strings.Contains(last.Content, "PRIVATE-FINANCE-EVIDENCE") {
				t.Error("authorized fetch missing")
			}
			var envelope agentsdk.ConversationToolResult
			var evidence agentsdk.ConversationKnowledgeResult
			if json.Unmarshal([]byte(last.Content), &envelope) != nil || json.Unmarshal(envelope.Content, &evidence) != nil || len(evidence.Citations) != 1 {
				t.Error("model did not receive real citation metadata")
				return
			}
			answer("已读取有权限的费用规则。[[cite:" + evidence.Citations[0].ID + "]]")
		case last.CallID == "old":
			if !strings.Contains(last.Content, "knowledge_access_denied") || strings.Contains(last.Content, "PRIVATE-FINANCE-EVIDENCE") {
				t.Error("revoked saved data reached model")
			}
			answer("该资料权限已变化，当前无法读取。")
		default:
			if strings.Contains(last.Content, "PRIVATE-FINANCE-EVIDENCE") {
				t.Error("private source exposed without document grant")
			}
			answer("未找到当前有权限的资料。")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "knowledge.db"), RuntimeID: "knowledge-runtime", WorkspaceID: "knowledge-workspace", ApplicationKey: "knowledge-app", KnowledgePermissions: map[string]string{"finance": "finance-upstream"}, Agent: agentmodule.Options{ConversationURL: model.URL, ConversationModel: "knowledge-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}, Knowledge: agentmodule.KnowledgeConfig{BaseURL: knowledge.URL, APIKey: "knowledge-fixture-key", TeamID: "team", KBID: "kb", WorkspaceID: "knowledge-workspace"}}}
	options.Agent.Knowledge.ResponseMapping = &agentmodule.KnowledgeResponseMapping{Search: &agentmodule.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Title: "/title", URL: "/url", Excerpt: "/snippet"}, Fetch: &agentmodule.KnowledgeCitationMapping{DocumentID: "/doc_id", Title: "/title", URL: "/url", Excerpt: "/body"}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "knowledge-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, UserID: "admin"}
	for _, definition := range agentsdk.KnowledgeConversationTools() {
		auth, err := host.AuthorizeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: a, Definition: definition})
		if err != nil || auth.Granted {
			t.Fatal("registration automatically granted access", err)
		}
	}
	grantPersonalTools(t, host, b, true)
	grant := func(private bool) {
		mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, p := range previous {
				if p.PermissionKey != knowledgePermissionResource+".finance" && p.PermissionKey != agentsdk.ConversationToolActionPrefix+"knowledge_search" && p.PermissionKey != agentsdk.ConversationToolActionPrefix+"knowledge_read" {
					out = append(out, p)
				}
			}
			for _, d := range agentsdk.KnowledgeConversationTools() {
				out = append(out, identitysdk.ProjectRolePermission{PermissionKey: d.ActionKey, DataScope: identitysdk.DataScopeOwner})
			}
			if private {
				out = append(out, identitysdk.ProjectRolePermission{PermissionKey: knowledgePermissionResource + ".finance", DataScope: identitysdk.DataScopeOwner})
			}
			return out
		})
	}
	var c agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"knowledge"}`, 200).Body.Bytes(), &c)
	base := "/agent/conversations/" + c.ID
	send := func(id, message string) agentsdk.ConversationRun {
		raw, _ := json.Marshal(map[string]string{"client_message_id": id, "message": message})
		var run agentsdk.ConversationRun
		_ = json.Unmarshal(b.call("POST", base+"/messages", string(raw), 202).Body.Bytes(), &run)
		// Include source revalidation and the final persistence transaction under
		// the race detector; observing a completed step is not a completed Run.
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			_ = json.Unmarshal(b.call("GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
			if run.Status == "completed" || run.Status == "failed" {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if run.Status != "completed" {
			t.Fatalf("knowledge run failed: %+v", run)
		}
		return run
	}
	grant(false)
	public := send("public", "查费用规则")
	if len(public.Steps) != 2 {
		t.Fatal("private document was fetched without grant")
	}
	grant(true)
	private := send("private", "读取费用规则正文")
	if len(private.Steps) != 3 || len(private.Steps[1].Calls) != 1 || private.Steps[1].Calls[0].ResultReference == nil {
		t.Fatal("knowledge execution evidence missing")
	}
	mu.Lock()
	reference = *private.Steps[1].Calls[0].ResultReference
	mu.Unlock()
	if len(private.Steps[0].Calls[0].Citations) != 1 || len(private.Steps[1].Calls[0].Citations) != 1 {
		t.Fatal("citation projection missing")
	}
	citation := private.Steps[1].Calls[0].Citations[0]
	if citation.DocumentID != "policy" || citation.Title != "私有费用规则" || citation.Excerpt != "PRIVATE-FINANCE-EVIDENCE" || citation.URL != "https://knowledge.example.test/policy" {
		t.Fatal("citation lost real source fields")
	}
	var citedPage agentsdk.ConversationMessagePage
	_ = json.Unmarshal(b.call("GET", base+"/messages", "", 200).Body.Bytes(), &citedPage)
	foundCitation := false
	for _, message := range citedPage.Items {
		if message.Role == "assistant" && message.RunID == private.ID {
			foundCitation = len(message.Citations) == 1 && message.Citations[0].ID == citation.ID && strings.Contains(message.Content, "[[cite:"+citation.ID+"]]")
		}
	}
	if !foundCitation {
		t.Fatal("persisted reply lost its citation linkage")
	}
	stream := b.call("GET", base+"/runs/"+private.ID+"/events/stream?scope="+b.scope, "", 200).Body.String()
	if !strings.Contains(stream, `"citations"`) || !strings.Contains(stream, citation.ID) {
		t.Fatal("SSE did not preserve citation metadata")
	}
	servePersonalToolAcceptance(t, host, options, map[string]func(){
		"knowledge/revoke":  func() { grant(false) },
		"knowledge/restore": func() { grant(true) },
	})
	grant(false)
	send("revoked", "重新读取刚才的原始结果")
	var restricted agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("GET", base+"/runs/"+private.ID, "", 200).Body.Bytes(), &restricted)
	if restricted.AccessError == "" || restricted.DraftText != "" || len(restricted.Steps) != 0 || restricted.Interaction != nil {
		t.Fatal("HTTP run view retained revoked source data")
	}
	blockedStream := b.call("GET", base+"/runs/"+private.ID+"/events/stream?scope="+b.scope, "", 403).Body.String()
	if strings.Contains(blockedStream, "PRIVATE-FINANCE-EVIDENCE") || !strings.Contains(blockedStream, "source_access_unavailable") {
		t.Fatal("HTTP SSE authorization did not protect saved events")
	}
	var page agentsdk.ConversationMessagePage
	_ = json.Unmarshal(b.call("GET", base+"/messages", "", 200).Body.Bytes(), &page)
	foundRestricted := false
	for _, message := range page.Items {
		if message.Role == "assistant" && message.RunID == private.ID {
			foundRestricted = message.AccessError != "" && len(message.Citations) == 0
		}
	}
	if !foundRestricted {
		t.Fatal("HTTP history did not mark the unavailable source")
	}
	grant(true)
	var restored agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("GET", base+"/runs/"+private.ID, "", 200).Body.Bytes(), &restored)
	if restored.AccessError != "" || len(restored.Steps) != 3 {
		t.Fatal("restoring Identity permissions did not restore the stored run view")
	}
	mu.Lock()
	defer mu.Unlock()
	hadPrivate, hadPublic := false, false
	for _, ids := range requests {
		hadPrivate = hadPrivate || len(ids) == 1
		hadPublic = hadPublic || len(ids) == 0
	}
	if !hadPrivate || !hadPublic {
		t.Fatal("live Identity changes never reached connector")
	}
}
