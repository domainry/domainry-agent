package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	"github.com/domainry/domainry-agent-sdk/persistence"
	agentpersistence "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-orm/query"
)

type managedLiveWrite struct {
	Method           string `json:"method"`
	KBID             string `json:"kb_id,omitempty"`
	DocumentID       string `json:"doc_id"`
	Filename         string `json:"filename,omitempty"`
	PermissionHeader string `json:"permission_header,omitempty"`
	RequestID        string `json:"request_id,omitempty"`
	HTTPStatus       int    `json:"http_status,omitempty"`
}
type managedLiveTransport struct {
	mu     sync.Mutex
	path   string
	writes []managedLiveWrite
}

func (r *managedLiveTransport) save() error {
	raw, err := json.MarshalIndent(r.writes, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(r.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func (r *managedLiveTransport) snapshot() []managedLiveWrite {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]managedLiveWrite(nil), r.writes...)
}
func (r *managedLiveTransport) RoundTrip(in *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(in.URL.Path, "/documents") {
		return http.DefaultTransport.RoundTrip(in)
	}
	r.mu.Lock()
	i := len(r.writes)
	r.writes = append(r.writes, managedLiveWrite{Method: in.Method, KBID: strings.TrimSuffix(strings.TrimPrefix(in.URL.Path, "/v1/kb/kbs/"), "/documents"), DocumentID: in.URL.Query().Get("doc_id"), Filename: in.URL.Query().Get("filename"), PermissionHeader: in.Header.Get("X-KB-Permission-Ids"), RequestID: in.Header.Get("X-KB-Request-ID")})
	err := r.save() // Durable exact ownership/ACL evidence before real mutation.
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out, err := http.DefaultTransport.RoundTrip(in)
	r.mu.Lock()
	if out != nil {
		r.writes[i].HTTPStatus = out.StatusCode
	}
	_ = r.save()
	r.mu.Unlock()
	return out, err
}

// Opt-in product lifecycle: real Identity, HTTP, SQLite, original storage,
// persistent worker, Module and Connector; the model is deterministic.
func TestLiveManagedPrivateDocumentIdentityHTTP(t *testing.T) {
	runManagedPrivateDocumentIdentityHTTP(t, false, "shared")
}

func TestLiveDynamicKnowledgeDatasourcesIdentityHTTP(t *testing.T) {
	if os.Getenv("AGENT_DATASOURCE_DOCUMENT_LIVE") != "1" {
		t.Skip("requires explicit three-KB synthetic upload acceptance")
	}
	root := os.Getenv("AGENT_LIVE_EVIDENCE_DIR")
	if !filepath.IsAbs(root) {
		t.Fatal("persistent evidence directory required")
	}
	if provider.KnowledgeConfigFromEnvironment().TeamID != "1470194374940573696" {
		t.Fatal("the three pre-approved KBs require their verified team")
	}
	for _, tc := range []struct{ name, kb, kind string }{{"personal_a", "kb-3bbd8f1d3249", "personal"}, {"personal_b", "kb-148740163350", "personal"}, {"shared", "kb-1f90b90e69a8", "shared"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENT_KNOWLEDGE_KB_ID", tc.kb)
			t.Setenv("AGENT_LIVE_EVIDENCE_DIR", filepath.Join(root, tc.name))
			runManagedPrivateDocumentIdentityHTTP(t, true, tc.kind)
		})
	}
}

func runManagedPrivateDocumentIdentityHTTP(t *testing.T, dynamic bool, kind string, formats ...string) {
	format := ""
	if len(formats) > 0 {
		format = formats[0]
	}
	if os.Getenv("AGENT_MANAGED_DOCUMENT_LIVE") != "1" {
		t.Skip("requires explicit synthetic private upload acceptance")
	}
	config := provider.KnowledgeConfigFromEnvironment()
	dir := os.Getenv("AGENT_LIVE_EVIDENCE_DIR")
	if config.BaseURL != "https://api.verdent.ai" || !filepath.IsAbs(dir) {
		t.Fatal("verified origin and persistent evidence directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	const initial, changed = "Initial-Private-Upload!2", "Changed-Private-Upload!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "private-upload-test-signing-secret")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "private-upload-test-data-secret")
	t.Setenv("APP_ENV", "development")
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(random)
	marker := "QINGHE-PRIVATE-UPLOAD-" + strings.ToUpper(suffix)
	transport := &managedLiveTransport{path: filepath.Join(dir, "writes.json")}
	options := Options{DatabasePath: filepath.Join(dir, "managed.db"), RuntimeID: "managed-live-" + suffix, WorkspaceID: "managed-live", ApplicationKey: "managed-live", Agent: agentmodule.Options{ConversationProvider: libraryPermissionModel{}, ConversationOptions: agentmodule.ConversationOptions{Poll: 20 * time.Millisecond, DocumentPoll: 3 * time.Second}}}

	if format != "" {
		marker = "Qinghe Fixture"
		liveModel, err := provider.NewConversationModel(provider.ConversationModelConfigFromEnvironment())
		if err != nil {
			t.Fatal("live extraction requires configured model", err)
		}
		options.Agent.ConversationProvider = liveModel
		options.Agent.ConversationOptions.MaxSteps = 16
	}
	metadata := "/data"
	config.WorkspaceID = options.WorkspaceID
	config.PermissionIDs, config.AuthorizeWorkspace, config.Transport = nil, nil, nil
	config.Client = &http.Client{Transport: transport, Timeout: 40 * time.Second}
	config.ResponseMapping = &provider.KnowledgeResponseMapping{Search: &provider.KnowledgeCitationMapping{Items: "/data/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/snippet"}, Fetch: &provider.KnowledgeCitationMapping{Items: "/data/chunks", Many: true, MetadataObject: &metadata, DocumentID: "/doc_id", Title: "/title", Excerpt: "/content"}}

	var approved []agentmodule.KnowledgeDatasourceConfig
	if dynamic {
		approved = []agentmodule.KnowledgeDatasourceConfig{{Key: "approved", Name: "合成入库知识源", Knowledge: config}}
		options.Agent.KnowledgeDatasources = approved
	}
	var host *Host
	var b *browser
	open := func(restart bool) {
		t.Helper()
		if host != nil {
			if err := host.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			host = nil
		}
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("private upload test")}}})
		if err != nil {
			t.Fatal(err)
		}
		b = &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
		if restart {
			b.login("admin@example.com", changed)
		} else {
			b.login("admin@example.com", initial)
			b.changePassword(initial, changed)
		}
	}
	open(false)
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	mutateTestRolePermissions(t, host, b, func(prior []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		out := append([]identitysdk.ProjectRolePermission(nil), prior...)
		for _, d := range agentsdk.ConversationHTTPDefinitions() {
			p := agentsdk.KnowledgeLibraryPermission(d.Operation)
			if p == nil {
				p = agentsdk.KnowledgeDocumentPermission(d.Operation)
			}
			if p != nil {
				out = append(out, identitysdk.ProjectRolePermission{PermissionKey: p.Key, DataScope: identitysdk.DataScopeAll})
			}
		}
		for _, tool := range append(agentsdk.LibraryKnowledgeConversationTools(), agentsdk.KnowledgeExtractionTool()) {
			out = append(out, identitysdk.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identitysdk.DataScopeOwner})
		}
		return out
	})
	var library agentsdk.KnowledgeLibrary
	if err := json.Unmarshal(b.call("POST", "/agent/knowledge-libraries", `{"client_id":"library","kind":"`+kind+`","name":"合成私有上传验收"}`, 200).Body.Bytes(), &library); err != nil {
		t.Fatal(err)
	}

	if dynamic {
		input := agentsdk.KnowledgeLibrarySourceWrite{DatasourceKey: "approved", ExpectedRevision: library.Revision}
		raw, _ := json.Marshal(input)
		path := "/agent/knowledge-libraries/" + library.ID + "/source"
		if err := json.Unmarshal(b.call("PUT", path, string(raw), 200).Body.Bytes(), &library); err != nil || !library.DocumentsConfigured || library.DatasourceKey != "approved" {
			t.Fatal("dynamic binding did not activate immediately", err)
		}
		revision := library.Revision
		if err := json.Unmarshal(b.call("PUT", path, string(raw), 200).Body.Bytes(), &library); err != nil || library.Revision != revision {
			t.Fatal("real binding replay mutated twice", err)
		}
	} else {
		options.Agent.KnowledgeLibraries = []agentmodule.KnowledgeLibraryConfig{{LibraryID: library.ID, Knowledge: config, ManageDocuments: true}}
		open(true)
	}

	base := "/agent/knowledge-libraries/" + library.ID + "/documents"
	var doc agentsdk.KnowledgeDocument
	report := map[string]any{"origin": config.BaseURL, "team_id": config.TeamID, "kb_id": config.KBID, "runtime_id": options.RuntimeID, "workspace_id": options.WorkspaceID, "library_id": library.ID, "synthetic": true, "dynamic_binding": dynamic, "library_kind": kind, "marker": marker, "complete": false, "cleanup_verified": false, "model": "deterministic", "database": options.DatabasePath}
	if format != "" {
		report["model"] = provider.ConversationModelConfigFromEnvironment().Model
		report["model_protocol"] = provider.ConversationModelConfigFromEnvironment().Protocol
		report["file_format"] = format
	}
	save := func() {
		raw, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "report.json"), raw, 0600); err != nil {
			t.Error(err)
		}
	}
	save()
	defer save()
	deletedRecord := func() bool {
		renderer, err := agentpersistence.Renderer("sqlite", "")
		if err != nil {
			return false
		}
		statement, args, err := query.NewSelectBuilder(renderer, "_agent_knowledge_documents").Columns("payload_json").Where(query.And(query.Equal("document_id", doc.ID), query.Equal("library_id", library.ID))).Build()
		if err != nil {
			return false
		}
		var raw []byte
		if host.db.QueryRowContext(context.Background(), statement, args...).Scan(&raw) != nil {
			return false
		}
		var r persistence.KnowledgeDocumentRecord
		return json.Unmarshal(raw, &r) == nil && r.Actor.RuntimeID == options.RuntimeID && r.Document.State == "deleted" && r.BodyRef == "" && (!r.PutStarted || r.DeleteAcknowledged && r.AccessPolicySHA256 != "")
	}
	cleanup := func() bool {
		if dynamic && len(options.Agent.KnowledgeDatasources) == 0 {
			options.Agent.KnowledgeDatasources = approved
			open(true)
		}
		if !dynamic && !options.Agent.KnowledgeLibraries[0].ManageDocuments {
			options.Agent.KnowledgeLibraries[0].ManageDocuments = true
			open(true)
		}
		request := func(method, path string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, "http://127.0.0.1:8091"+path, nil)
			req.Header.Set("Origin", "http://127.0.0.1:8091")
			req.Header.Set("X-Agent-Scope", b.scope)
			for _, cookie := range b.cookies {
				req.AddCookie(cookie)
			}
			out := httptest.NewRecorder()
			b.handler.ServeHTTP(out, req)
			return out
		}
		if doc.ID == "" {
			var page agentsdk.KnowledgeDocumentPage
			out := request("GET", base)
			if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &page) != nil {
				return false
			}
			if len(page.Items) == 0 {
				return true
			}
			if len(page.Items) != 1 {
				return false
			}
			doc = page.Items[0]
		}
		path := base + "/" + doc.ID
		if deletedRecord() {
			return true
		}
		out := request("GET", path)
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &doc) != nil {
			return false
		}
		if doc.State != "deleted" && doc.State != "deleting" {
			if request("DELETE", path+fmt.Sprintf("?expected_revision=%d", doc.Revision)).Code != 200 {
				return false
			}
		}
		for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); {
			if deletedRecord() {
				return true
			}
			out = request("GET", path)
			if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &doc) != nil {
				return false
			}
			if doc.State == "deleted" {
				return true
			}
			time.Sleep(time.Second)
		}
		return false
	}
	defer func() {
		if report["cleanup_verified"] == true {
			return
		}
		if cleanup() {
			report["cleanup_verified"] = true
			return
		}
		// Preserve the DB, originals and pre-write manifest for interrupted work.
		report["recovery_required"] = true
		t.Logf("Private upload recovery state retained: %s", dir)
	}()
	raw := []byte("# 合成私有资料\n\n本文件只用于自动验收，不含真实个人或业务资料。\n\n验收标识：" + marker + "。\n\n合成规则：收到发票后 30 日付款，金额 123.45 元。\n")

	filename := "私有资料验收.md"
	if format != "" {
		switch format {
		case "pdf", "docx", "xlsx":
		default:
			t.Fatal("unsupported synthetic fixture format")
		}
		filename = "synthetic-extraction." + format
		var err error
		raw, err = os.ReadFile(filepath.Join("../../../integration/testdata/knowledge-extraction", filename))
		if err != nil {
			t.Fatal(err)
		}
	}
	query := url.Values{"client_id": {"synthetic-upload"}, "filename": {filename}}.Encode()

	upload := func() agentsdk.KnowledgeDocument {
		t.Helper()
		req := httptest.NewRequest("POST", "http://127.0.0.1:8091"+base+"?"+query, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Origin", "http://127.0.0.1:8091")
		req.Header.Set("X-Agent-Scope", b.scope)
		for _, c := range b.cookies {
			req.AddCookie(c)
		}
		res := httptest.NewRecorder()
		b.handler.ServeHTTP(res, req)
		var out agentsdk.KnowledgeDocument
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &out) != nil {
			t.Fatalf("product upload failed: HTTP %d", res.Code)
		}
		return out
	}
	doc = upload()
	report["document_id"] = doc.ID
	save()
	if repeated := upload(); repeated.ID != doc.ID {
		t.Fatal("duplicate product reservation")
	}
	statusPath := base + "/" + doc.ID
	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for {
			if want == "deleted" {
				if deletedRecord() {
					doc.State = "deleted"
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("private cleanup lacks acknowledged durable tombstone")
				}
				time.Sleep(time.Second)
				continue
			}
			if err := json.Unmarshal(b.call("GET", statusPath, "", 200).Body.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if doc.State == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("document state %s/%s/%s; wanted %s", doc.State, doc.IndexStatus, doc.ErrorCode, want)
			}
			time.Sleep(time.Second)
		}
	}
	wait("ready")
	report["indexed"] = true
	save()
	writes := transport.snapshot()
	if len(writes) != 1 || writes[0].Method != "POST" || writes[0].RequestID == "" {
		t.Fatal("unstable or duplicate upstream upload")
	}
	var ids []string
	if json.Unmarshal([]byte(writes[0].PermissionHeader), &ids) != nil || len(ids) != 1 || !strings.HasPrefix(ids[0], "scope:agent:library:") {
		t.Fatal("managed default uploaded without private ACL")
	}
	report["private_acl"] = true
	// No matching scope, a wrong scope, and the exact persisted scope exercise
	// upstream visibility independently of Agent's local document allowlist.
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, UserID: "probe"}
	for _, tc := range []struct {
		name   string
		ids    []string
		exists bool
	}{{"omitted", nil, false}, {"wrong", []string{"synthetic:wrong"}, false}, {"matching", ids, true}} {
		c := config
		c.DocumentPermissionIDs = tc.ids
		source, err := provider.NewKnowledge(c)
		if err != nil {
			t.Fatal(err)
		}
		state, err := source.InspectKnowledgeDocument(t.Context(), writes[0].DocumentID, a)
		if err != nil || state.Exists != tc.exists {
			t.Fatalf("upstream private visibility %s failed: %v", tc.name, err)
		}
		report["visibility_"+tc.name] = true
	}

	if dynamic {
		options.Agent.KnowledgeDatasources = nil
	} else {
		options.Agent.KnowledgeLibraries[0].ManageDocuments = false
	}
	open(true)
	if dynamic {
		var current agentsdk.KnowledgeLibrary
		_ = json.Unmarshal(b.call("GET", "/agent/knowledge-libraries/"+library.ID, "", 200).Body.Bytes(), &current)
		if current.KnowledgeConfigured || current.DocumentsConfigured || current.DatasourceKey != "approved" {
			t.Fatal("removed catalog did not preserve unavailable binding")
		}
	}

	download := b.call("GET", statusPath+"/content", "", 200)
	if !bytes.Equal(download.Body.Bytes(), raw) || !strings.Contains(download.Header().Get("Content-Disposition"), url.PathEscape(filename)) {
		t.Fatal("restart lost original bytes/filename")
	}
	report["restart_original"] = true
	if dynamic {
		report["original_after_catalog_removal"] = true
		options.Agent.KnowledgeDatasources = approved
		open(true)
	}
	var conversation agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"query"}`, 200).Body.Bytes(), &conversation)
	plan, _ := json.Marshal(libraryPermissionPlan{LibraryID: library.ID, Query: marker, DocumentID: doc.ID})

	message := string(plan)
	if format != "" {
		message = fmt.Sprintf("请读取资料库 %s 内的文档 %s，使用 knowledge_extract 提取并校验客户名 customer(text)、发票金额 amount(decimal)、签署日期 signed_date(date)、确认状态 approval(boolean)、联系邮箱 email(text, required)，以及明细表 items（item:text、quantity:integer、price:decimal）。先读取实际解析正文，再据此选择提取规则；保留每项缺失或错误，不补造文档没有提供的值。完整提取实际可用的明细行，按原始英文列出结果和来源引用，不做金额合计。", library.ID, doc.ID)
	}
	body, _ := json.Marshal(agentsdk.ConversationSend{ClientMessageID: "query", Message: message})

	var run agentsdk.ConversationRun
	convPath := "/agent/conversations/" + conversation.ID
	_ = json.Unmarshal(b.call("POST", convPath+"/messages", string(body), 202).Body.Bytes(), &run)
	waitBudget := 90 * time.Second
	if format != "" {
		waitBudget = 3 * time.Minute
	}
	for deadline := time.Now().Add(waitBudget); !run.Terminal() && time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		_ = json.Unmarshal(b.call("GET", convPath+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
	}
	if run.Status != "completed" {
		t.Fatalf("private document conversation: %s %s", run.Status, run.ErrorCode)
	}
	var messages agentsdk.ConversationMessagePage
	_ = json.Unmarshal(b.call("GET", convPath+"/messages", "", 200).Body.Bytes(), &messages)
	found := false
	for _, m := range messages.Items {
		if m.Role == "assistant" && strings.Contains(m.Content, marker) && len(m.Citations) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("product query did not cite the uploaded private document")
	}
	report["conversation_run_id"] = run.ID
	save()
	if format != "" {
		report["extraction_outputs"] = verifyLiveKnowledgeExtraction(t, host, format, conversation.ID, run.ID, library.ID, doc.ID)
	}
	report["conversation_run_id"] = run.ID
	report["conversation_citation"] = true
	if dynamic {
		report["read_after_catalog_restore"] = true
	} else {
		report["read_after_disabling_uploads"] = true
	}
	save()
	if !dynamic {
		options.Agent.KnowledgeLibraries[0].ManageDocuments = true
	}
	open(true)
	_ = json.Unmarshal(b.call("GET", statusPath, "", 200).Body.Bytes(), &doc)
	b.call("DELETE", statusPath+fmt.Sprintf("?expected_revision=%d", doc.Revision), "", 200)
	wait("deleted")
	c := config
	c.DocumentPermissionIDs = ids
	source, err := provider.NewKnowledge(c)
	if err != nil {
		t.Fatal(err)
	}
	state, err := source.InspectKnowledgeDocument(t.Context(), writes[0].DocumentID, a)
	if err != nil || state.Exists {
		t.Fatal("private cleanup not confirmed", err)
	}
	for deadline := time.Now().Add(time.Minute); ; {
		raw, err := source.Search(t.Context(), marker, a)
		if err != nil {
			t.Fatal("cleanup search failed", err)
		}
		var envelope struct {
			Result struct {
				Data struct {
					Hits *[]struct {
						DocumentID string `json:"doc_id"`
					} `json:"hits"`
				} `json:"data"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.Result.Data.Hits == nil {
			t.Fatal("invalid cleanup search response")
		}
		present := false
		for _, hit := range *envelope.Result.Data.Hits {
			present = present || hit.DocumentID == writes[0].DocumentID
		}
		if !present {
			report["absent_from_search"] = true
			report["cleanup_search_hits"] = len(*envelope.Result.Data.Hits)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deleted document remained searchable")
		}
		time.Sleep(time.Second)
	}
	finalWrites := transport.snapshot()
	if len(finalWrites) != 2 || finalWrites[1].Method != "DELETE" {
		t.Fatal("upstream mutations repeated")
	}
	report["cleanup_verified"] = true
	report["complete"] = true
	save()
	t.Logf("Private product upload, index, 3 visibility scopes, restart, citation and cleanup passed: %s", dir)
}
