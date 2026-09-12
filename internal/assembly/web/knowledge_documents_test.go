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
	testKnowledgeDocumentsIdentityHTTP(t, false, false)
}
func TestKnowledgeDatasourcesIdentityHTTPAndPersistentOriginal(t *testing.T) {
	testKnowledgeDocumentsIdentityHTTP(t, true, false)
}
func testKnowledgeDocumentsIdentityHTTP(t *testing.T, dynamic, extract bool) {
	const initial, changed = "Initial-Document-Test!2", "Changed-Document-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "document-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "document-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	var mu sync.Mutex
	type remoteDocument struct {
		filename, body, status string
		kb                     string
		exists                 bool
		puts, deletes          int
	}
	documents := map[string]*remoteDocument{}
	holdIndex := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/v1/kb/kbs/") && strings.HasSuffix(r.URL.Path, "/documents") {
			id := r.URL.Query().Get("doc_id")
			d := documents[id]
			if d == nil {
				d = &remoteDocument{}
				documents[id] = d
			}
			if r.Method == "POST" {
				d.puts++
				d.kb = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/kb/kbs/"), "/documents")
				if dynamic && (r.Header.Get("X-KB-Permission-Ids") == "" || r.Header.Get("X-KB-Request-ID") == "") {
					t.Error("dynamic source push omitted private policy or request ID")
				}
				d.exists = true
				d.filename = r.URL.Query().Get("filename")
				raw, _ := io.ReadAll(r.Body)
				d.body = string(raw)
				if strings.HasSuffix(d.filename, ".pdf") || strings.HasSuffix(d.filename, ".docx") {
					// Explicit knowledge-service fixture output; never interpret binary PDF bytes as text.
					d.body = knowledgeExtractionBrowserText
				}
				if extract && strings.HasSuffix(d.filename, ".xlsx") {
					// Connector search summary; original bytes are only displayed by the browser.
					d.body = "Synthetic Invoice: 9 rows x 1 column; no cell values"
				}
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
				if d.exists && d.status == "INDEXED" && d.kb == in["kb_id"] {
					hits = append(hits, map[string]any{"doc_id": id, "title": d.filename, "body": d.body})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": hits})
			return
		}
		id, _ := in["doc_id"].(string)
		d := documents[id]
		if d == nil || !d.exists || d.kb != in["kb_id"] {
			io.WriteString(w, `{"err_code":1004}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"doc_id": id, "status": d.status, "title": d.filename, "body": d.body}})
	}))
	defer upstream.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "documents.db"), RuntimeID: "document-runtime", WorkspaceID: "document-workspace", ApplicationKey: "document-app", Agent: agentmodule.Options{ConversationProvider: libraryKnowledgeWebModel{}, ConversationOptions: agentmodule.ConversationOptions{DocumentPoll: 10 * time.Millisecond}}}

	if extract {
		options.Agent.ConversationProvider = knowledgeExtractionWebModel{}
	}

	mapping := &agentmodule.KnowledgeResponseMapping{Search: &agentmodule.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}, Fetch: &agentmodule.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}}
	knowledge := agentmodule.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture-secret-not-for-browser", TeamID: "team", KBID: "project", WorkspaceID: options.WorkspaceID, ResponseMapping: mapping}
	if dynamic {
		for _, key := range []string{"first", "second", "third"} {
			config := knowledge
			if key != "first" {
				config.KBID = key
			}
			options.Agent.KnowledgeDatasources = append(options.Agent.KnowledgeDatasources, agentmodule.KnowledgeDatasourceConfig{Key: key, Name: "知识源 " + key, Knowledge: config})
		}
	}
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
			tools := agentsdk.LibraryKnowledgeConversationTools()
			if extract {
				tools = append(tools, agentsdk.KnowledgeExtractionTool())
			}
			for _, tool := range tools {
				if tool.Key == denied {
					continue
				}
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
	if !dynamic {
		options.Agent.KnowledgeLibraries = []agentmodule.KnowledgeLibraryConfig{{LibraryID: lib.ID, ManageDocuments: true, Knowledge: knowledge}}
	}

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

	if dynamic {
		sourcePath := "/agent/knowledge-libraries/" + lib.ID + "/sources"
		bindPath := "/agent/knowledge-libraries/" + lib.ID + "/source"
		var page agentsdk.KnowledgeLibrarySources
		readSources := func() {
			t.Helper()
			if err := json.Unmarshal(b.call("GET", sourcePath+"?limit=1", "", 200).Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
		}
		readSources()
		if !lib.SourcesManageable || page.Status != "unbound" || !page.CanBind || len(page.Items) != 1 || !page.Items[0].Available || page.NextAfter != "first" || page.Complete {
			t.Fatal("unbound capabilities or pagination", page)
		}
		rawPage := b.call("GET", sourcePath+"?after=first&limit=1", "", 200).Body.String()
		if !strings.Contains(rawPage, `"key":"second"`) || strings.Contains(rawPage, knowledge.APIKey) || strings.Contains(rawPage, upstream.URL) {
			t.Fatal("invalid source projection")
		}
		b.call("GET", sourcePath+"?permission_ids=all", "", 400)
		b.call("GET", sourcePath+"?limit=1&limit=2", "", 400)
		b.call("PUT", bindPath+"?permission_ids=all", `{"datasource_key":"first","expected_revision":1}`, 400)
		b.call("PUT", bindPath, `{"datasource_key":"first","expected_revision":1,"permission_ids":["all"]}`, 400)
		grant("libraries_bind_source")
		readSources()
		if page.CanBind || page.Items[0].Available {
			t.Fatal("Identity denied but source offered")
		}
		b.call("PUT", bindPath, `{"datasource_key":"first","expected_revision":1}`, 403)
		grant("libraries_sources")
		b.call("GET", sourcePath, "", 403)
		grant("")
		b.call("PUT", bindPath, `{"datasource_key":"unknown","expected_revision":1}`, 404)
		b.call("PUT", bindPath, `{"datasource_key":"first","expected_revision":99}`, 409)
		readSources()
		if !page.Items[0].Available {
			t.Fatal("failed binding consumed source")
		}
		input := `{"datasource_key":"first","expected_revision":` + fmtRevision(lib.Revision) + `}`
		if err := json.Unmarshal(b.call("PUT", bindPath, input, 200).Body.Bytes(), &lib); err != nil || !lib.DocumentsConfigured || lib.DatasourceKey != "first" {
			t.Fatal("binding did not enable upload immediately", err)
		}
		revision := lib.Revision
		if err := json.Unmarshal(b.call("PUT", bindPath, input, 200).Body.Bytes(), &lib); err != nil || lib.Revision != revision {
			t.Fatal("binding replay repeated mutation", err)
		}
		b.call("PUT", bindPath, `{"datasource_key":"second","expected_revision":`+fmtRevision(lib.Revision)+`}`, 409)
		readSources()
		if page.Status != "connected" || page.Items[0].Available {
			t.Fatal("binding not visible", page)
		}
	} else {
		reopen()
	}
	if err := json.Unmarshal(b.call("GET", "/agent/knowledge-libraries/"+lib.ID, "", 200).Body.Bytes(), &lib); err != nil || !lib.DocumentsConfigured || lib.DocumentMaxBytes != 10<<20 {
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

	if dynamic {
		originalSources := options.Agent.KnowledgeDatasources
		assertUnavailable := func() {
			t.Helper()
			reopen()
			var current agentsdk.KnowledgeLibrary
			if e := json.Unmarshal(b.call("GET", "/agent/knowledge-libraries/"+lib.ID, "", 200).Body.Bytes(), &current); e != nil || current.DocumentsConfigured || current.KnowledgeConfigured || current.DatasourceKey != "first" {
				t.Fatal("changed source remained active", e)
			}
			sourcePage := b.call("GET", "/agent/knowledge-libraries/"+lib.ID+"/sources", "", 200).Body.String()
			if !strings.Contains(sourcePage, `"status":"unavailable"`) {
				t.Fatal("unavailable source not explained")
			}
			if !bytes.Equal(b.call("GET", downloadPath, "", 200).Body.Bytes(), raw) {
				t.Fatal("configuration change lost original")
			}
		}
		options.Agent.KnowledgeDatasources = nil
		assertUnavailable()
		options.Agent.KnowledgeLibraries = []agentmodule.KnowledgeLibraryConfig{{LibraryID: lib.ID, Knowledge: knowledge, ManageDocuments: true}}
		assertUnavailable() // Startup configuration cannot adopt a durable user binding.
		options.Agent.KnowledgeLibraries = nil
		changedSources := append([]agentmodule.KnowledgeDatasourceConfig(nil), originalSources...)
		changedSources[0].Knowledge.KBID = "changed-physical-kb"
		options.Agent.KnowledgeDatasources = changedSources
		assertUnavailable()
		changedSources[0] = originalSources[0]
		changedSources[0].PermissionIDs = []string{"changed-policy"}
		assertUnavailable()
		options.Agent.KnowledgeDatasources = originalSources
		reopen()
		if e := json.Unmarshal(b.call("GET", "/agent/knowledge-libraries/"+lib.ID, "", 200).Body.Bytes(), &lib); e != nil || !lib.DocumentsConfigured {
			t.Fatal("restored config did not recover persisted binding", e)
		}
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
			"revoke_extract":             func() { grant("knowledge_extract") },
			"revoke_document_download":   func() { grant("documents_download") },
			"revoke_document_list":       func() { grant("documents_list") },
			"revoke_document_import":     func() { grant("documents_import_attachment") },
			"revoke_document_transfer":   func() { grant("documents_transfer") },
			"revoke_attachment_download": func() { grant("attachments_download") },
			"restore_documents":          func() { grant("") },
			"resume_document_indexing": func() {
				mu.Lock()
				defer mu.Unlock()
				holdIndex = false
				for _, d := range documents {
					if d.exists {
						d.status = "INDEXED"
					}
				}
			},
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

		if dynamic {
			b.call("POST", "/agent/knowledge-libraries", `{"client_id":"browser-unbound","kind":"shared","name":"待连接共享资料"}`, 200)
			catalog := options.Agent.KnowledgeDatasources
			controls["remove_datasource_config"] = func() { options.Agent.KnowledgeDatasources = nil; reopen() }
			controls["restore_datasource_config"] = func() { options.Agent.KnowledgeDatasources = catalog; reopen() }
			controls["revoke_source_bind"] = func() { grant("libraries_bind_source") }
			controls["revoke_source_list"] = func() { grant("libraries_sources") }
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
