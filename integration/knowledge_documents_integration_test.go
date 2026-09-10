package integration_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-agent/internal/infrastructure/documentstorage"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

func TestManagedLibraryDocumentsIndexFilterRevokeAndRestart(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	lib, err := repo.CreateKnowledgeLibrary(t.Context(), agentsdk.KnowledgeLibraryCreate{ClientID: "managed", Kind: "shared", Name: "Project documents"}, a)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.UserID = "second"
	lib, err = repo.SetKnowledgeLibraryMember(t.Context(), lib.ID, b.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: "reader", ExpectedRevision: lib.Revision}, a)
	if err != nil {
		t.Fatal(err)
	}
	files, err := documentstorage.NewFiles(t.TempDir() + "/documents")
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	var mu sync.Mutex
	remote, phase, body := "", "", ""
	puts, deletes, requests := 0, 0, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if r.URL.Path == "/v1/kb/kbs/managed/documents" {
			if r.Method == "POST" {
				puts++
				remote = r.URL.Query().Get("doc_id")
				raw, _ := io.ReadAll(r.Body)
				body = string(raw)
				phase = "PENDING"
			} else if r.Method == "DELETE" {
				deletes++
				phase = ""
			} else {
				t.Error("unexpected write method")
			}
			io.WriteString(w, `{"err_code":0}`)
			return
		}
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil || in["kb_id"] != "managed" {
			t.Error("bad remote scope")
		}
		if r.URL.Path == "/v1/kb/search" {
			hits := []any{map[string]any{"doc_id": "unregistered", "body": "SECRET-UNREGISTERED", "title": "private"}}
			// Retain stale indexed text after DELETE to prove the local allowlist
			// wins even when the upstream search index has not caught up yet.
			if remote != "" {
				hits = append(hits, map[string]any{"doc_id": remote, "title": "Policy", "body": body, "unexpected": "SECRET-RAW-FIELD"})
			}
			json.NewEncoder(w).Encode(map[string]any{"hits": hits, "global": "SECRET-GLOBAL"})
			return
		}
		if phase == "" || in["doc_id"] != remote {
			io.WriteString(w, `{"err_code":1004}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"doc_id": remote, "status": phase, "title": "Policy", "body": body, "global": "SECRET-GLOBAL"}})
	}))
	defer upstream.Close()
	config := provider.KnowledgeConfig{BaseURL: upstream.URL, TeamID: "team", KBID: "managed", WorkspaceID: a.WorkspaceID, APIKey: "fixture", DocumentManagement: true, ResponseMapping: &provider.KnowledgeResponseMapping{Search: &provider.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}, Fetch: &provider.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}}}
	source, err := provider.NewKnowledge(config)
	if err != nil {
		t.Fatal(err)
	}
	policy := &libraryTestPolicy{}
	tools, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	docID, requireContent := "", true
	model := &executionModel{step: func(_ int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role != "tool" {
			return resultToolCall("knowledge_search", fmt.Sprintf("search-%d", len(in.Messages)), map[string]any{"library_id": lib.ID, "query": "policy"}), nil
		}
		raw := string(mustJSON(in))
		if strings.Contains(raw, "SECRET-") || strings.Contains(raw, "dka_") {
			t.Error("remote/global/unregistered data reached the model")
		}
		if requireContent && (!strings.Contains(last.Content, "APPROVED-POLICY") || !strings.Contains(last.Content, docID)) {
			t.Errorf("managed evidence missing: %s", last.Content)
		}
		if !requireContent && strings.Contains(last.Content, "APPROVED-POLICY") {
			t.Error("deleted document reached the model")
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "Library result processed"}, FinishReason: "stop"}, nil
	}}
	options := application.ConversationOptions{ToolHost: tools, PersonalAuthorizer: personalReadAuthorizer{}, LibraryAuthorizer: policy, DocumentStorage: files, DocumentPoll: 10 * time.Millisecond, Poll: 10 * time.Millisecond, LibraryKnowledge: []application.LibraryKnowledgeBinding{{WorkspaceID: a.WorkspaceID, LibraryID: lib.ID, Source: source, ManageDocuments: true}}}
	service, err := application.NewConversationService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { service.Close() }()
	upload := agentsdk.KnowledgeDocumentUpload{ClientID: "policy", Filename: "policy.txt", Data: []byte("APPROVED-POLICY\n" + strings.Repeat("完整原文", 800))}
	if _, err := service.UploadKnowledgeDocument(t.Context(), lib.ID, upload, b); err == nil {
		t.Fatal("reader uploaded")
	}
	doc, err := service.UploadKnowledgeDocument(t.Context(), lib.ID, upload, a)
	if err != nil {
		t.Fatal(err)
	}
	docID = doc.ID
	replay, err := service.UploadKnowledgeDocument(t.Context(), lib.ID, upload, a)
	if err != nil || replay.ID != doc.ID {
		t.Fatal("duplicate upload", err)
	}
	changed := upload
	changed.Data = []byte("changed")
	if _, err := service.UploadKnowledgeDocument(t.Context(), lib.ID, changed, a); err == nil {
		t.Fatal("changed idempotency replay")
	}
	waitDoc := func(state string) persistence.KnowledgeDocumentRecord {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		var r persistence.KnowledgeDocumentRecord
		for time.Now().Before(deadline) {
			var err error
			r, err = repo.KnowledgeDocumentRecord(t.Context(), doc.ID, a)
			if err != nil {
				t.Fatal(err)
			}
			if r.Document.State == state {
				return r
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("document did not reach %s: %+v", state, r)
		return r
	}
	waitDoc("indexing")
	down, err := service.DownloadKnowledgeDocument(t.Context(), lib.ID, doc.ID, b)
	if err != nil || string(down.Data) != string(upload.Data) {
		t.Fatal("member original download", err)
	}
	mu.Lock()
	phase = "INDEXED"
	mu.Unlock()
	waitDoc("ready")
	// A restarted host with management disabled must retain managed filtering.
	service.Close()
	options.LibraryKnowledge[0].ManageDocuments = false
	service, err = application.NewConversationService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	runSearch := func(client string) (string, agentsdk.ConversationRun) {
		t.Helper()
		c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: client}, a)
		if err != nil {
			t.Fatal(err)
		}
		r, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: client, Message: "查询资料"}, a)
		if err != nil {
			t.Fatal(err)
		}
		return c.ID, waitConversation(t, service, c.ID, r.ID)
	}
	conversationID, run := runSearch("ready")
	if run.Status != "completed" {
		t.Fatalf("managed retrieval failed: %+v", run)
	}
	policy.denied.Store(true)
	if _, err := service.DownloadKnowledgeDocument(t.Context(), lib.ID, doc.ID, b); err == nil {
		t.Fatal("Identity revocation ignored")
	}
	policy.denied.Store(false)
	lib, err = repo.RemoveKnowledgeLibraryMember(t.Context(), lib.ID, b.UserID, lib.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DownloadKnowledgeDocument(t.Context(), lib.ID, doc.ID, b); err == nil {
		t.Fatal("removed member downloaded")
	}
	if _, err := service.DownloadKnowledgeDocument(t.Context(), lib.ID, doc.ID, a); err != nil {
		t.Fatal("uploader removal changed library storage ownership", err)
	}
	current, err := service.KnowledgeDocument(t.Context(), lib.ID, doc.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := service.DeleteKnowledgeDocument(t.Context(), lib.ID, doc.ID, current.Revision, a)
	if err != nil || deleted.State != "deleting" {
		t.Fatal("local revocation failed", err)
	}
	if _, err := service.DownloadKnowledgeDocument(t.Context(), lib.ID, doc.ID, a); err == nil {
		t.Fatal("deleting original accessible")
	}
	view, err := service.Messages(t.Context(), conversationID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustJSON(view)), "Library result processed") {
		t.Fatal("deleted source retained historical answer")
	}
	requireContent = false
	_, run = runSearch("deleted")
	if run.Status != "completed" {
		t.Fatalf("filtered empty search failed: %+v", run)
	}
	service.Close()
	options.LibraryKnowledge[0].ManageDocuments = true
	service, err = application.NewConversationService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	// A previously paused worker persisted its backoff. Wake it with a trusted
	// idempotent deletion request; this must not reset an active lease.
	r, err := repo.KnowledgeDocumentRecord(t.Context(), doc.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.RequestKnowledgeDocumentDeletion(t.Context(), doc.ID, r.Document.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	waitDoc("deleted")
	mu.Lock()
	if puts != 1 || deletes != 1 {
		t.Errorf("writes repeated: puts=%d deletes=%d", puts, deletes)
	}
	before := requests
	mu.Unlock()
	service.Close()
	options.LibraryKnowledge = nil
	options.Knowledge = source
	model.step = func(_ int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "tool" {
			if !strings.Contains(last.Content, "knowledge_access_denied") {
				t.Error("default retrieval did not report the managed scope denial")
			}
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "Access denied"}, FinishReason: "stop"}, nil
		}
		return resultToolCall("knowledge_search", "default", map[string]any{"query": "policy"}), nil
	}
	service, err = application.NewConversationService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	_, run = runSearch("legacy")
	if run.Status != "completed" {
		t.Fatalf("default denial was not handled: %+v", run)
	}
	mu.Lock()
	if requests != before {
		t.Error("downgraded default contacted remote")
	}
	mu.Unlock()
}
