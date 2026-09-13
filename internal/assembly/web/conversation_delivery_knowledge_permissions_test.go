package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
)

func TestPeerKnowledgeDeliveryReadsWithoutSearchReadOrExtractExecution(t *testing.T) {
	const initial, changed = "Knowledge-Receipt-Initial!26", "Knowledge-Receipt-Changed!26"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "knowledge-receipt-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "knowledge-receipt-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	var mu sync.Mutex
	type original struct{ name, body string }
	files := map[string]original{}
	puts := 0
	changedSource := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/documents") {
			id := r.URL.Query().Get("doc_id")
			if r.Method == "POST" {
				data, _ := io.ReadAll(r.Body)
				files[id] = original{r.URL.Query().Get("filename"), string(data)}
				puts++
			} else {
				delete(files, id)
			}
			io.WriteString(w, `{"err_code":0}`)
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		item := func(id string, f original) map[string]any {
			body := f.body
			if changedSource {
				body = "金额：999.99"
			}
			return map[string]any{"doc_id": id, "status": "INDEXED", "title": f.name, "body": body}
		}
		if r.URL.Path == "/v1/kb/search" {
			hits := []any{}
			for id, f := range files {
				hits = append(hits, item(id, f))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": hits})
			return
		}
		id, _ := in["doc_id"].(string)
		f, ok := files[id]
		if !ok {
			io.WriteString(w, `{"err_code":1004}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": item(id, f)})
	}))
	defer upstream.Close()
	worker := &peerReceiptDeliveryModel{peerWebModel: peerWebModel{modelKey: "knowledge-review"}, summary: "已核对资料中的金额 123.45，附目录、检索、正文和抽取回执"}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "knowledge-receipt.db"), RuntimeID: "knowledge-receipt-runtime", WorkspaceID: "knowledge-receipt-workspace", ApplicationKey: "knowledge-receipt-app", Agent: agentmodule.Options{ConversationProvider: &peerWebModel{}, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"knowledge-review": worker}, Poll: 5 * time.Millisecond, DocumentPoll: 10 * time.Millisecond, MaxSteps: 12}}}
	mapping := &agentmodule.KnowledgeResponseMapping{Search: &agentmodule.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}, Fetch: &agentmodule.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}}
	options.Agent.KnowledgeDatasources = []agentmodule.KnowledgeDatasourceConfig{{Key: "source", Name: "验收资料源", Knowledge: agentmodule.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture-key", TeamID: "team", KBID: "project", WorkspaceID: options.WorkspaceID, ResponseMapping: mapping}}}
	var host *Host
	open := func() http.Handler {
		t.Helper()
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		adapters, err := host.ToolSettingsAdapters()
		if err != nil {
			t.Fatal(err)
		}
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("knowledge receipts")}}, ModuleAdapters: adapters, ApplicationRoutes: host.ToolSettingsSetupRoutes()})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	b := &browser{t: t, handler: open(), cookies: map[string]*http.Cookie{}}
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	grantCollaborationPermissions(t, host, b)
	grant := func(execute bool, denied string) {
		mutateTestRolePermissions(t, host, b, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range prior {
				if !strings.HasPrefix(p.PermissionKey, sdk.ConversationActionPrefix+"libraries_") && !strings.HasPrefix(p.PermissionKey, sdk.ConversationActionPrefix+"documents_") && !strings.HasPrefix(p.PermissionKey, sdk.ConversationToolActionPrefix+"knowledge_") {
					out = append(out, p)
				}
			}
			for _, d := range sdk.ConversationHTTPDefinitions() {
				p := sdk.KnowledgeLibraryPermission(d.Operation)
				if p == nil {
					p = sdk.KnowledgeDocumentPermission(d.Operation)
				}
				if p != nil && d.Operation != denied {
					out = append(out, identity.ProjectRolePermission{PermissionKey: p.Key, DataScope: identity.DataScopeAll})
				}
			}
			if execute {
				for _, d := range append(sdk.LibraryKnowledgeConversationTools(), sdk.KnowledgeExtractionTool()) {
					out = append(out, identity.ProjectRolePermission{PermissionKey: d.ActionKey, DataScope: identity.DataScopeOwner})
				}
			}
			return out
		})
	}
	grant(true, "")
	b.call("POST", "/app/product/tool-settings-setup", "{}", 200)
	library := accountDecode[sdk.KnowledgeLibrary](t, b.call("POST", "/agent/knowledge-libraries", `{"client_id":"receipt-library","kind":"shared","name":"成果来源资料库"}`, 200))
	libraryPath := "/agent/knowledge-libraries/" + library.ID
	b.call("PUT", libraryPath+"/source", `{"datasource_key":"source","expected_revision":1}`, 200)
	req := httptest.NewRequest("POST", "http://127.0.0.1:8091"+libraryPath+"/documents?"+url.Values{"client_id": {"original"}, "filename": {"金额依据.txt"}}.Encode(), strings.NewReader("金额：123.45\n资料编号：R-2026\n"))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Origin", "http://127.0.0.1:8091")
	req.Header.Set("X-Agent-Scope", b.scope)
	for _, c := range b.cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	b.handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	doc := accountDecode[sdk.KnowledgeDocument](t, w)
	docPath := libraryPath + "/documents/" + doc.ID
	for deadline := time.Now().Add(30 * time.Second); doc.State != "ready"; {
		if time.Now().After(deadline) {
			t.Fatalf("index not ready: %+v", doc)
		}
		time.Sleep(20 * time.Millisecond)
		doc = accountDecode[sdk.KnowledgeDocument](t, b.call("GET", docPath, "", 200))
	}
	worker.sourceCalls = []sdk.ConversationToolCall{
		{ID: "knowledge-catalog", Name: "knowledge_libraries", Arguments: `{}`},
		{ID: "knowledge-search", Name: "knowledge_search", Arguments: accountJSON(map[string]string{"library_id": library.ID, "query": "金额"})},
		{ID: "knowledge-read", Name: "knowledge_read", Arguments: accountJSON(map[string]string{"library_id": library.ID, "doc_id": doc.ID})},
		{ID: "knowledge-extract", Name: "knowledge_extract", Arguments: accountJSON(sdk.KnowledgeExtractionArguments{LibraryID: library.ID, DocumentID: doc.ID, KnowledgeExtractionPlan: sdk.KnowledgeExtractionPlan{Fields: []sdk.KnowledgeExtractionField{{KnowledgeExtractionColumn: sdk.KnowledgeExtractionColumn{Key: "amount", Type: "decimal", Required: true}, Pattern: `金额：([0-9.]+)`}}}})},
	}
	recipient := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", `{"client_id":"knowledge-review","name":"资料成果核查","instructions":"阅读并附上准确回执","tools":["knowledge_libraries","knowledge_search","knowledge_read","knowledge_extract","delegation_get","delegation_update"],"skill_keys":[],"model_key":"knowledge-review","enabled":true,"max_concurrent":1}`, 200))
	conversation := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"knowledge-receipts","title":"资料交付阅读"}`, 200))
	d := accountDecode[sdk.ConversationDelegationDetail](t, b.call("POST", "/agent/delegations", accountJSON(sdk.ConversationDelegationCreate{ClientID: "knowledge-work", ConversationID: conversation.ID, AgentID: recipient.ID, Purpose: "核对资料金额", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核对金额 123.45", Deliverable: "资料成果及证据", CompletionConditions: []string{"附四类原始工具回执"}}}), 200))
	path := "/agent/delegations/" + d.ID
	read := func() sdk.ConversationDelegationDetail {
		return accountDecode[sdk.ConversationDelegationDetail](t, b.call("GET", path, "", 200))
	}
	for deadline := time.Now().Add(90 * time.Second); ; {
		d = read()
		root := accountDecode[sdk.Conversation](t, b.call("GET", "/agent/conversations/"+conversation.ID, "", 200))
		if d.Status == "delivered" && d.Task != nil && d.Task.Status == "completed" && root.ActiveRunID == "" {
			break
		}
		if d.Task != nil && d.Task.Status == "failed" {
			t.Fatalf("knowledge fixture failed: %+v", d.Task)
		}
		if time.Now().After(deadline) {
			t.Fatalf("knowledge delivery did not complete: %+v", d)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if d.Delivery == nil || len(d.Delivery.Conditions) != 1 || len(d.Delivery.Conditions[0].Receipts) != 4 {
		t.Fatal("missing original receipts")
	}
	refs := d.Delivery.Conditions[0].Receipts
	// Tool preferences remain binding independently of execution and data ACLs.
	setting := settingList(t, b)["knowledge_extract"]
	b.call("PUT", "/tools/preferences/knowledge_extract", accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	if got := read(); got.Delivery != nil {
		t.Fatal("disabled extraction still exposed delivery")
	}
	setting = settingList(t, b)["knowledge_extract"]
	b.call("PUT", "/tools/preferences/knowledge_extract", accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	grant(false, "")
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read")
	assertRead := func() {
		t.Helper()
		got := read()
		if got.Delivery == nil || !strings.Contains(got.Delivery.Summary, "123.45") || got.Task != nil || len(got.Messages) != 0 {
			t.Fatalf("execution rights affected independent delivery: %+v", got)
		}
		for _, ref := range refs {
			result := readReleasedResult(t, b, d.ID, 0, ref)
			if result.Status != "completed" {
				t.Fatal("original result lost")
			}
		}
	}
	assertRead()
	history := accountDecode[sdk.ConversationDeliveryHistory](t, b.call("GET", path+"/deliveries", "", 200))
	if len(history.Items) == 0 {
		t.Fatal("lost historical delivery")
	}
	for _, ref := range refs {
		readReleasedResult(t, b, d.ID, history.Items[0].Revision, ref)
		run := "/agent/conversations/" + ref.ConversationID + "/runs/" + ref.RunID
		b.call("GET", run, "", 403)
		b.call("POST", run+"/result", accountJSON(sdk.ConversationResultRead{Reference: ref}), 403)
	}
	for _, denied := range []string{"documents_download", "libraries_get", "libraries_list"} {
		grant(false, denied)
		if got := read(); got.Delivery != nil || got.Verification != nil {
			t.Fatalf("source revocation %s kept delivery", denied)
		}
		b.call("POST", path+"/delivery-result", accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: refs[3]}}), 403)
		grant(false, "")
	}
	mu.Lock()
	changedSource = true
	mu.Unlock()
	if got := read(); got.Delivery != nil {
		t.Fatal("changed remote passages kept old delivery")
	}
	mu.Lock()
	changedSource = false
	mu.Unlock()
	assertRead()
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	host = nil
	b.handler = open()
	b.login("admin@example.com", changed)
	assertRead()
	exerciseKnowledgeDeliveryBrowser(t, host, b, options, conversation.ID, d.ID, func(allowed bool) {
		denied := "documents_download"
		if allowed {
			denied = ""
		}
		grant(false, denied)
	})
	mu.Lock()
	count := puts
	mu.Unlock()
	if count != 1 {
		t.Fatalf("delivery reading performed %d document writes", count)
	}
}

func exerciseKnowledgeDeliveryBrowser(t *testing.T, host *Host, b *browser, options Options, source, delegation string, grant func(bool)) {
	t.Helper()
	if os.Getenv("AGENT_KNOWLEDGE_DELIVERY_BROWSER") != "1" {
		return
	}
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Model: "peer", Files: os.DirFS(filepath.Join(project, "frontend/dist"))})
	if err != nil {
		t.Fatal(err)
	}
	type change struct {
		allowed bool
		done    chan struct{}
	}
	changes := make(chan change)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/fixture/knowledge-result-read" {
			c := change{r.URL.Query().Get("allowed") == "true", make(chan struct{})}
			select {
			case changes <- c:
			case <-r.Context().Done():
				return
			}
			select {
			case <-c.done:
				w.WriteHeader(204)
			case <-r.Context().Done():
			}
			return
		}
		ui.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()
	command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/knowledge-delivery.browser.mjs"))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_CONVERSATION="+source, "AGENT_UI_DELEGATION="+delegation)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	for {
		select {
		case c := <-changes:
			grant(c.allowed)
			close(c.done)
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
