package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

func TestKnowledgeDocumentsIdentityHTTPAndPersistentOriginal(t *testing.T) {
	const initial, changed = "Initial-Document-Test!2", "Changed-Document-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "document-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "document-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	var mu sync.Mutex
	type remoteDocument struct {
		filename, body, status string
		exists                 bool
		puts, deletes          int
	}
	documents := map[string]*remoteDocument{}
	holdIndex := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/v1/kb/kbs/project/documents" {
			id := r.URL.Query().Get("doc_id")
			d := documents[id]
			if d == nil {
				d = &remoteDocument{}
				documents[id] = d
			}
			if r.Method == "POST" {
				d.puts++
				d.exists = true
				d.filename = r.URL.Query().Get("filename")
				raw, _ := io.ReadAll(r.Body)
				d.body = string(raw)
				d.status = "INDEXED"
				if holdIndex {
					d.status = "PENDING"
				}
			} else {
				d.deletes++
				d.exists = false
			}
			io.WriteString(w, `{"err_code":0}`)
			return
		}
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		if r.URL.Path == "/v1/kb/search" {
			hits := []any{}
			for id, d := range documents {
				if d.exists && d.status == "INDEXED" {
					hits = append(hits, map[string]any{"doc_id": id, "title": d.filename, "body": d.body})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": hits})
			return
		}
		id, _ := in["doc_id"].(string)
		d := documents[id]
		if d == nil || !d.exists {
			io.WriteString(w, `{"err_code":1004}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"doc_id": id, "status": d.status, "title": d.filename, "body": d.body}})
	}))
	defer upstream.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "documents.db"), RuntimeID: "document-runtime", WorkspaceID: "document-workspace", ApplicationKey: "document-app", Agent: agentmodule.Options{ConversationProvider: libraryKnowledgeWebModel{}, ConversationOptions: agentmodule.ConversationOptions{DocumentPoll: 10 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if host != nil {
			host.Close(context.Background())
		}
	}()
	newBrowser := func() *browser {
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
		return &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	b := newBrowser()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grant := func(denied string) {
		mutateTestRolePermissions(t, host, b, func(prior []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, p := range prior {
				if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationActionPrefix+"libraries_") && !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationActionPrefix+"documents_") && !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationActionPrefix+"attachments_") && !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationToolActionPrefix+"knowledge_") {
					out = append(out, p)
				}
			}
			for _, d := range agentsdk.ConversationHTTPDefinitions() {
				p := agentsdk.KnowledgeLibraryPermission(d.Operation)
				if p == nil {
					p = agentsdk.KnowledgeDocumentPermission(d.Operation)
				}
				if p == nil {
					p = agentsdk.ConversationAttachmentPermission(d.Operation)
				}
				if p != nil && d.Operation != denied {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: p.Key, DataScope: identitysdk.DataScopeAll})
				}
			}
			for _, tool := range agentsdk.LibraryKnowledgeConversationTools() {
				out = append(out, identitysdk.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identitysdk.DataScopeOwner})
			}
			return out
		})
	}
	grant("")
	var lib agentsdk.KnowledgeLibrary
	if err := json.Unmarshal(b.call("POST", "/agent/knowledge-libraries", `{"client_id":"shared","kind":"shared","name":"共享资料"}`, 200).Body.Bytes(), &lib); err != nil {
		t.Fatal(err)
	}
	if lib.DocumentsConfigured {
		t.Fatal("unbound library advertised document management")
	}
	path := "/agent/knowledge-libraries/" + lib.ID + "/documents"
	query := url.Values{"client_id": {"upload"}, "filename": {"资料.txt"}}.Encode()
	raw := []byte("共享资料原件：123.45\n")
	upload := func(q, origin string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "http://127.0.0.1:8091"+path+"?"+q, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-Agent-Scope", b.scope)
		for _, c := range b.cookies {
			req.AddCookie(c)
		}
		response := httptest.NewRecorder()
		b.handler.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("upload: %d want %d: %s", response.Code, want, response.Body.String())
		}
		return response
	}
	upload(query, "http://127.0.0.1:8091", 503) // Library existence alone does not grant remote writes.
	options.Agent.KnowledgeLibraries = []agentmodule.KnowledgeLibraryConfig{{LibraryID: lib.ID, ManageDocuments: true, Knowledge: agentmodule.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture", TeamID: "team", KBID: "project", WorkspaceID: options.WorkspaceID, ResponseMapping: &agentmodule.KnowledgeResponseMapping{Search: &agentmodule.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}, Fetch: &agentmodule.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}}}}}
	reopen := func() {
		t.Helper()
		if err := host.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		host = nil
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		b = newBrowser()
		b.login("admin@example.com", changed)
	}
	reopen()
	if err := json.Unmarshal(b.call("GET", "/agent/knowledge-libraries/"+lib.ID, "", 200).Body.Bytes(), &lib); err != nil || !lib.DocumentsConfigured {
		t.Fatal("managed document capability missing", err)
	}
	upload(query, "http://untrusted.example", 403)
	upload(query+"&permission_ids=all", "http://127.0.0.1:8091", 400)
	grant("documents_upload")
	upload(query, "http://127.0.0.1:8091", 403)
	grant("")
	var doc agentsdk.KnowledgeDocument
	if err := json.Unmarshal(upload(query, "http://127.0.0.1:8091", 200).Body.Bytes(), &doc); err != nil || doc.State != "queued" {
		t.Fatal("upload claimed ready", doc, err)
	}
	statusPath, downloadPath := path+"/"+doc.ID, path+"/"+doc.ID+"/content"
	for deadline := time.Now().Add(5 * time.Second); ; {
		if err := json.Unmarshal(b.call("GET", statusPath, "", 200).Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.State == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("index incomplete: %+v", doc)
		}
		time.Sleep(10 * time.Millisecond)
	}
	download := b.call("GET", downloadPath, "", 200)
	if !bytes.Equal(download.Body.Bytes(), raw) || download.Header().Get("Content-Type") != "application/octet-stream" || download.Header().Get("Cache-Control") != "private, no-store" || download.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("unsafe original download")
	}
	grant("documents_download")
	b.call("GET", downloadPath, "", 403)
	grant("")
	reopen()
	if !bytes.Equal(b.call("GET", downloadPath, "", 200).Body.Bytes(), raw) {
		t.Fatal("restart lost original")
	}
	response := b.call("DELETE", statusPath+"?expected_revision="+fmtRevision(doc.Revision), "", 200)
	if !strings.Contains(response.Body.String(), `"state":"deleting"`) {
		t.Fatal("cleanup incorrectly reported complete")
	}
	b.call("GET", downloadPath, "", 404)
	if os.Getenv("AGENT_TOOL_UI_ACCEPTANCE") == "1" {
		b.call("POST", "/agent/knowledge-libraries", `{"client_id":"personal","kind":"personal","name":"个人资料"}`, 200)
		options.Agent.ConversationOptions.DocumentPoll = 200 * time.Millisecond
		reopen()
		mu.Lock()
		holdIndex = true
		mu.Unlock()
		controls := map[string]func(){
			"restart_document_host":      reopen,
			"revoke_document_download":   func() { grant("documents_download") },
			"revoke_document_list":       func() { grant("documents_list") },
			"revoke_document_import":     func() { grant("documents_import_attachment") },
			"revoke_attachment_download": func() { grant("attachments_download") },
			"restore_documents":          func() { grant("") },
			"index_documents": func() {
				mu.Lock()
				defer mu.Unlock()
				for _, d := range documents {
					if d.exists {
						d.status = "INDEXED"
					}
				}
			},
		}
		servePersonalToolAcceptanceWithHost(t, func() *Host { return host }, options, controls)
		mu.Lock()
		for _, d := range documents {
			if d.puts != 1 || d.deletes != 1 || d.exists {
				t.Errorf("browser lifecycle not fully cleaned or repeated: puts=%d deletes=%d exists=%v", d.puts, d.deletes, d.exists)
			}
		}
		mu.Unlock()
	}
}

func fmtRevision(n int64) string { raw, _ := json.Marshal(n); return string(raw) }
