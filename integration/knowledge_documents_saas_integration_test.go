package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"

	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
)

func TestKnowledgeDocumentsSaaSLargeOriginalAndUnknownPut(t *testing.T) {
	for _, scenario := range []struct {
		name              string
		lostAck, imported bool
	}{{"acknowledged", false, false}, {"lost_acknowledgment", true, false}, {"import_lost_acknowledgment", true, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			lostAck := scenario.lostAck
			repo, a := conversationRepository(t), conversationAuthority()
			lib, err := repo.CreateKnowledgeLibrary(t.Context(), agentsdk.KnowledgeLibraryCreate{ClientID: "personal", Kind: "personal", Name: "My library"}, a)
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			remote, exists, puts, deletes := "", false, 0, 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path == "/v1/kb/kbs/personal/documents" {
					if r.Method == "POST" {
						puts++
						remote = r.URL.Query().Get("doc_id")
						exists = true
						io.Copy(io.Discard, r.Body)
						if lostAck {
							w.WriteHeader(503)
							return
						}
					} else {
						deletes++
						exists = false
					}
					io.WriteString(w, `{"err_code":0}`)
					return
				}
				var in map[string]any
				json.NewDecoder(r.Body).Decode(&in)
				if !exists || in["doc_id"] != remote {
					io.WriteString(w, `{"err_code":1004}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"doc_id": remote, "status": "INDEXED"}})
			}))
			defer upstream.Close()
			source, err := provider.NewKnowledge(provider.KnowledgeConfig{BaseURL: upstream.URL, TeamID: "team", KBID: "personal", WorkspaceID: a.WorkspaceID, APIKey: "fixture", DocumentManagement: true, ResponseMapping: &provider.KnowledgeResponseMapping{Search: &provider.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Excerpt: "/body"}, Fetch: &provider.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Excerpt: "/body"}}})
			if err != nil {
				t.Fatal(err)
			}
			files, err := knowledgemodule.NewDocumentFiles(t.TempDir() + "/private")
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			attachments, err := knowledgemodule.NewAttachmentFiles(t.TempDir() + "/attachments")
			if err != nil {
				t.Fatal(err)
			}
			defer attachments.Close()
			attachmentPolicy := &attachmentTestPolicy{}
			policy := &libraryTestPolicy{}
			tools, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "UTC")
			if err != nil {
				t.Fatal(err)
			}
			service, err := conversationassembly.NewService(repo, &executionModel{}, a.RuntimeID, application.ConversationOptions{AttachmentStorage: attachments, AttachmentAuthorizer: attachmentPolicy, DocumentStorage: files, DocumentPoll: 10 * time.Millisecond, LibraryAuthorizer: policy, PersonalAuthorizer: personalReadAuthorizer{}, ToolHost: tools, LibraryKnowledge: []application.LibraryKnowledgeBinding{{WorkspaceID: a.WorkspaceID, LibraryID: lib.ID, Source: source, ManageDocuments: true}}})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			server, err := agentserver.New(agentserver.Config{APIKey: "documents-saas-fixture", Conversations: service, ConversationRuntimeID: a.RuntimeID})
			if err != nil {
				t.Fatal(err)
			}
			rpc := httptest.NewServer(server.Handler())
			defer rpc.Close()
			binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: rpc.URL, APIKey: "documents-saas-fixture", Client: rpc.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
			if err != nil {
				t.Fatal(err)
			}
			defer binding.Close(context.Background())
			api := binding.(agentsdk.ConversationBinding).Conversations().(agentsdk.KnowledgeDocumentService)
			in := agentsdk.KnowledgeDocumentUpload{ClientID: "big", Filename: "原始资料.txt", Data: bytes.Repeat([]byte("original bytes\n"), 240000)}
			var doc agentsdk.KnowledgeDocument
			var imported agentsdk.KnowledgeAttachmentImport
			if scenario.imported {
				conversations := binding.(agentsdk.ConversationBinding).Conversations()
				conv, e := conversations.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "source"}, a)
				if e != nil {
					t.Fatal(e)
				}
				att, e := conversations.(agentsdk.ConversationAttachmentService).UploadAttachment(t.Context(), conv.ID, agentsdk.ConversationAttachmentUpload{ClientID: "file", Filename: in.Filename, Data: in.Data}, a)
				if e != nil {
					t.Fatal(e)
				}
				imported = agentsdk.KnowledgeAttachmentImport{ClientID: "copy", ConversationID: conv.ID, AttachmentID: att.ID, ExpectedRevision: att.Revision}
				importer := conversations.(agentsdk.KnowledgeAttachmentImportService)
				attachmentPolicy.denied.Store("attachments_download")
				_, e = importer.ImportConversationAttachment(t.Context(), lib.ID, imported, a)
				artifactErrorClass(t, e, "forbidden")
				attachmentPolicy.denied.Store("")
				doc, err = importer.ImportConversationAttachment(t.Context(), lib.ID, imported, a)
				if err != nil {
					t.Fatal(err)
				}
				if _, e = conversations.(agentsdk.ConversationAttachmentService).DeleteAttachment(t.Context(), conv.ID, att.ID, att.Revision, a); e != nil {
					t.Fatal(e)
				}
				// Retry a completed import after its source has been revoked/deleted.
				attachmentPolicy.denied.Store("attachments_download")
				replay, e := importer.ImportConversationAttachment(t.Context(), lib.ID, imported, a)
				if e != nil || replay.ID != doc.ID {
					t.Fatal("lost import acknowledgement did not recover", e)
				}
				record, e := repo.KnowledgeDocumentRecord(t.Context(), doc.ID, a)
				if e != nil || record.AttachmentOrigin == nil || record.AttachmentOrigin.AttachmentID != att.ID {
					t.Fatal("private provenance missing", e)
				}
				changed := imported
				changed.AttachmentID = "att_other"
				_, e = importer.ImportConversationAttachment(t.Context(), lib.ID, changed, a)
				artifactErrorClass(t, e, "conflict")
			} else {
				doc, err = api.UploadKnowledgeDocument(t.Context(), lib.ID, in, a)
			}
			if err != nil || doc.Bytes != int64(len(in.Data)) {
				t.Fatal("large upload", err)
			}
			down, err := api.DownloadKnowledgeDocument(t.Context(), lib.ID, doc.ID, a)
			if err != nil || !bytes.Equal(down.Data, in.Data) {
				t.Fatal("large download", err)
			}
			other := a
			other.UserID = "stranger"
			if _, err := api.KnowledgeDocument(t.Context(), lib.ID, doc.ID, other); err == nil {
				t.Fatal("personal original leaked")
			}
			policy.denied.Store(true)
			if _, err := api.DownloadKnowledgeDocument(t.Context(), lib.ID, doc.ID, a); err == nil {
				t.Fatal("revoked Identity ignored")
			}
			policy.denied.Store(false)
			page, err := api.KnowledgeDocuments(t.Context(), lib.ID, "", 10, a)
			if err != nil || len(page.Items) != 1 {
				t.Fatal("document page", err)
			}
			want := "ready"
			if lostAck {
				want = "needs_reconcile"
			}
			for deadline := time.Now().Add(5 * time.Second); ; {
				doc, err = api.KnowledgeDocument(t.Context(), lib.ID, doc.ID, a)
				if err != nil {
					t.Fatal(err)
				}
				if doc.State == want {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("index state=%+v want=%s", doc, want)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if lostAck && doc.ErrorCode != "document_put_uncertain" {
				t.Fatal("unknown write incorrectly acknowledged", doc)
			}
			if _, err = api.DeleteKnowledgeDocument(t.Context(), lib.ID, doc.ID, doc.Revision, a); err != nil {
				t.Fatal(err)
			}
			for deadline := time.Now().Add(5 * time.Second); ; {
				r, err := repo.KnowledgeDocumentRecord(t.Context(), doc.ID, a)
				if err != nil {
					t.Fatal(err)
				}
				if r.Document.State == "deleted" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("cleanup failed: %+v", r)
				}
				time.Sleep(5 * time.Millisecond)
			}
			mu.Lock()
			if puts != 1 || deletes != 1 || exists {
				t.Errorf("lifecycle repeated or incomplete: put=%d delete=%d exists=%v", puts, deletes, exists)
			}
			mu.Unlock()
		})
	}
}
