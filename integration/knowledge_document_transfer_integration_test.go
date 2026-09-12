package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

type transferTestPolicy struct{ denied atomic.Value }

func (p *transferTestPolicy) AuthorizeKnowledgeLibrary(_ context.Context, op string, lib agentsdk.KnowledgeLibrary, _ agentsdk.ConversationAuthority) error {
	if p.denied.Load() == lib.ID+":"+op {
		return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.library_access_denied"}
	}
	return nil
}
func (*transferTestPolicy) ValidateKnowledgeLibraryMember(context.Context, string, agentsdk.ConversationAuthority) error {
	return nil
}

func TestKnowledgeDocumentTransferSaaSScopesRecoveryAndStaleSearch(t *testing.T) {
	repo, a, ctx := conversationRepository(t), conversationAuthority(), t.Context()
	b := a
	b.UserID = "second"
	personal, err := repo.CreateKnowledgeLibrary(ctx, agentsdk.KnowledgeLibraryCreate{ClientID: "personal", Kind: "personal", Name: "Private"}, a)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := repo.CreateKnowledgeLibrary(ctx, agentsdk.KnowledgeLibraryCreate{ClientID: "shared", Kind: "shared", Name: "Shared"}, a)
	if err != nil {
		t.Fatal(err)
	}
	shared, err = repo.SetKnowledgeLibraryMember(ctx, shared.ID, b.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: "reader", ExpectedRevision: shared.Revision}, a)
	if err != nil {
		t.Fatal(err)
	}
	files, err := knowledgemodule.NewDocumentFiles(t.TempDir() + "/documents")
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	type remoteDocument struct {
		kb, id, body, request string
		deleted               bool
		puts, deletes         int
	}
	var mu sync.Mutex
	documents := map[string]*remoteDocument{}
	policies := map[string]string{"personal": "private-reader", "shared": "shared-readers"}
	loseNextPut := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		for kb, policy := range policies {
			if r.URL.Path != "/v1/kb/kbs/"+kb+"/documents" {
				continue
			}
			id := r.URL.Query().Get("doc_id")
			key := kb + ":" + id
			if r.Method == http.MethodPost {
				if r.Header.Get("X-KB-Permission-Ids") != fmt.Sprintf(`["%s"]`, policy) || r.Header.Get("X-KB-Request-ID") == "" {
					t.Error("wrong target ACL or missing stable request ID")
				}
				raw, e := io.ReadAll(r.Body)
				if e != nil {
					t.Error(e)
				}
				if documents[key] == nil {
					documents[key] = &remoteDocument{kb: kb, id: id}
				}
				d := documents[key]
				d.body, d.request, d.puts = string(raw), r.Header.Get("X-KB-Request-ID"), d.puts+1
				if loseNextPut {
					loseNextPut = false
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
			} else if r.Method == http.MethodDelete {
				d := documents[key]
				if d == nil {
					t.Error("delete addressed another remote scope")
					w.WriteHeader(404)
					return
				}
				d.deleted, d.deletes = true, d.deletes+1
			} else {
				t.Error("unexpected document mutation")
				w.WriteHeader(405)
				return
			}
			io.WriteString(w, `{"err_code":0}`)
			return
		}
		var input struct {
			KBID  string `json:"kb_id"`
			DocID string `json:"doc_id"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || policies[input.KBID] == "" {
			t.Error("invalid remote scope")
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == "/v1/kb/search" {
			hits := []any{}
			// Deliberately keep deleted index hits: local retirement must win
			// before the remote search index has caught up with DELETE.
			keys := make([]string, 0, len(documents))
			for key := range documents {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				d := documents[key]
				if d.kb == input.KBID {
					hits = append(hits, map[string]any{"doc_id": d.id, "title": "Transfer fixture", "body": d.body})
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"hits": hits})
			return
		}
		if r.URL.Path != "/v1/kb/fetch" {
			t.Error("unexpected connector operation")
			w.WriteHeader(404)
			return
		}
		d := documents[input.KBID+":"+input.DocID]
		if d == nil || d.deleted {
			io.WriteString(w, `{"err_code":1004}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"doc_id": d.id, "status": "INDEXED", "title": "Transfer fixture", "body": d.body}})
	}))
	defer upstream.Close()
	bindings := []application.LibraryKnowledgeBinding{}
	for _, entry := range []struct{ kb, library string }{{"personal", personal.ID}, {"shared", shared.ID}} {
		source, e := provider.NewKnowledge(provider.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture", TeamID: "team", KBID: entry.kb, WorkspaceID: a.WorkspaceID, DocumentManagement: true, DocumentPermissionIDs: []string{policies[entry.kb]}, ResponseMapping: &provider.KnowledgeResponseMapping{Search: &provider.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}, Fetch: &provider.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}}})
		if e != nil {
			t.Fatal(e)
		}
		bindings = append(bindings, application.LibraryKnowledgeBinding{WorkspaceID: a.WorkspaceID, LibraryID: entry.library, Source: source, ManageDocuments: true})
	}
	policy := &transferTestPolicy{}
	tools, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	const marker = "K07-TRANSFER-ONLY-CONTENT"
	model := &executionModel{step: func(_ int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		var query struct {
			Library string `json:"library"`
			Visible bool   `json:"visible"`
		}
		for _, m := range in.Messages {
			if m.Role == "user" {
				if err := json.Unmarshal([]byte(m.Content), &query); err != nil {
					return agentsdk.ConversationStepResult{}, err
				}
			}
		}
		last := in.Messages[len(in.Messages)-1]
		if last.Role != "tool" {
			return resultToolCall("knowledge_search", fmt.Sprintf("search-%d", len(in.Messages)), map[string]any{"library_id": query.Library, "query": "transfer"}), nil
		}
		if strings.Contains(last.Content, marker) != query.Visible {
			t.Errorf("search after transfer visible=%v, expected=%v", strings.Contains(last.Content, marker), query.Visible)
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "Search checked"}, FinishReason: "stop"}, nil
	}}
	options := application.ConversationOptions{ToolHost: tools, PersonalAuthorizer: personalReadAuthorizer{}, LibraryAuthorizer: policy, DocumentStorage: files, DocumentPoll: 10 * time.Millisecond, Poll: 10 * time.Millisecond, LibraryKnowledge: bindings}
	var service *application.ConversationService
	var rpc *httptest.Server
	var closeBinding func()
	var conversations agentsdk.ConversationService
	var api agentsdk.KnowledgeDocumentService
	var transfers agentsdk.KnowledgeDocumentTransferService
	stop := func() {
		if closeBinding != nil {
			closeBinding()
			closeBinding = nil
		}
		if rpc != nil {
			rpc.Close()
			rpc = nil
		}
		if service != nil {
			service.Close()
			service = nil
		}
	}
	defer stop()
	start := func() {
		t.Helper()
		service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
		if err != nil {
			t.Fatal(err)
		}
		server, e := agentserver.New(agentserver.Config{APIKey: "transfer-fixture", Conversations: service, ConversationRuntimeID: a.RuntimeID})
		if e != nil {
			t.Fatal(e)
		}
		rpc = httptest.NewServer(server.Handler())
		binding, e := agentremote.NewFactory(agentremote.Options{BaseURL: rpc.URL, APIKey: "transfer-fixture", Client: rpc.Client()}).OpenSaaS(ctx, agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
		if e != nil {
			t.Fatal(e)
		}
		closeBinding = func() { binding.Close(context.Background()) }
		conversations = binding.(agentsdk.ConversationBinding).Conversations()
		api = conversations.(agentsdk.KnowledgeDocumentService)
		transfers = conversations.(agentsdk.KnowledgeDocumentTransferService)
	}
	start()
	waitState := func(id, state string, timeout time.Duration) persistence.KnowledgeDocumentRecord {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for {
			r, e := repo.KnowledgeDocumentRecord(ctx, id, a)
			if e != nil {
				t.Fatal(e)
			}
			if r.Document.State == state {
				return r
			}
			if time.Now().After(deadline) {
				t.Fatalf("document %s remained %s (%s), want %s", id, r.Document.State, r.Document.ErrorCode, state)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	search := func(client, library string, visible bool, authority agentsdk.ConversationAuthority) {
		t.Helper()
		c, e := conversations.Create(ctx, agentsdk.ConversationCreate{ClientID: client}, authority)
		if e != nil {
			t.Fatal(e)
		}
		body, _ := json.Marshal(map[string]any{"library": library, "visible": visible})
		r, e := conversations.Send(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: client, Message: string(body)}, authority)
		if e != nil {
			t.Fatal(e)
		}
		for deadline := time.Now().Add(5 * time.Second); ; {
			v, e := conversations.Run(ctx, c.ID, r.ID, authority)
			if e != nil {
				t.Fatal(e)
			}
			if v.Status == "completed" {
				return
			}
			if v.Status == "failed" || time.Now().After(deadline) {
				t.Fatalf("search run ended: %+v", v)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	data := []byte(marker)
	original, err := api.UploadKnowledgeDocument(ctx, personal.ID, agentsdk.KnowledgeDocumentUpload{ClientID: "original", Filename: "transfer.txt", Data: data}, a)
	if err != nil {
		t.Fatal(err)
	}
	original = waitState(original.ID, "ready", 5*time.Second).Document
	if _, err = api.DownloadKnowledgeDocument(ctx, personal.ID, original.ID, b); err == nil {
		t.Fatal("shared reader read private origin")
	}
	copyRequest := agentsdk.KnowledgeDocumentTransfer{ClientID: "copy", SourceLibraryID: personal.ID, SourceDocumentID: original.ID, ExpectedRevision: original.Revision, Mode: "copy"}
	for _, denial := range []string{personal.ID + ":documents_download", shared.ID + ":documents_transfer", shared.ID + ":documents_upload"} {
		policy.denied.Store(denial)
		if _, err = transfers.TransferKnowledgeDocument(ctx, shared.ID, copyRequest, a); err == nil {
			t.Fatal("transfer ignored independently revoked action", denial)
		}
	}
	policy.denied.Store("")
	copyDoc, err := transfers.TransferKnowledgeDocument(ctx, shared.ID, copyRequest, a)
	if err != nil {
		t.Fatal(err)
	}
	copyRecord := waitState(copyDoc.ID, "ready", 5*time.Second)
	copyDoc = copyRecord.Document
	if copyRecord.DocumentOrigin == nil || copyRecord.DocumentOrigin.DocumentID != original.ID {
		t.Fatal("transfer provenance missing")
	}
	for _, item := range []struct {
		lib, id   string
		authority agentsdk.ConversationAuthority
	}{{personal.ID, original.ID, a}, {shared.ID, copyDoc.ID, b}} {
		down, e := api.DownloadKnowledgeDocument(ctx, item.lib, item.id, item.authority)
		if e != nil || !bytes.Equal(down.Data, data) {
			t.Fatal("copy did not preserve independent authorized original", e)
		}
	}
	search("shared-before-move", shared.ID, true, b)
	moveRequest := agentsdk.KnowledgeDocumentTransfer{ClientID: "move", SourceLibraryID: shared.ID, SourceDocumentID: copyDoc.ID, ExpectedRevision: copyDoc.Revision, Mode: "move"}
	policy.denied.Store(shared.ID + ":documents_delete")
	if _, err = transfers.TransferKnowledgeDocument(ctx, personal.ID, moveRequest, a); err == nil {
		t.Fatal("move ignored source delete authorization")
	}
	policy.denied.Store("")
	mu.Lock()
	loseNextPut = true
	mu.Unlock()
	moved, err := transfers.TransferKnowledgeDocument(ctx, personal.ID, moveRequest, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = api.DownloadKnowledgeDocument(ctx, shared.ID, copyDoc.ID, b); err == nil {
		t.Fatal("moved source still downloadable")
	}
	search("shared-after-move", shared.ID, false, b)
	uncertain := waitState(moved.ID, "needs_reconcile", 5*time.Second)
	if uncertain.Document.ErrorCode != "document_put_uncertain" {
		t.Fatal("lost remote acknowledgment not recorded")
	}
	if _, err = api.DownloadKnowledgeDocument(ctx, personal.ID, moved.ID, b); err == nil {
		t.Fatal("private target leaked to former reader")
	}
	// Reopen service and SaaS binding against the same durable repository/files;
	// leave the actual retry schedule intact and verify no second remote PUT.
	stop()
	start()
	policy.denied.Store(shared.ID + ":documents_download")
	recovered, err := transfers.TransferKnowledgeDocument(ctx, personal.ID, moveRequest, a)
	if err != nil || recovered.ID != moved.ID {
		t.Fatal("lost move receipt could not recover without source access", err)
	}
	policy.denied.Store("")
	ready := waitState(moved.ID, "ready", 40*time.Second)
	if ready.AccessPolicySHA256 == copyRecord.AccessPolicySHA256 || ready.RemoteID == copyRecord.RemoteID || ready.PutRequestID == copyRecord.PutRequestID {
		t.Fatal("move retained shared remote scope")
	}
	if ready.PutRequestID != uncertain.PutRequestID {
		t.Fatal("restart changed uncertain command")
	}
	search("private-after-move", personal.ID, true, a)
	waitState(copyDoc.ID, "deleted", 5*time.Second)
	if _, err = files.ReadKnowledgeDocumentContent(ctx, agentsdk.KnowledgeDocumentStorageScope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, LibraryID: shared.ID}, copyDoc.ID, copyRecord.BodyRef); err == nil {
		t.Fatal("retired source original not physically cleaned")
	}
	for _, id := range []string{original.ID, moved.ID} {
		d, e := api.KnowledgeDocument(ctx, personal.ID, id, a)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = api.DeleteKnowledgeDocument(ctx, personal.ID, id, d.Revision, a); e != nil {
			t.Fatal(e)
		}
		waitState(id, "deleted", 5*time.Second)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(documents) != 3 {
		t.Fatalf("unexpected remote document count: %d", len(documents))
	}
	for _, d := range documents {
		if d.puts != 1 || d.deletes != 1 || !d.deleted || d.body != marker {
			t.Errorf("remote lifecycle not exactly once: %s puts=%d deletes=%d", d.kb, d.puts, d.deletes)
		}
	}
	t.Log("Three originals copied/moved through SaaS; target ACLs checked at Connector HTTP, stale source search hidden, uncertain PUT recovered after restart, all remote documents cleaned")
}
