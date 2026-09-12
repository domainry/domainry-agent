package integration_test

import (
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	"github.com/domainry/domainry-knowledge/artifact"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

func TestManagedDocumentPolicyChangesAndHiddenDeletionRemainRecoverable(t *testing.T) {
	for _, scenario := range []string{"queued-policy-changed", "uncertain-policy-changed", "private-delete-unacknowledged", "delete-recovered", "delete-retry-failed", "delete-policy-changed", "delete-source-changed"} {
		t.Run(scenario, func(t *testing.T) {
			repo, a := conversationRepository(t), conversationAuthority()
			lib, err := repo.CreateKnowledgeLibrary(t.Context(), agentsdk.KnowledgeLibraryCreate{ClientID: "private", Kind: "personal", Name: "Private"}, a)
			if err != nil {
				t.Fatal(err)
			}
			var calls, deletes atomic.Int64
			var expectedRemoteID string
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method == http.MethodDelete && (scenario == "delete-recovered" || scenario == "delete-retry-failed") {
					deletes.Add(1)
					if r.URL.Path != "/v1/kb/kbs/private/documents" || r.URL.Query().Get("doc_id") != expectedRemoteID {
						t.Error("recovery changed the frozen remote document")
					}
					if scenario == "delete-retry-failed" {
						http.Error(w, "purge still incomplete", http.StatusServiceUnavailable)
					} else {
						io.WriteString(w, `{"err_code":0,"data":{"ok":true,"stats":{"vectors_deleted":0}}}`)
					}
					return
				}
				if r.URL.Path != "/v1/kb/fetch" {
					t.Error("stale or uncertain work repeated a write")
				}
				io.WriteString(w, `{"err_code":1004}`) // ACL denied, not proof of global deletion.
			}))
			defer remote.Close()
			config := provider.KnowledgeConfig{BaseURL: remote.URL, APIKey: "fixture", TeamID: "team", KBID: "private", WorkspaceID: a.WorkspaceID, DocumentManagement: true, DocumentPermissionIDs: []string{"original"}, ResponseMapping: &provider.KnowledgeResponseMapping{Search: &provider.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Excerpt: "/body"}, Fetch: &provider.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Excerpt: "/body"}}}
			source, err := provider.NewKnowledge(config)
			if err != nil {
				t.Fatal(err)
			}
			scope := agentsdk.KnowledgeDocumentStorageScope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, LibraryID: lib.ID}
			if err := repo.ActivateKnowledgeDocumentSource(t.Context(), scope, source.KnowledgeDocumentSourceIdentity()); err != nil {
				t.Fatal(err)
			}
			files, err := knowledgemodule.NewDocumentFiles(t.TempDir() + "/documents")
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			data := []byte("durable-private-document")
			r, err := repo.ReserveKnowledgeDocument(t.Context(), persistence.KnowledgeDocumentReserve{LibraryID: lib.ID, ClientID: "upload", Filename: "资料.txt", ContentType: "text/plain", Bytes: int64(len(data)), SHA256: artifact.Hash(data), SourceID: source.KnowledgeDocumentSourceIdentity(), AccessPolicySHA256: source.KnowledgeDocumentAccessPolicySHA256()}, a)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := files.PutKnowledgeDocumentContent(t.Context(), scope, r.Document.ID, r.Document.SHA256, data)
			if err != nil {
				t.Fatal(err)
			}
			r, err = repo.CommitKnowledgeDocumentContent(t.Context(), r.Document.ID, r.Document.Revision, ref, a)
			if err != nil {
				t.Fatal(err)
			}
			requestID := r.PutRequestID
			expectedRemoteID = r.RemoteID
			if scenario != "queued-policy-changed" {
				lease, found, err := repo.ClaimKnowledgeDocumentWork(t.Context(), a.RuntimeID, "interrupted-worker", time.Now(), time.Minute)
				if err != nil || !found {
					t.Fatal(err)
				}
				r, _, err = repo.StartKnowledgeDocumentPut(t.Context(), lease)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(scenario, "delete") {
					r, err = repo.RequestKnowledgeDocumentDeletion(t.Context(), r.Document.ID, r.Document.Revision, a)
					if err != nil {
						t.Fatal(err)
					}
					if err = repo.ApplyKnowledgeDocumentProgress(t.Context(), lease, persistence.KnowledgeDocumentProgress{Event: "indexed", IndexStatus: "INDEXED", RetryAt: time.Now()}); err != nil {
						t.Fatal(err)
					}
					lease, found, err = repo.ClaimKnowledgeDocumentWork(t.Context(), a.RuntimeID, "interrupted-delete", time.Now(), time.Minute)
					if err != nil || !found {
						t.Fatal(err)
					}
					if err = repo.StartKnowledgeDocumentDelete(t.Context(), lease); err != nil {
						t.Fatal(err)
					}
				}
				if err = repo.ApplyKnowledgeDocumentProgress(t.Context(), lease, persistence.KnowledgeDocumentProgress{Event: "retry", ErrorCode: "document_put_uncertain", RetryAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			want := "document_delete_uncertain"
			if strings.HasSuffix(scenario, "policy-changed") {
				config.DocumentPermissionIDs = []string{"new-policy"}
				source, err = provider.NewKnowledge(config)
				if err != nil {
					t.Fatal(err)
				}
				want = "document_access_policy_changed"
			}
			if scenario == "delete-source-changed" {
				config.KBID = "replacement"
				source, err = provider.NewKnowledge(config)
				if err != nil {
					t.Fatal(err)
				}
				want = "document_management_unavailable"
			}
			var bound agentsdk.ConversationKnowledgeSource = source
			if !strings.HasPrefix(scenario, "delete-") {
				bound = noLibraryDeleteRecoverySource{noDeleteRecoverySource{source}, source}
			}
			tools, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "UTC")
			if err != nil {
				t.Fatal(err)
			}
			service, err := conversationassembly.NewService(repo, &executionModel{}, a.RuntimeID, application.ConversationOptions{ToolHost: tools, PersonalAuthorizer: personalReadAuthorizer{}, LibraryAuthorizer: &libraryTestPolicy{}, DocumentStorage: files, DocumentPoll: 10 * time.Millisecond, LibraryKnowledge: []application.LibraryKnowledgeBinding{{LibraryID: lib.ID, WorkspaceID: a.WorkspaceID, Source: bound, ManageDocuments: true}}})
			if scenario == "delete-source-changed" {
				if err == nil {
					service.Close()
					t.Fatal("changed physical source started recovery")
				}
				if !strings.Contains(err.Error(), "document_source_changed") || calls.Load() != 0 {
					t.Fatal("source fence did not reject before IO", err)
				}
				if original, err := files.ReadKnowledgeDocumentContent(t.Context(), scope, r.Document.ID, ref); err != nil || string(original) != string(data) {
					t.Fatal("rejected source replacement discarded original", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			deadline := time.Now().Add(5 * time.Second)
			for {
				r, err = repo.KnowledgeDocumentRecord(t.Context(), r.Document.ID, a)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "delete-recovered" && r.Document.State == "deleted" || scenario != "delete-recovered" && r.Document.ErrorCode == want {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("work did not stop safely: %s %s", r.Document.State, r.Document.ErrorCode)
				}
				time.Sleep(5 * time.Millisecond)
			}
			service.Close()
			if r.RemoteID != expectedRemoteID || r.PutRequestID != requestID {
				t.Fatal("recovery replaced the persisted command")
			}
			if scenario == "delete-recovered" {
				if !r.DeleteAcknowledged || r.BodyRef != "" || deletes.Load() != 1 || calls.Load() != 2 {
					t.Fatal("recovery did not persist its acknowledgement before cleanup", calls.Load(), deletes.Load())
				}
				if _, err := files.ReadKnowledgeDocumentContent(t.Context(), scope, r.Document.ID, ref); err == nil {
					t.Fatal("acknowledged deletion retained original bytes")
				}
				return
			}
			if r.Document.State == "deleted" || r.BodyRef == "" || r.PutRequestID != requestID {
				t.Fatal("uncertainty discarded durable content/command")
			}
			if scenario != "private-delete-unacknowledged" && scenario != "delete-retry-failed" && calls.Load() != 0 {
				t.Fatal("changed policy reached upstream")
			}
			if scenario == "private-delete-unacknowledged" && (calls.Load() != 1 || r.DeleteAcknowledged) {
				t.Fatal("hidden private document falsely confirmed deletion")
			}
			if scenario == "delete-retry-failed" && (calls.Load() != 1 || deletes.Load() != 1 || r.DeleteAcknowledged) {
				t.Fatal("failed retry was confirmed or repeated without backoff")
			}
			if original, err := files.ReadKnowledgeDocumentContent(t.Context(), scope, r.Document.ID, r.BodyRef); err != nil || string(original) != string(data) {
				t.Fatal("recovery original discarded", err)
			}
		})
	}
}
