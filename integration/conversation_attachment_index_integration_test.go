package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

type privateIndexRemoteDocument struct {
	body                []byte
	permission, request string
	deleted             bool
}
type privateIndexProtocol struct {
	mu                                sync.Mutex
	docs                              map[string]privateIndexRemoteDocument
	puts, deletes, fetchesAfterDelete int
	lostPut, lostDelete               bool
	lostDeleteOnce                    bool
	preflight                         func()
}

func (p *privateIndexProtocol) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.URL.Path == "/v1/kb/kbs/attachments/documents" {
		id := r.URL.Query().Get("doc_id")
		if r.Method == "POST" {
			p.puts++
			var ids []string
			if json.Unmarshal([]byte(r.Header.Get("X-KB-Permission-Ids")), &ids) != nil || len(ids) != 1 || !strings.HasPrefix(ids[0], "scope:agent:attachment:") || r.Header.Get("X-KB-Request-ID") == "" || id == "" {
				http.Error(w, "missing private scope", 400)
				return
			}
			raw, _ := io.ReadAll(r.Body)
			p.docs[id] = privateIndexRemoteDocument{body: raw, permission: ids[0], request: r.Header.Get("X-KB-Request-ID")}
			if p.lostPut {
				http.Error(w, "accepted but response lost", 503)
				return
			}
		} else if r.Method == "DELETE" {
			p.deletes++
			d := p.docs[id]
			d.deleted = true
			p.docs[id] = d
			if p.lostDelete {
				if p.lostDeleteOnce {
					p.lostDelete = false
				}
				http.Error(w, "deleted but response lost", 503)
				return
			}
		} else {
			http.Error(w, "method", 405)
			return
		}
		io.WriteString(w, `{"err_code":0}`)
		return
	}
	var in struct {
		KB          string   `json:"kb_id"`
		ID          string   `json:"doc_id"`
		Permissions []string `json:"permission_ids"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.KB != "attachments" {
		http.Error(w, "scope", 400)
		return
	}
	if r.URL.Path == "/v1/kb/search" {
		hits := []any{}
		for id, d := range p.docs {
			if !d.deleted && len(in.Permissions) == 1 && in.Permissions[0] == d.permission {
				hits = append(hits, map[string]any{"doc_id": id, "body": string(d.body), "title": "Private"})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"hits": hits})
		return
	}
	d, found := p.docs[in.ID]
	if d.deleted {
		p.fetchesAfterDelete++
	}
	if !found && p.preflight != nil {
		p.preflight()
	}
	if !found || d.deleted || len(in.Permissions) != 1 || in.Permissions[0] != d.permission {
		io.WriteString(w, `{"err_code":1004}`)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"doc_id": in.ID, "status": "INDEXED", "body": string(d.body), "title": "Private"}})
}

func privateIndexConfig(url, workspace string) provider.KnowledgeConfig {
	return provider.KnowledgeConfig{BaseURL: url, APIKey: "synthetic", TeamID: "team", KBID: "attachments", WorkspaceID: workspace, ResponseMapping: &provider.KnowledgeResponseMapping{Search: &provider.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Excerpt: "/body", Title: "/title"}, Fetch: &provider.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Excerpt: "/body", Title: "/title"}}}
}

func TestPrivateAttachmentIndexSaaSConnectorLifecycleAndResponseLoss(t *testing.T) {
	for _, mode := range []string{"normal", "lost-put", "lost-delete", "lost-delete-recovered", "revoked-during-preflight"} {
		t.Run(mode, func(t *testing.T) {
			repo, a := conversationRepository(t), conversationAuthority()
			wire := &privateIndexProtocol{docs: map[string]privateIndexRemoteDocument{}, lostPut: mode == "lost-put", lostDelete: strings.HasPrefix(mode, "lost-delete"), lostDeleteOnce: mode == "lost-delete-recovered"}
			upstream := httptest.NewServer(http.HandlerFunc(wire.serve))
			defer upstream.Close()
			source, err := provider.NewAttachmentKnowledge(privateIndexConfig(upstream.URL, a.WorkspaceID), a.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			originalDirectory := filepath.Join(t.TempDir(), "originals")
			files, err := knowledgemodule.NewAttachmentFiles(originalDirectory)
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			policy := &attachmentTestPolicy{}
			if mode == "revoked-during-preflight" {
				wire.preflight = func() { policy.denied.Store("attachments_index") }
			}
			var configured agentsdk.ConversationAttachmentKnowledge = source
			if mode == "lost-delete" {
				configured = noAttachmentDeleteRecovery{source}
			}
			options := application.ConversationOptions{AttachmentStorage: files, AttachmentAuthorizer: policy, DocumentPoll: 10 * time.Millisecond, AttachmentKnowledge: []agentsdk.ConversationAttachmentKnowledgeBinding{{WorkspaceID: a.WorkspaceID, Knowledge: configured}}}
			var service *application.ConversationService
			var closeRPC func()
			var api agentsdk.ConversationService
			start := func() {
				t.Helper()
				service, err = conversationassembly.NewService(repo, nil, a.RuntimeID, options)
				if err != nil {
					t.Fatal(err)
				}
				server, err := agentserver.New(agentserver.Config{APIKey: "synthetic-private-index", Conversations: service, ConversationRuntimeID: a.RuntimeID})
				if err != nil {
					t.Fatal(err)
				}
				host := httptest.NewServer(server.Handler())
				binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: host.URL, APIKey: "synthetic-private-index", Client: host.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
				if err != nil {
					t.Fatal(err)
				}
				api = binding.(agentsdk.ConversationBinding).Conversations()
				closeRPC = func() { _ = binding.Close(context.Background()); host.Close() }
			}
			start()
			defer func() { closeRPC(); service.Close() }()
			c, err := api.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "private-index"}, a)
			if err != nil {
				t.Fatal(err)
			}
			upload := agentsdk.ConversationAttachmentUpload{ClientID: "original", Filename: "私有资料.txt", Data: []byte("PRIVATE-ORIGINAL\n" + strings.Repeat("字节不变", 300))}
			att, err := api.(agentsdk.ConversationAttachmentService).UploadAttachment(t.Context(), c.ID, upload, a)
			if err != nil {
				t.Fatal(err)
			}
			r, err := repo.AttachmentRecord(t.Context(), att.ID, a)
			if err != nil || r.Source != nil || r.Attachment.State != "stored" {
				t.Fatal("upload automatically indexed", r, err)
			}
			policy.denied.Store("attachments_index")
			_, err = api.(agentsdk.ConversationAttachmentIndexService).IndexAttachment(t.Context(), c.ID, att.ID, att.Revision, a)
			artifactErrorClass(t, err, "forbidden")
			policy.denied.Store("")
			wrong, err := api.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "other-conversation"}, a)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = api.(agentsdk.ConversationAttachmentIndexService).IndexAttachment(t.Context(), wrong.ID, att.ID, att.Revision, a); err == nil {
				t.Fatal("cross-conversation index accepted")
			}
			indexed, err := api.(agentsdk.ConversationAttachmentIndexService).IndexAttachment(t.Context(), c.ID, att.ID, att.Revision, a)
			if err != nil {
				t.Fatal(err)
			}
			public, _ := json.Marshal(indexed)
			for _, secret := range []string{"dka_", "aput_", "scope:agent:attachment:", "body_ref", "PRIVATE-ORIGINAL"} {
				if strings.Contains(string(public), secret) {
					t.Fatal("public metadata leaked private lifecycle", secret)
				}
			}
			wait := func(check func(persistence.ConversationAttachmentRecord) bool, timeout time.Duration) persistence.ConversationAttachmentRecord {
				t.Helper()
				deadline := time.Now().Add(timeout)
				for time.Now().Before(deadline) {
					r, err = repo.AttachmentRecord(t.Context(), att.ID, a)
					if err != nil {
						t.Fatal(err)
					}
					if check(r) {
						return r
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("attachment lifecycle timed out: %+v", r)
				return r
			}
			if mode == "revoked-during-preflight" {
				r = wait(func(r persistence.ConversationAttachmentRecord) bool {
					return r.Attachment.ErrorCode == "attachment_index_access_denied"
				}, 5*time.Second)
				if r.Index.PutStarted {
					t.Fatal("revoked write started")
				}
			} else if mode == "lost-put" {
				r = wait(func(r persistence.ConversationAttachmentRecord) bool {
					return r.Attachment.ErrorCode == "attachment_put_uncertain"
				}, 5*time.Second)
				if !r.Index.PutStarted || r.Index.PutAcknowledged {
					t.Fatal("uncertain PUT receipt was fabricated")
				}
			} else {
				r = wait(func(r persistence.ConversationAttachmentRecord) bool { return r.Attachment.State == "ready" }, 5*time.Second)
				replay, err := api.(agentsdk.ConversationAttachmentIndexService).IndexAttachment(t.Context(), c.ID, att.ID, att.Revision, a)
				if err != nil || replay.ID != att.ID || replay.State != "ready" {
					t.Fatal("index response recovery duplicated work", err)
				}
			}
			if mode == "normal" {
				checker := api.(agentsdk.ConversationAttachmentIndexCheckService)
				policy.denied.Store("attachments_check_index")
				_, err = checker.CheckAttachmentIndex(t.Context(), c.ID, att.ID, r.Attachment.Revision, a)
				artifactErrorClass(t, err, "forbidden")
				policy.denied.Store("")
				if _, err = checker.CheckAttachmentIndex(t.Context(), wrong.ID, att.ID, r.Attachment.Revision, a); err == nil {
					t.Fatal("foreign conversation check allowed")
				}
				checked, err := checker.CheckAttachmentIndex(t.Context(), c.ID, att.ID, r.Attachment.Revision, a)
				if err != nil || checked.Indexing == nil || checked.Indexing.LastCheckRevision != r.Attachment.Revision || !checked.Indexing.CanCheck || checked.State != "ready" {
					t.Fatal("SaaS check contract lost", checked, err)
				}
				replay, err := checker.CheckAttachmentIndex(t.Context(), c.ID, att.ID, r.Attachment.Revision, a)
				if err != nil || replay.Indexing == nil || replay.Indexing.LastCheckRevision != r.Attachment.Revision {
					t.Fatal("SaaS check replay lost", err)
				}
				r, err = repo.AttachmentRecord(t.Context(), att.ID, a)
				if err != nil {
					t.Fatal(err)
				}
				scope, err := source.ResolveAttachmentKnowledge(t.Context(), c.ID, a)
				if err != nil {
					t.Fatal(err)
				}
				if r.Source.PermissionID != scope.PermissionID {
					t.Fatal("persisted private ACL mismatch")
				}
				passages, err := scope.Source.ReadKnowledgeDocumentPassages(t.Context(), r.Source.DocID, a)
				if err != nil || len(passages) != 1 || passages[0].Content != string(upload.Data) {
					t.Fatal("Connector private read changed bytes", err)
				}
				b := a
				b.UserID = "other-user"
				if _, err = scope.Source.ReadKnowledgeDocumentPassages(t.Context(), r.Source.DocID, b); err == nil {
					t.Fatal("resolved source reused by another user")
				}
				for _, target := range []struct {
					conversation string
					authority    agentsdk.ConversationAuthority
				}{{wrong.ID, a}, {c.ID, b}} {
					other, err := source.ResolveAttachmentKnowledge(t.Context(), target.conversation, target.authority)
					if err != nil {
						t.Fatal(err)
					}
					if other.PermissionID == scope.PermissionID {
						t.Fatal("conversation or owner ACL collision")
					}
					if state, err := other.Source.InspectKnowledgeDocument(t.Context(), r.Source.DocID, target.authority); err != nil || state.Exists {
						t.Fatal("private remote ACL leaked document", state, err)
					}
				}
				wire.mu.Lock()
				remote := wire.docs[r.Source.DocID]
				wire.mu.Unlock()
				if !bytes.Equal(remote.body, upload.Data) || remote.request != r.Source.RequestID {
					t.Fatal("original bytes or logical write ID changed")
				}
			}
			// Restart the SaaS host with the durable state, then delete the parent.
			// A lost PUT response must still lead to exactly one remote deletion.
			originalRef := r.BodyRef
			closeRPC()
			service.Close()
			start()
			if mode == "lost-delete-recovered" {
				if _, err = api.(agentsdk.ConversationAttachmentService).DeleteAttachment(t.Context(), c.ID, att.ID, r.Attachment.Revision, a); err != nil {
					t.Fatal(err)
				}
				r = wait(func(r persistence.ConversationAttachmentRecord) bool {
					return r.Attachment.ErrorCode == "attachment_delete_uncertain"
				}, 5*time.Second)
				if r.Index.DeleteAcknowledged || r.BodyRef == "" {
					t.Fatal("lost acknowledgement fabricated")
				}
				closeRPC()
				service.Close()
				start()
				if _, err = api.(agentsdk.ConversationAttachmentIndexCheckService).CheckAttachmentIndex(t.Context(), c.ID, att.ID, r.Attachment.Revision, a); err != nil {
					t.Fatal(err)
				}
			} else if err = api.Delete(t.Context(), c.ID, c.Revision, a); err != nil {
				t.Fatal(err)
			}
			if mode == "lost-delete" {
				wait(func(r persistence.ConversationAttachmentRecord) bool {
					return r.Attachment.ErrorCode == "attachment_delete_uncertain"
				}, 5*time.Second)
				deadline := time.Now().Add(35 * time.Second)
				for time.Now().Before(deadline) {
					wire.mu.Lock()
					seen := wire.fetchesAfterDelete > 0
					wire.mu.Unlock()
					if seen {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				wire.mu.Lock()
				absentObserved := wire.fetchesAfterDelete > 0
				wire.mu.Unlock()
				r, err = repo.AttachmentRecord(t.Context(), att.ID, a)
				if err != nil || !absentObserved || r.Index.DeleteAcknowledged || r.Attachment.State != "deleting" || r.BodyRef == "" {
					t.Fatal("absence falsely confirmed private deletion", r, err)
				}
				if original, err := files.ReadAttachmentContent(t.Context(), att.ID, r.BodyRef, a); err != nil || !bytes.Equal(original, upload.Data) {
					t.Fatal("uncertain delete discarded original", err)
				}
			} else {
				r = wait(func(r persistence.ConversationAttachmentRecord) bool { return r.Attachment.State == "deleted" }, 5*time.Second)
				if r.BodyRef != "" {
					t.Fatal("deleted original ref remains")
				}
				if _, err = files.ReadAttachmentContent(t.Context(), att.ID, originalRef, a); err == nil {
					t.Fatal("original remains after confirmed remote cleanup")
				}
				remaining, err := filepath.Glob(filepath.Join(originalDirectory, "*", "*.bin"))
				if err != nil || len(remaining) != 0 {
					t.Fatal("original bytes not physically removed", remaining, err)
				}
				if _, err = os.Stat(originalDirectory); err != nil {
					t.Fatal("test original store disappeared", err)
				}
			}
			wire.mu.Lock()
			puts, deletes := wire.puts, wire.deletes
			wire.mu.Unlock()
			if mode == "revoked-during-preflight" {
				if puts != 0 || deletes != 0 {
					t.Fatal("revoked original reached remote", puts, deletes)
				}
			} else if mode == "lost-delete-recovered" {
				if puts != 1 || deletes != 2 || !r.Index.DeleteAcknowledged {
					t.Fatal("safe recovery did not repeat exactly the same DELETE and preserve acknowledgement", puts, deletes)
				}
			} else if puts != 1 || deletes != 1 {
				t.Fatal("remote write repeated", puts, deletes)
			}
		})
	}
}
