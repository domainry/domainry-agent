package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	agentinfra "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-orm/query"
)

func TestPrivateAttachmentIndexIdentityHTTPAndFullHostRestart(t *testing.T) {
	runPrivateAttachmentIndexIdentityHTTP(t, nil, "")
}

func runPrivateAttachmentIndexIdentityHTTP(t *testing.T, live *agentmodule.KnowledgeConfig, evidenceDir string) {
	t.Helper()
	const initial, changed = "Initial-Private-Index!2", "Changed-Private-Index!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "private-index-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "private-index-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	var mu sync.Mutex
	remote, permission := "", ""
	var content []byte
	puts, deletes := 0, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/v1/kb/kbs/attachments/documents" {
			if r.Method == "POST" {
				puts++
				remote = r.URL.Query().Get("doc_id")
				content, _ = io.ReadAll(r.Body)
				var ids []string
				_ = json.Unmarshal([]byte(r.Header.Get("X-KB-Permission-Ids")), &ids)
				if len(ids) != 1 || !strings.HasPrefix(ids[0], "scope:agent:attachment:") || r.Header.Get("X-KB-Request-ID") == "" {
					t.Error("private upload context missing")
				} else {
					permission = ids[0]
				}
			} else if r.Method == "DELETE" {
				deletes++
				remote = ""
			} else {
				t.Error("unexpected write")
			}
			io.WriteString(w, `{"err_code":0}`)
			return
		}
		var in struct {
			ID          string   `json:"doc_id"`
			Permissions []string `json:"permission_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if r.URL.Path == "/v1/kb/search" {
			hits := []any{}
			if remote != "" && len(in.Permissions) == 1 && in.Permissions[0] == permission {
				hits = append(hits, map[string]any{"doc_id": remote, "body": "附件检索验收片段，来自 Connector。"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": hits})
			return
		}
		if remote == "" || in.ID != remote || len(in.Permissions) != 1 || in.Permissions[0] != permission {
			io.WriteString(w, `{"err_code":1004}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"doc_id": remote, "status": "INDEXED", "body": "附件检索验收片段，来自 Connector。"}})
	}))
	defer upstream.Close()
	knowledge := agentmodule.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "synthetic", TeamID: "team", KBID: "attachments", WorkspaceID: "private-index-workspace", ResponseMapping: &agentmodule.KnowledgeResponseMapping{Search: &agentmodule.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Excerpt: "/body"}, Fetch: &agentmodule.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Excerpt: "/body"}}}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "private-index.db"), RuntimeID: "private-index-runtime", WorkspaceID: knowledge.WorkspaceID, ApplicationKey: "private-index-app", Agent: agentmodule.Options{ConversationProvider: privateAttachmentWebModel{}, AttachmentKnowledge: []agentmodule.KnowledgeConfig{knowledge}, ConversationOptions: agentmodule.ConversationOptions{DocumentPoll: 10 * time.Millisecond}}}
	var liveAudit *attachmentLiveAudit
	indexWait, cleanupWait, poll := 5*time.Second, 5*time.Second, 10*time.Millisecond
	if live != nil {
		if live.BaseURL == "" {
			*live = knowledge
		}
		var err error
		liveAudit, err = newAttachmentLiveAudit(*live, evidenceDir)
		if err != nil {
			t.Fatal(err)
		}
		knowledge = *live
		knowledge.WorkspaceID = options.WorkspaceID
		knowledge.Transport = liveAudit
		options.Agent.AttachmentKnowledge = []agentmodule.KnowledgeConfig{knowledge}
		options.DatabasePath = filepath.Join(evidenceDir, "attachment-live.db")
		options.Agent.ConversationOptions.DocumentPoll = time.Second
		indexWait, cleanupWait, poll = 2*time.Minute, time.Minute, 250*time.Millisecond
		defer func() {
			if t.Failed() {
				if err := liveAudit.cleanupFailedFixture(); err != nil {
					t.Error("fixture cleanup unresolved; retain manifest", err)
				}
			}
			if err := liveAudit.save(); err != nil {
				t.Error(err)
			}
		}()
	}
	var host *Host
	var b *browser
	open := func() {
		t.Helper()
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
		b = &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	open()
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grant := func(denied string) {
		mutateTestRolePermissions(t, host, b, func(prior []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, p := range prior {
				if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationActionPrefix+"attachments_") && p.PermissionKey != agentsdk.ConversationToolActionPrefix+"attachment_search" && p.PermissionKey != agentsdk.ConversationToolActionPrefix+"attachment_read" {
					out = append(out, p)
				}
			}
			for _, d := range agentsdk.ConversationHTTPDefinitions() {
				if p := agentsdk.ConversationAttachmentPermission(d.Operation); p != nil && d.Operation != denied {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: p.Key, DataScope: identitysdk.DataScopeOwner})
				}
			}
			for _, d := range agentsdk.AttachmentConversationTools() {
				if d.Key != denied {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: d.ActionKey, DataScope: identitysdk.DataScopeOwner})
				}
			}
			return out
		})
	}
	grant("attachments_index")
	var c agentsdk.Conversation
	if err := json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"private-index-http","title":"附件引用验收"}`, 200).Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	path := "/agent/conversations/" + c.ID + "/attachments"
	raw, err := os.ReadFile("../../../integration/testdata/file-preview/two-page.pdf")
	if err != nil {
		t.Fatal(err)
	}
	filename := "private.pdf"
	if liveAudit != nil {
		filename = "private-attachment-acceptance.md"
		raw = []byte("# 附件检索验收\n\n附件检索验收片段，来自 Connector。这是合成私有附件，不包含用户资料。验收编号：" + c.ID + "。合成规则：收到发票后 30 天付款。\n")
		liveAudit.expectOriginal(raw)
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1:8091"+path+"?client_id=upload&filename="+filename, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Origin", "http://127.0.0.1:8091")
	req.Header.Set("X-Agent-Scope", b.scope)
	for _, cookie := range b.cookies {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	b.handler.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatal("original upload failed", response.Code, response.Body.String())
	}
	var att agentsdk.ConversationAttachment
	if err := json.Unmarshal(response.Body.Bytes(), &att); err != nil {
		t.Fatal(err)
	}
	var otherConversation agentsdk.Conversation
	if err := json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"private-index-other-conversation","title":"隔离对照"}`, 200).Body.Bytes(), &otherConversation); err != nil {
		t.Fatal(err)
	}
	b.call("GET", "/agent/conversations/"+otherConversation.ID+"/attachments/"+att.ID+"/content", "", 404)
	otherUser := &browser{t: t, handler: b.handler, cookies: map[string]*http.Cookie{}}
	otherUser.login("system_administrator@example.com", initial)
	otherUser.changePassword(initial, changed)
	mutateTestRolePermissions(t, host, b, func(prior []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		out := []identitysdk.ProjectRolePermission{}
		for _, p := range prior {
			if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationActionPrefix+"attachments_") {
				out = append(out, p)
			}
		}
		for _, d := range agentsdk.ConversationHTTPDefinitions() {
			if p := agentsdk.ConversationAttachmentPermission(d.Operation); p != nil {
				out = append(out, identitysdk.ProjectRolePermission{PermissionKey: p.Key, DataScope: identitysdk.DataScopeOwner})
			}
		}
		return out
	}, otherUser.readSession()["user_id"].(string))
	otherUser.call("GET", path+"/"+att.ID+"/content", "", 404)
	if liveAudit != nil {
		liveAudit.markOwnerIsolation()
	}
	indexPath := fmt.Sprintf("%s/%s/index?expected_revision=%d", path, att.ID, att.Revision)
	if att.Indexing == nil || att.Indexing.CanStart || att.Indexing.Requested || att.Indexing.Reason != "attachment_index_access_denied" {
		t.Fatal("upload capability ignored live permission", att.Indexing)
	}
	b.call("POST", indexPath, "", 403)
	grant("")
	var stored agentsdk.ConversationAttachment
	if err := json.Unmarshal(b.call("GET", path+"/"+att.ID, "", 200).Body.Bytes(), &stored); err != nil || stored.Indexing == nil || !stored.Indexing.CanStart || stored.Indexing.MaxBytes != 10*1024*1024 {
		t.Fatal("missing start capability or source size limit", stored.Indexing, err)
	}
	checkPath := fmt.Sprintf("%s/%s/index/check?expected_revision=%d", path, att.ID, att.Revision)
	b.call("POST", checkPath, "", 409)
	b.call("POST", indexPath+"&permission_ids=public", "", 400)
	b.call("POST", indexPath+"&expected_revision=2", "", 400)
	b.call("POST", indexPath, `{"permission_ids":["public"],"user_id":"other"}`, 400)
	b.call("POST", indexPath, "", 200)
	deadline := time.Now().Add(indexWait)
	for time.Now().Before(deadline) {
		if err := json.Unmarshal(b.call("GET", path+"/"+att.ID, "", 200).Body.Bytes(), &att); err != nil {
			t.Fatal(err)
		}
		if att.State == "ready" {
			break
		}
		time.Sleep(poll)
	}
	if att.State != "ready" {
		t.Fatal("private index not ready", att)
	}
	if liveAudit != nil {
		if err := liveAudit.verifyPrivateVisibility(t.Context(), att, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	checkRevision := att.Revision
	checkPath = fmt.Sprintf("%s/%s/index/check?expected_revision=%d", path, att.ID, checkRevision)
	grant("attachments_check_index")
	b.call("POST", checkPath, "", 403)
	var denied agentsdk.ConversationAttachment
	if err := json.Unmarshal(b.call("GET", path+"/"+att.ID, "", 200).Body.Bytes(), &denied); err != nil || denied.Indexing == nil || denied.Indexing.CanCheck || denied.Indexing.Reason != "attachment_index_check_denied" {
		t.Fatal("check permission not reflected", err)
	}
	grant("")
	for _, suffix := range []string{"&permission_ids=public", "&expected_revision=2", "&user_id=other"} {
		b.call("POST", checkPath+suffix, "", 400)
	}
	b.call("POST", checkPath, `{"reset":true}`, 400)
	var checked agentsdk.ConversationAttachment
	if err := json.Unmarshal(b.call("POST", checkPath, "", 200).Body.Bytes(), &checked); err != nil || checked.Indexing == nil || !checked.Indexing.CanCheck || checked.Indexing.LastCheckRevision != checkRevision || checked.State != "ready" {
		t.Fatal("check response incomplete", checked, err)
	}
	var run agentsdk.ConversationRun
	if err := json.Unmarshal(b.call("POST", "/agent/conversations/"+c.ID+"/messages", `{"client_message_id":"retrieve","message":"检索当前会话附件并引用"}`, 202).Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(30 * time.Second)
	for {
		if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+c.ID+"/runs/"+run.ID, "", 200).Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		if run.Terminal() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retrieval did not finish: %+v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.Status != "completed" || len(run.Steps) != 3 || len(run.Steps[1].Calls[0].Citations) != 1 || run.Steps[1].Calls[0].Citations[0].ConversationID != c.ID {
		t.Fatalf("private retrieval not cited: %+v", run)
	}
	assertSource := func(readable bool) {
		t.Helper()
		body := b.call("GET", "/agent/conversations/"+c.ID+"/messages", "", 200).Body.String()
		if strings.Contains(body, "附件检索验收片段") != readable || strings.Contains(body, `"citations":[{`) != readable {
			t.Fatal("private citation visibility mismatch", body)
		}
	}
	assertSource(true)
	grant("attachment_read")
	assertSource(false)
	grant("")
	assertSource(true)
	grant("attachments_download")
	assertSource(false)
	b.call("GET", path+"/"+att.ID+"/content", "", 403)
	grant("")
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	host = nil
	open()
	b.login("admin@example.com", changed)
	assertSource(true)
	if err := json.Unmarshal(b.call("POST", checkPath, "", 200).Body.Bytes(), &checked); err != nil || checked.Indexing == nil || checked.Indexing.LastCheckRevision != checkRevision || checked.State != "ready" {
		t.Fatal("check receipt or ready state lost on restart", checked, err)
	}
	b.call("POST", indexPath, "", 200) // Same initial revision recovers its durable receipt.
	if got := b.call("GET", path+"/"+att.ID+"/content", "", 200).Body.Bytes(); !bytes.Equal(got, raw) {
		t.Fatal("restart changed original")
	}
	if liveAudit == nil && os.Getenv("AGENT_ATTACHMENT_RETRIEVAL_BROWSER") == "1" {
		runPrivateAttachmentCitationBrowser(t, host, options, c.ID, att.ID, raw, grant)
	}
	if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+c.ID, "", 200).Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	b.call("DELETE", fmt.Sprintf("/agent/conversations/%s?expected_revision=%d", c.ID, c.Revision), "", 200)
	b.call("GET", path+"/"+att.ID+"/content", "", 404)
	// Read-only repository inspection verifies cleanup independently of HTTP.
	renderer, err := agentinfra.Renderer("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	statement, args, err := query.NewSelectBuilder(renderer, "_agent_conversation_attachments").Columns("payload_json").Where(query.Equal("attachment_id", att.ID)).Build()
	if err != nil {
		t.Fatal(err)
	}
	var record persistence.ConversationAttachmentRecord
	deadline = time.Now().Add(cleanupWait)
	for time.Now().Before(deadline) {
		var saved []byte
		if err = host.db.QueryRowContext(t.Context(), statement, args...).Scan(&saved); err != nil {
			t.Fatal(err)
		}
		record = persistence.ConversationAttachmentRecord{}
		if err = json.Unmarshal(saved, &record); err != nil {
			t.Fatal(err)
		}
		if record.Attachment.State == "deleted" {
			break
		}
		time.Sleep(poll)
	}
	if record.Attachment.State != "deleted" || record.BodyRef != "" {
		t.Fatal("durable cleanup incomplete", record)
	}
	remaining, err := filepath.Glob(filepath.Join(options.DatabasePath+".attachments", "*", "*.bin"))
	if err != nil || len(remaining) != 0 {
		t.Fatal("physical original remains", remaining, err)
	}
	if liveAudit != nil {
		if !record.Index.IndexObserved || record.Index.PutAcknowledged || !record.Index.DeleteAcknowledged {
			t.Fatal("real lost-upload recovery or deletion receipt not preserved")
		}
		if err := liveAudit.verifyCleanup(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		return
	}
	mu.Lock()
	same := bytes.Equal(content, raw)
	mu.Unlock()
	if !same {
		t.Fatal("Connector received altered original")
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := deletes == 1
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if puts != 1 || deletes != 1 {
		t.Fatal("remote lifecycle repeated or missing", puts, deletes)
	}
}
