package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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

var transferAcceptanceKBs = []string{"kb-3bbd8f1d3249", "kb-148740163350", "kb-1f90b90e69a8"}

func TestLiveKnowledgeDocumentTransfersIdentityHTTP(t *testing.T) {
	if os.Getenv("AGENT_DOCUMENT_TRANSFERS_LIVE") != "1" {
		t.Skip("requires explicit synthetic transfer acceptance")
	}
	config := provider.KnowledgeConfigFromEnvironment()
	dir := os.Getenv("AGENT_LIVE_EVIDENCE_DIR")
	if config.BaseURL != "https://api.verdent.ai" || config.TeamID != "1470194374940573696" || !filepath.IsAbs(dir) {
		t.Fatal("verified origin, team and persistent evidence directory required")
	}
	runKnowledgeDocumentTransferHTTP(t, config, dir, true)
}

// Exercise the entire live harness locally before any external mutation.
func TestKnowledgeDocumentTransfersIdentityHTTPProtocol(t *testing.T) {
	type remoteDoc struct {
		kb, id, body string
		ids          []string
		deleted      bool
	}
	docs := map[string]*remoteDoc{}
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/documents") {
			kb := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/kb/kbs/"), "/documents")
			if !slices.Contains(transferAcceptanceKBs, kb) {
				t.Error("unexpected KB")
				w.WriteHeader(400)
				return
			}
			id := r.URL.Query().Get("doc_id")
			key := kb + ":" + id
			if r.Method == "POST" {
				var ids []string
				if json.Unmarshal([]byte(r.Header.Get("X-KB-Permission-Ids")), &ids) != nil || len(ids) != 1 {
					t.Error("missing private write policy")
					w.WriteHeader(400)
					return
				}
				if docs[key] != nil {
					t.Error("duplicate remote PUT")
				}
				raw, _ := io.ReadAll(r.Body)
				docs[key] = &remoteDoc{kb: kb, id: id, body: string(raw), ids: ids}
			} else if r.Method == "DELETE" && docs[key] != nil {
				docs[key].deleted = true
			} else {
				t.Error("unexpected document write")
				w.WriteHeader(400)
				return
			}
			io.WriteString(w, `{"err_code":0}`)
			return
		}
		var in struct {
			KB  string   `json:"kb_id"`
			Doc string   `json:"doc_id"`
			IDs []string `json:"permission_ids"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == "/v1/kb/search" {
			hits := []any{}
			for _, d := range docs {
				if d.kb == in.KB && !d.deleted && slices.Equal(in.IDs, d.ids) {
					hits = append(hits, map[string]any{"doc_id": d.id, "title": "Synthetic transfer", "snippet": d.body})
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"hits": hits}})
			return
		}
		d := docs[in.KB+":"+in.Doc]
		if d == nil || d.deleted || !slices.Equal(in.IDs, d.ids) {
			io.WriteString(w, `{"err_code":1004}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"doc_id": d.id, "title": "Synthetic transfer", "status": "INDEXED", "chunks": []any{map[string]any{"content": d.body}}}})
	}))
	defer upstream.Close()
	runKnowledgeDocumentTransferHTTP(t, provider.KnowledgeConfig{BaseURL: upstream.URL, TeamID: "fixture-team", APIKey: "fixture"}, t.TempDir(), false)
}

func runKnowledgeDocumentTransferHTTP(t *testing.T, config provider.KnowledgeConfig, dir string, real bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	const initial, changed = "Initial-Transfer-Live!2", "Changed-Transfer-Live!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "transfer-test-signing-secret")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "transfer-test-data-secret")
	t.Setenv("APP_ENV", "development")
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(random)
	marker := "QINGHE-TRANSFER-" + strings.ToUpper(suffix)
	raw := []byte("# 合成跨库资料\n\n仅用于自动验收，不含真实个人或业务信息。\n\n验收标识：" + marker + "\n\n测试规则：核对资料后 30 日内付款，合成金额 123.45 元。\n")
	filename := "agent-transfer-" + suffix + ".md"
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	transport := &managedLiveTransport{path: filepath.Join(dir, "writes.json")}
	options := Options{DatabasePath: filepath.Join(dir, "transfers.db"), RuntimeID: "transfers-live-" + suffix, WorkspaceID: "transfers-live", ApplicationKey: "transfers-live", Agent: agentmodule.Options{ConversationProvider: libraryPermissionModel{}, ConversationOptions: agentmodule.ConversationOptions{Poll: 20 * time.Millisecond, DocumentPoll: time.Second}}}
	metadata := "/data"
	config.WorkspaceID = options.WorkspaceID
	config.PermissionIDs, config.AuthorizeWorkspace, config.Transport = nil, nil, nil
	config.DocumentPermissionIDs = nil
	config.Client = &http.Client{Transport: transport, Timeout: 40 * time.Second}
	config.ResponseMapping = &provider.KnowledgeResponseMapping{Search: &provider.KnowledgeCitationMapping{Items: "/data/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/snippet"}, Fetch: &provider.KnowledgeCitationMapping{Items: "/data/chunks", Many: true, MetadataObject: &metadata, DocumentID: "/doc_id", Title: "/title", Excerpt: "/content"}}
	for i, kb := range transferAcceptanceKBs {
		c := config
		c.KBID = kb
		options.Agent.KnowledgeDatasources = append(options.Agent.KnowledgeDatasources, agentmodule.KnowledgeDatasourceConfig{Key: fmt.Sprintf("source-%d", i), Name: fmt.Sprintf("Synthetic source %d", i), Knowledge: c})
	}
	var host *Host
	var a, b *browser
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
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("transfer acceptance")}}})
		if err != nil {
			t.Fatal(err)
		}
		a = &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
		b = &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
		password := initial
		if restart {
			password = changed
		}
		a.login("admin@example.com", password)
		b.login("system_administrator@example.com", password)
		if !restart {
			a.changePassword(initial, changed)
			b.changePassword(initial, changed)
		}
	}
	open(false)
	defer func() {
		if host != nil {
			host.Close(context.Background())
		}
	}()
	for _, user := range []string{a.readSession()["user_id"].(string), b.readSession()["user_id"].(string)} {
		mutateTestRolePermissions(t, host, a, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := append([]identitysdk.ProjectRolePermission(nil), previous...)
			seen := map[string]bool{}
			for _, p := range out {
				seen[p.PermissionKey] = true
			}
			for _, d := range agentsdk.ConversationHTTPDefinitions() {
				p := agentsdk.KnowledgeLibraryPermission(d.Operation)
				if p == nil {
					p = agentsdk.KnowledgeDocumentPermission(d.Operation)
				}
				if p != nil && !seen[p.Key] {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: p.Key, DataScope: identitysdk.DataScopeAll})
					seen[p.Key] = true
				}
			}
			for _, tool := range agentsdk.LibraryKnowledgeConversationTools() {
				if !seen[tool.ActionKey] {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identitysdk.DataScopeOwner})
				}
			}
			return out
		}, user)
	}
	decodeLibrary := func(raw []byte) agentsdk.KnowledgeLibrary {
		t.Helper()
		var l agentsdk.KnowledgeLibrary
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	libraries := []agentsdk.KnowledgeLibrary{
		decodeLibrary(a.call("POST", "/agent/knowledge-libraries", `{"client_id":"personal-a","kind":"personal","name":"个人 A 合成验收"}`, 200).Body.Bytes()),
		decodeLibrary(b.call("POST", "/agent/knowledge-libraries", `{"client_id":"personal-b","kind":"personal","name":"个人 B 合成验收"}`, 200).Body.Bytes()),
		decodeLibrary(a.call("POST", "/agent/knowledge-libraries", `{"client_id":"shared","kind":"shared","name":"共享合成验收"}`, 200).Body.Bytes()),
	}
	owner := func(index int) *browser {
		if index == 1 {
			return b
		}
		return a
	}
	base := func(index int) string { return "/agent/knowledge-libraries/" + libraries[index].ID + "/documents" }
	for i := range libraries {
		body, _ := json.Marshal(agentsdk.KnowledgeLibrarySourceWrite{DatasourceKey: fmt.Sprintf("source-%d", i), ExpectedRevision: libraries[i].Revision})
		libraries[i] = decodeLibrary(owner(i).call("PUT", "/agent/knowledge-libraries/"+libraries[i].ID+"/source", string(body), 200).Body.Bytes())
		if !libraries[i].DocumentsConfigured {
			t.Fatal("binding did not enable document management")
		}
	}
	second := b.readSession()["user_id"].(string)
	setMember := func(role string) {
		t.Helper()
		body, _ := json.Marshal(agentsdk.KnowledgeLibraryMemberWrite{Role: role, ExpectedRevision: libraries[2].Revision})
		libraries[2] = decodeLibrary(a.call("PUT", "/agent/knowledge-libraries/"+libraries[2].ID+"/members/"+second, string(body), 200).Body.Bytes())
	}
	setMember("reader")
	report := map[string]any{"complete": false, "cleanup_verified": false, "synthetic": true, "real_verdent": real, "origin": config.BaseURL, "team_id": config.TeamID, "runtime_id": options.RuntimeID, "workspace_id": options.WorkspaceID, "database": options.DatabasePath, "marker": marker, "filename": filename, "sha256": digest, "libraries": libraries, "model": "deterministic"}
	save := func() {
		data, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "report.json"), data, 0600); err != nil {
			t.Error(err)
		}
	}
	save()
	defer save()
	known := map[string]int{}
	readRecord := func(id string, index int) (persistence.KnowledgeDocumentRecord, error) {
		var record persistence.KnowledgeDocumentRecord
		renderer, err := agentpersistence.Renderer("sqlite", "")
		if err != nil {
			return record, err
		}
		q, args, err := query.NewSelectBuilder(renderer, "_agent_knowledge_documents").Columns("payload_json").Where(query.And(query.Equal("document_id", id), query.Equal("library_id", libraries[index].ID))).Build()
		if err != nil {
			return record, err
		}
		var data []byte
		if err = host.db.QueryRowContext(context.Background(), q, args...).Scan(&data); err != nil {
			return record, err
		}
		err = json.Unmarshal(data, &record)
		if err == nil && (record.Actor.RuntimeID != options.RuntimeID || record.Document.SHA256 != digest || record.Document.Filename != filename) {
			err = fmt.Errorf("record is not this run's owned synthetic original")
		}
		return record, err
	}
	request := func(client *browser, method, path string, data []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1:8091"+path, bytes.NewReader(data))
		req.Header.Set("Origin", "http://127.0.0.1:8091")
		req.Header.Set("X-Agent-Scope", client.scope)
		req.Header.Set("Content-Type", "application/octet-stream")
		for _, c := range client.cookies {
			req.AddCookie(c)
		}
		out := httptest.NewRecorder()
		client.handler.ServeHTTP(out, req)
		return out
	}
	cleanup := func() bool {
		for i := range libraries {
			out := request(owner(i), "GET", base(i)+"?limit=50", nil)
			var page agentsdk.KnowledgeDocumentPage
			if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &page) != nil || !page.Complete {
				return false
			}
			for _, d := range page.Items {
				if d.SHA256 != digest || d.Filename != filename {
					return false
				}
				known[d.ID] = i
				if d.State != "deleting" && d.State != "deleted" {
					if request(owner(i), "DELETE", base(i)+"/"+d.ID+fmt.Sprintf("?expected_revision=%d", d.Revision), nil).Code != 200 {
						return false
					}
				}
			}
		}
		for deadline := time.Now().Add(3 * time.Minute); ; {
			done := true
			for id, index := range known {
				r, err := readRecord(id, index)
				if err != nil {
					return false
				}
				if r.Document.State != "deleted" || r.BodyRef != "" || r.PutStarted && !r.DeleteAcknowledged {
					done = false
				}
			}
			if done {
				return true
			}
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(time.Second)
		}
	}
	defer func() {
		if report["cleanup_verified"] == true {
			return
		}
		report["cleanup_verified"] = cleanup()
		if report["cleanup_verified"] != true {
			report["recovery_required"] = true
			t.Logf("Transfer recovery database and ownership manifest retained: %s", dir)
		}
	}()
	decodeDocument := func(out *httptest.ResponseRecorder, index int) agentsdk.KnowledgeDocument {
		t.Helper()
		var d agentsdk.KnowledgeDocument
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &d) != nil || d.LibraryID != libraries[index].ID || d.SHA256 != digest {
			t.Fatalf("document operation failed HTTP %d", out.Code)
		}
		known[d.ID] = index
		report["documents"] = known
		save()
		return d
	}
	wait := func(id string, index int, state string) agentsdk.KnowledgeDocument {
		t.Helper()
		for deadline := time.Now().Add(3 * time.Minute); ; {
			r, err := readRecord(id, index)
			if err != nil {
				t.Fatal(err)
			}
			if r.Document.State == state {
				return r.Document
			}
			if time.Now().After(deadline) {
				t.Fatalf("document remained %s/%s, wanted %s", r.Document.State, r.Document.ErrorCode, state)
			}
			time.Sleep(time.Second)
		}
	}
	writeFor := func(id string) managedLiveWrite {
		t.Helper()
		r, indexErr := readRecord(id, known[id])
		if indexErr != nil {
			t.Fatal(indexErr)
		}
		for _, w := range transport.snapshot() {
			if w.DocumentID == r.RemoteID && w.Method == "POST" {
				return w
			}
		}
		t.Fatal("missing durable remote ownership record")
		return managedLiveWrite{}
	}
	probe := func(w managedLiveWrite, ids []string, want bool) {
		t.Helper()
		c := config
		c.KBID = w.KBID
		c.DocumentPermissionIDs = ids
		p, err := provider.NewKnowledge(c)
		if err != nil {
			t.Fatal(err)
		}
		state, err := p.InspectKnowledgeDocument(t.Context(), w.DocumentID, agentsdk.ConversationAuthority{Known: true, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, UserID: "synthetic-probe"})
		if err != nil || state.Exists != want {
			t.Fatalf("remote visibility mismatch: want %t, err %v", want, err)
		}
	}
	permissions := func(w managedLiveWrite) []string {
		t.Helper()
		var ids []string
		if json.Unmarshal([]byte(w.PermissionHeader), &ids) != nil || len(ids) != 1 || !strings.HasPrefix(ids[0], "scope:agent:library:") {
			t.Fatal("missing private library scope")
		}
		return ids
	}
	queryDocument := func(client *browser, name string, index int, id string) string {
		t.Helper()
		var c agentsdk.Conversation
		json.Unmarshal(client.call("POST", "/agent/conversations", `{"client_id":"`+name+`"}`, 200).Body.Bytes(), &c)
		plan, _ := json.Marshal(libraryPermissionPlan{LibraryID: libraries[index].ID, Query: marker, DocumentID: id})
		body, _ := json.Marshal(agentsdk.ConversationSend{ClientMessageID: name, Message: string(plan)})
		var run agentsdk.ConversationRun
		convPath := "/agent/conversations/" + c.ID
		json.Unmarshal(client.call("POST", convPath+"/messages", string(body), 202).Body.Bytes(), &run)
		for deadline := time.Now().Add(90 * time.Second); !run.Terminal() && time.Now().Before(deadline); {
			time.Sleep(200 * time.Millisecond)
			json.Unmarshal(client.call("GET", convPath+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
		}
		if run.Status != "completed" {
			t.Fatalf("query failed: %s %s", run.Status, run.ErrorCode)
		}
		var page agentsdk.ConversationMessagePage
		json.Unmarshal(client.call("GET", convPath+"/messages", "", 200).Body.Bytes(), &page)
		found := false
		for _, m := range page.Items {
			if m.Role == "assistant" && strings.Contains(m.Content, marker) {
				for _, citation := range m.Citations {
					found = found || citation.DocumentID == id && citation.LibraryID == libraries[index].ID
				}
			}
		}
		if !found {
			t.Fatal("query did not cite transferred original")
		}
		return convPath
	}
	original := decodeDocument(request(a, "POST", base(0)+"?"+url.Values{"client_id": {"original"}, "filename": {filename}}.Encode(), raw), 0)
	original = wait(original.ID, 0, "ready")
	sourceWrite := writeFor(original.ID)
	b.call("GET", base(0)+"/"+original.ID+"/content", "", 404)
	copyInput := agentsdk.KnowledgeDocumentTransfer{ClientID: "copy-to-shared", SourceLibraryID: libraries[0].ID, SourceDocumentID: original.ID, ExpectedRevision: original.Revision, Mode: "copy"}
	copyBody, _ := json.Marshal(copyInput)
	copyDoc := decodeDocument(a.call("POST", base(2)+"/from-document", string(copyBody), 200), 2)
	copyDoc = wait(copyDoc.ID, 2, "ready")
	sharedWrite := writeFor(copyDoc.ID)
	if slices.Equal(permissions(sourceWrite), permissions(sharedWrite)) {
		t.Fatal("shared copy retained personal ACL")
	}
	probe(sharedWrite, permissions(sourceWrite), false)
	probe(sharedWrite, permissions(sharedWrite), true)
	if !bytes.Equal(b.call("GET", base(2)+"/"+copyDoc.ID+"/content", "", 200).Body.Bytes(), raw) {
		t.Fatal("shared member original mismatch")
	}
	historyPath := queryDocument(b, "shared-before-move", 2, copyDoc.ID)
	report["copy_private_to_shared"] = true
	save()
	t.Log("Real product copy indexed; personal scope rejected by shared target, shared reader queried and downloaded")
	moveInput := agentsdk.KnowledgeDocumentTransfer{ClientID: "move-to-personal-b", SourceLibraryID: libraries[2].ID, SourceDocumentID: copyDoc.ID, ExpectedRevision: copyDoc.Revision, Mode: "move"}
	moveBody, _ := json.Marshal(moveInput)
	b.call("POST", base(1)+"/from-document", string(moveBody), 403)
	setMember("editor")
	moved := decodeDocument(b.call("POST", base(1)+"/from-document", string(moveBody), 200), 1)
	a.call("GET", base(2)+"/"+copyDoc.ID+"/content", "", 404)
	a.call("GET", base(1)+"/"+moved.ID+"/content", "", 404)
	open(true)
	replay := decodeDocument(b.call("POST", base(1)+"/from-document", string(moveBody), 200), 1)
	if replay.ID != moved.ID {
		t.Fatal("restart duplicated target")
	}
	if !bytes.Equal(b.call("GET", base(1)+"/"+moved.ID+"/content", "", 200).Body.Bytes(), raw) || !bytes.Equal(a.call("GET", base(0)+"/"+original.ID+"/content", "", 200).Body.Bytes(), raw) {
		t.Fatal("restart lost target or independent source")
	}
	moved = wait(moved.ID, 1, "ready")
	movedWrite := writeFor(moved.ID)
	probe(movedWrite, nil, false)
	probe(movedWrite, permissions(sharedWrite), false)
	probe(movedWrite, permissions(movedWrite), true)
	queryDocument(b, "private-after-move", 1, moved.ID)
	var history agentsdk.ConversationMessagePage
	json.Unmarshal(b.call("GET", historyPath+"/messages", "", 200).Body.Bytes(), &history)
	for _, m := range history.Items {
		if m.Role == "assistant" && (len(m.Citations) > 0 || strings.Contains(m.Content, marker)) {
			t.Fatal("old shared evidence remained visible")
		}
	}
	wait(copyDoc.ID, 2, "deleted")
	probe(sharedWrite, permissions(sharedWrite), false)
	report["move_shared_to_personal_b"] = true
	report["reader_move_denied"] = true
	report["restart_receipt_and_originals"] = true
	report["old_shared_history_hidden"] = true
	report["target_acl_omitted_and_old_scope_denied"] = true
	save()
	t.Log("Move indexed after host restart; old shared download/history/remote scope denied, personal B target cited")
	if !cleanup() {
		t.Fatal("owned transfer cleanup remains pending")
	}
	for _, w := range []managedLiveWrite{sourceWrite, sharedWrite, movedWrite} {
		probe(w, permissions(w), false)
		c := config
		c.KBID = w.KBID
		c.DocumentPermissionIDs = permissions(w)
		source, err := provider.NewKnowledge(c)
		if err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(time.Minute); ; {
			passages, err := source.SearchKnowledgeDocumentPassages(t.Context(), marker, agentsdk.ConversationAuthority{Known: true, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, UserID: "synthetic-probe"})
			if err != nil {
				t.Fatal(err)
			}
			present := false
			for _, p := range passages {
				present = present || p.DocumentID == w.DocumentID
			}
			if !present {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("deleted original remained in remote search")
			}
			time.Sleep(time.Second)
		}
	}
	writes := transport.snapshot()
	if len(writes) != 6 {
		t.Fatalf("unexpected mutation count %d", len(writes))
	}
	for _, post := range []managedLiveWrite{sourceWrite, sharedWrite, movedWrite} {
		puts, deletes := 0, 0
		for _, w := range writes {
			if w.KBID == post.KBID && w.DocumentID == post.DocumentID {
				if w.HTTPStatus != 200 {
					t.Fatal("remote mutation lacks successful HTTP response")
				}
				if w.Method == "POST" {
					puts++
				}
				if w.Method == "DELETE" {
					deletes++
				}
			}
		}
		if puts != 1 || deletes != 1 {
			t.Fatal("remote operation repeated")
		}
	}
	report["cleanup_verified"] = true
	report["remote_search_absent"] = true
	report["complete"] = true
	save()
	t.Logf("Three distinct KBs, two Identity users, copy/move permissions, restart and all cleanup verified: %s", dir)
}
