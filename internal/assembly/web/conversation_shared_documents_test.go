package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
)

type sharedDocumentReaderModel struct{ peerWebModel }

func (m *sharedDocumentReaderModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	m.mu.Lock()
	m.requests = append(m.requests, in)
	m.mu.Unlock()
	var ref *sdk.ConversationDocumentReference
	var received sdk.ConversationAgentMessage
	read := false
	for _, message := range in.Messages {
		if message.Role == "tool" && message.ToolCallID == "shared-document-reply" {
			var result sdk.ConversationToolResult
			if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
				return sdk.ConversationStepResult{}, err
			}
			if result.Status != "completed" {
				return sdk.ConversationStepResult{}, fmt.Errorf("shared-message fixture rejected: %s %s", result.Status, result.ErrorCode)
			}
			return sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "已阅读明确共享的资料。"}}, nil
		}
		if message.Role == "tool" && message.ToolCallID == "shared-document-read" {
			read = true
		}
		if !strings.Contains(message.Content, "Explicit Knowledge file references") {
			continue
		}
		if start := strings.Index(message.Content, "{"); start >= 0 {
			var peer sdk.ConversationAgentMessage
			if json.Unmarshal([]byte(message.Content[start:]), &peer) == nil && len(peer.Documents) > 0 {
				received = peer
				item := peer.Documents[0]
				ref = &item
			}
		}
	}
	if read && ref != nil {
		send := map[string]any{"to_agent_id": "default", "content": "Agent 已核对这份明确共享的资料引用。", "brief_version": received.BriefVersion, "agreement_revision": received.AgreementRevision, "documents": []sdk.ConversationDocumentReference{*ref}}
		return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: "shared-document-reply", Name: "agent_message", Arguments: accountJSON(map[string]any{"id": received.DelegationID, "message": send})}}}}, nil
	}
	if ref != nil {
		return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: "shared-document-read", Name: "knowledge_read", Arguments: accountJSON(map[string]any{"library_id": ref.LibraryID, "doc_id": ref.DocumentID})}}}}, nil
	}
	return sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "等待明确共享的资料。"}}, nil
}

func TestPeerSharedDocumentsUseExplicitKnowledgeCopyAndCurrentReadPermission(t *testing.T) {
	const initial, changed = "Shared-Document-Initial!26", "Shared-Document-Changed!26"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "shared-document-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "shared-document-test-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	var mu sync.Mutex
	type file struct {
		name, body string
		exists     bool
	}
	files := map[string]file{}
	puts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/documents") {
			id := r.URL.Query().Get("doc_id")
			if r.Method == "POST" {
				data, _ := io.ReadAll(r.Body)
				files[id] = file{r.URL.Query().Get("filename"), string(data), true}
				puts++
			} else {
				delete(files, id)
			}
			io.WriteString(w, `{"err_code":0}`)
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		id, _ := in["doc_id"].(string)
		f := files[id]
		if !f.exists {
			io.WriteString(w, `{"err_code":1004}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"doc_id": id, "status": "INDEXED", "title": f.name, "body": f.body}})
	}))
	defer upstream.Close()
	reader := &sharedDocumentReaderModel{peerWebModel: peerWebModel{modelKey: "reader"}}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "shared.db"), RuntimeID: "shared-runtime", WorkspaceID: "shared-workspace", ApplicationKey: "shared-app", Agent: agentmodule.Options{ConversationProvider: &peerWebModel{}, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"reader": reader}, Poll: 5 * time.Millisecond, DocumentPoll: 10 * time.Millisecond}}}
	mapping := &agentmodule.KnowledgeResponseMapping{Search: &agentmodule.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}, Fetch: &agentmodule.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}}
	options.Agent.KnowledgeDatasources = []agentmodule.KnowledgeDatasourceConfig{{Key: "source", Name: "资料源", Knowledge: agentmodule.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture", TeamID: "team", KBID: "project", WorkspaceID: options.WorkspaceID, ResponseMapping: mapping}}}
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
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
		return &browser{t: t, handler: h, cookies: map[string]*http.Cookie{}}
	}
	b := newBrowser()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	grantCollaborationPermissions(t, host, b)
	grant := func(denied string) {
		mutateTestRolePermissions(t, host, b, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range prior {
				if !strings.HasPrefix(p.PermissionKey, sdk.ConversationActionPrefix+"libraries_") && !strings.HasPrefix(p.PermissionKey, sdk.ConversationActionPrefix+"documents_") && !strings.HasPrefix(p.PermissionKey, sdk.ConversationActionPrefix+"attachments_") && !strings.HasPrefix(p.PermissionKey, sdk.ConversationToolActionPrefix+"knowledge_") {
					out = append(out, p)
				}
			}
			for _, d := range sdk.ConversationHTTPDefinitions() {
				p := sdk.KnowledgeLibraryPermission(d.Operation)
				if p == nil {
					p = sdk.KnowledgeDocumentPermission(d.Operation)
				}
				if p == nil {
					p = sdk.ConversationAttachmentPermission(d.Operation)
				}
				if p != nil && d.Operation != denied {
					out = append(out, identity.ProjectRolePermission{PermissionKey: p.Key, DataScope: identity.DataScopeAll})
				}
			}
			for _, d := range sdk.LibraryKnowledgeConversationTools() {
				out = append(out, identity.ProjectRolePermission{PermissionKey: d.ActionKey, DataScope: identity.DataScopeOwner})
			}
			return out
		})
	}
	grant("")
	source := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"sharing-source","title":"明确共享资料"}`, 200))
	raw := "共享资料原件：已核对 8 家供应商。\n"
	attachmentPath := "/agent/conversations/" + source.ID + "/attachments"
	req := httptest.NewRequest("POST", "http://127.0.0.1:8091"+attachmentPath+"?"+url.Values{"client_id": {"private-file"}, "filename": {"共享资料.txt"}}.Encode(), strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Origin", "http://127.0.0.1:8091")
	req.Header.Set("X-Agent-Scope", b.scope)
	for _, cookie := range b.cookies {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	b.handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("attachment upload: %d %s", w.Code, w.Body.String())
	}
	attachment := accountDecode[sdk.ConversationAttachment](t, w)
	library := accountDecode[sdk.KnowledgeLibrary](t, b.call("POST", "/agent/knowledge-libraries", `{"client_id":"shared","kind":"shared","name":"委派共享资料库"}`, 200))
	libraryPath := "/agent/knowledge-libraries/" + library.ID
	b.call("PUT", libraryPath+"/source", `{"datasource_key":"source","expected_revision":1}`, 200)
	importBody := accountJSON(sdk.KnowledgeAttachmentImport{ClientID: "explicit-copy", ConversationID: source.ID, AttachmentID: attachment.ID, ExpectedRevision: attachment.Revision})
	doc := accountDecode[sdk.KnowledgeDocument](t, b.call("POST", libraryPath+"/documents/from-attachment", importBody, 200))
	if replay := accountDecode[sdk.KnowledgeDocument](t, b.call("POST", libraryPath+"/documents/from-attachment", importBody, 200)); replay.ID != doc.ID {
		t.Fatal("import repeated copied file")
	}
	docPath := libraryPath + "/documents/" + doc.ID
	deadline := time.Now().Add(30 * time.Second)
	for doc.State != "ready" {
		if time.Now().After(deadline) {
			t.Fatalf("document failed to index: %+v", doc)
		}
		time.Sleep(20 * time.Millisecond)
		doc = accountDecode[sdk.KnowledgeDocument](t, b.call("GET", docPath, "", 200))
	}
	// The source stays private; deleting it does not grant access or revoke an
	// explicitly created, independently owned library copy.
	if attachment.Visibility != "conversation_private" || doc.SHA256 != attachment.SHA256 {
		t.Fatal("private attachment ownership changed")
	}
	b.call("DELETE", attachmentPath+"/"+attachment.ID+"?expected_revision="+fmtRevision(attachment.Revision), "", 200)
	if got := b.call("GET", docPath+"/content", "", 200).Body.String(); got != raw {
		t.Fatal("copy depended on deleted attachment")
	}
	agent := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", `{"client_id":"reader","name":"资料核查员","instructions":"阅读明确共享的资料","tools":["knowledge_read","agent_message"],"skill_keys":[],"model_key":"reader","enabled":true,"max_concurrent":1}`, 200))
	d := accountDecode[sdk.ConversationDelegationDetail](t, b.call("POST", "/agent/delegations", accountJSON(sdk.ConversationDelegationCreate{ClientID: "shared-task", ConversationID: source.ID, AgentID: agent.ID, Purpose: "明确引用资料", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "阅读共享资料", Deliverable: "核查结果", CompletionConditions: []string{"核对原件"}}}), 200))
	path := "/agent/delegations/" + d.ID
	ref := sdk.ConversationDocumentReference{LibraryID: library.ID, DocumentID: doc.ID, Revision: doc.Revision, SHA256: doc.SHA256}
	in := sdk.ConversationAgentMessageSend{ClientID: "explicit-reference", ToAgentID: d.ToAgentID, Content: "请核对这份明确共享的资料。", BriefVersion: 1, AgreementRevision: 1, Documents: []sdk.ConversationDocumentReference{ref}}
	setTestCollaborationPermissions(t, host, b, "view", "communicate", "receive", "execution_read")
	b.call("POST", path+"/messages", accountJSON(in), 403)
	setTestCollaborationPermissions(t, host, b, sdk.ConversationCollaborationOperations()...)
	grant("documents_download")
	b.call("POST", path+"/messages", accountJSON(in), 403)
	grant("")
	invalid := in
	invalid.Documents = []sdk.ConversationDocumentReference{ref}
	invalid.Documents[0].Revision++
	b.call("POST", path+"/messages", accountJSON(invalid), 409)
	invalid.Documents[0] = ref
	invalid.Documents[0].DocumentID = attachment.ID
	b.call("POST", path+"/messages", accountJSON(invalid), 400)
	message := accountDecode[sdk.ConversationAgentMessage](t, b.call("POST", path+"/messages", accountJSON(in), 200))
	if message.FromUserID == "" || !reflect.DeepEqual(message.Documents, in.Documents) {
		t.Fatal("lost actor or exact reference")
	}
	if replay := accountDecode[sdk.ConversationAgentMessage](t, b.call("POST", path+"/messages", accountJSON(in), 200)); replay.ID != message.ID {
		t.Fatal("send repeated message")
	}
	deadline = time.Now().Add(60 * time.Second)
	for {
		d = accountDecode[sdk.ConversationDelegationDetail](t, b.call("GET", path, "", 200))
		consumed := false
		for _, m := range d.Messages {
			consumed = consumed || m.ID == message.ID && m.ConsumedByRunID != ""
		}
		if consumed && d.Task != nil && d.Task.Status == "completed" {
			break
		}
		if d.Task != nil && d.Task.Status == "failed" {
			t.Fatalf("shared reference execution failed: %+v", d.Task)
		}
		if time.Now().After(deadline) {
			t.Fatalf("shared message was not consumed: %+v", d)
		}
		time.Sleep(20 * time.Millisecond)
	}
	agentShared := false
	for _, m := range d.Messages {
		if m.FromAgentID == d.ToAgentID && m.ToAgentID == d.FromAgentID && reflect.DeepEqual(m.Documents, in.Documents) && m.Source != nil {
			agentShared = true
		}
	}
	if !agentShared {
		t.Fatal("Agent did not return exact reference through actual agent_message tool")
	}
	reader.mu.Lock()
	requests := append([]sdk.ConversationStepRequest(nil), reader.requests...)
	reader.mu.Unlock()
	readOriginal := false
	for _, request := range requests {
		for _, m := range request.Messages {
			if m.Role == "tool" && m.ToolCallID == "shared-document-read" && strings.Contains(m.Content, "8 家供应商") {
				readOriginal = true
			}
		}
	}
	if !readOriginal {
		t.Fatal("recipient never read original through Knowledge tool")
	}
	// A source revocation clears references and associated communication text,
	// and blocks original execution history; the metadata view remains usable.
	grant("documents_download")
	setTestCollaborationPermissions(t, host, b, "view", "communicate", "share")
	current := accountDecode[sdk.ConversationDelegationDetail](t, b.call("GET", path, "", 200))
	for _, m := range current.Messages {
		if m.ID == message.ID && (!m.DocumentsOmitted || len(m.Documents) != 0 || strings.Contains(m.Content, "请核对这份")) {
			t.Fatal("revoked reference remained visible")
		}
	}
	b.call("POST", path+"/messages", accountJSON(in), 403)
	grant("")
	setTestCollaborationPermissions(t, host, b, sdk.ConversationCollaborationOperations()...)
	if err = host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	host = nil
	host, err = Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	b = newBrowser()
	b.login("admin@example.com", changed)
	current = accountDecode[sdk.ConversationDelegationDetail](t, b.call("GET", path, "", 200))
	found := false
	for _, m := range current.Messages {
		if m.ID == message.ID {
			found = reflect.DeepEqual(m.Documents, in.Documents) && m.ConsumedByRunID != ""
		}
	}
	if !found {
		t.Fatal("restart lost consumed exact shared reference")
	}
	if got := b.call("GET", docPath+"/content", "", 200).Body.String(); got != raw {
		t.Fatal("restart lost independently shared original")
	}
	exercisePeerSharedDocumentBrowser(t, host, b, options, source.ID, d.ID, doc, grant)
	mu.Lock()
	count := puts
	mu.Unlock()
	if count != 1 {
		t.Fatalf("sharing repeated original import/index: %d writes", count)
	}
}

func exercisePeerSharedDocumentBrowser(t *testing.T, host *Host, b *browser, options Options, source, delegation string, doc sdk.KnowledgeDocument, grant func(string)) {
	t.Helper()
	if os.Getenv("AGENT_SHARED_DOCUMENT_BROWSER") != "1" {
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
		profile string
		done    chan struct{}
	}
	changes := make(chan change)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/fixture/shared-documents" {
			c := change{r.URL.Query().Get("profile"), make(chan struct{})}
			if c.profile != "all" && c.profile != "no-share" && c.profile != "no-read" {
				w.WriteHeader(400)
				return
			}
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
	command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/shared-documents.browser.mjs"))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_CONVERSATION="+source, "AGENT_UI_DELEGATION="+delegation, "AGENT_UI_DOCUMENT="+doc.ID)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	for {
		select {
		case c := <-changes:
			operations := sdk.ConversationCollaborationOperations()
			if c.profile == "no-share" {
				operations = nil
				for _, op := range sdk.ConversationCollaborationOperations() {
					if op != "share" {
						operations = append(operations, op)
					}
				}
			}
			if c.profile == "no-read" {
				operations = []string{"view", "communicate", "share"}
				grant("documents_download")
			} else {
				grant("")
			}
			setTestCollaborationPermissions(t, host, b, operations...)
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
