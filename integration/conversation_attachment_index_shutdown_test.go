package integration_test

import (
	"context"
	"errors"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

type heldAttachmentKnowledge struct {
	agentsdk.ConversationAttachmentKnowledge
	operation string
	entered   chan context.Context
	release   chan struct{}
}

func (h *heldAttachmentKnowledge) ResolveAttachmentKnowledge(ctx context.Context, c string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentKnowledgeScope, error) {
	scope, err := h.ConversationAttachmentKnowledge.ResolveAttachmentKnowledge(ctx, c, a)
	if err == nil {
		scope.Source = &heldAttachmentSource{ConversationAttachmentKnowledgeSource: scope.Source, gate: h}
	}
	return scope, err
}

type heldAttachmentSource struct {
	agentsdk.ConversationAttachmentKnowledgeSource
	gate *heldAttachmentKnowledge
}

func (h *heldAttachmentSource) hold(ctx context.Context, operation string) error {
	if h.gate.operation != operation {
		return nil
	}
	select {
	case h.gate.entered <- ctx:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.gate.release:
		return ctx.Err()
	}
}
func (h *heldAttachmentSource) PutKnowledgeDocument(ctx context.Context, in agentsdk.KnowledgeDocumentContent, a agentsdk.ConversationAuthority) error {
	if err := h.hold(ctx, "put"); err != nil {
		return err
	}
	return h.ConversationAttachmentKnowledgeSource.PutKnowledgeDocument(ctx, in, a)
}
func (h *heldAttachmentSource) DeleteKnowledgeDocument(ctx context.Context, id string, a agentsdk.ConversationAuthority) error {
	if err := h.hold(ctx, "delete"); err != nil {
		return err
	}
	return h.ConversationAttachmentKnowledgeSource.DeleteKnowledgeDocument(ctx, id, a)
}

func TestPrivateAttachmentCommittedWritesDrainBeforeHostCloses(t *testing.T) {
	for _, operation := range []string{"put", "delete"} {
		t.Run(operation, func(t *testing.T) {
			repo, a := conversationRepository(t), conversationAuthority()
			wire := &privateIndexProtocol{docs: map[string]privateIndexRemoteDocument{}}
			upstream := httptest.NewServer(http.HandlerFunc(wire.serve))
			defer upstream.Close()
			base, err := provider.NewAttachmentKnowledge(privateIndexConfig(upstream.URL, a.WorkspaceID), a.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			source := &heldAttachmentKnowledge{ConversationAttachmentKnowledge: base, operation: operation, entered: make(chan context.Context, 1), release: make(chan struct{})}
			files, err := knowledgemodule.NewAttachmentFiles(filepath.Join(t.TempDir(), "originals"))
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			options := application.ConversationOptions{AttachmentStorage: files, AttachmentAuthorizer: &attachmentTestPolicy{}, DocumentPoll: 10 * time.Millisecond, AttachmentKnowledge: []agentsdk.ConversationAttachmentKnowledgeBinding{{WorkspaceID: a.WorkspaceID, Knowledge: source}}}
			model := conversationModelFunc(func(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
				return agentsdk.ConversationModelResult{}, nil
			})
			service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			var release sync.Once
			defer func() { release.Do(func() { close(source.release) }); service.Close() }()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "shutdown"}, a)
			if err != nil {
				t.Fatal(err)
			}
			att, err := service.UploadAttachment(t.Context(), c.ID, agentsdk.ConversationAttachmentUpload{ClientID: "file", Filename: "private.txt", Data: []byte("private original")}, a)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.IndexAttachment(t.Context(), c.ID, att.ID, att.Revision, a); err != nil {
				t.Fatal(err)
			}
			waitState := func(state string) persistence.ConversationAttachmentRecord {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					r, err := repo.AttachmentRecord(t.Context(), att.ID, a)
					if err != nil {
						t.Fatal(err)
					}
					if r.Attachment.State == state {
						return r
					}
					time.Sleep(5 * time.Millisecond)
				}
				t.Fatal("state did not become", state)
				return persistence.ConversationAttachmentRecord{}
			}
			if operation == "delete" {
				r := waitState("ready")
				if _, err = service.DeleteAttachment(t.Context(), c.ID, att.ID, r.Attachment.Revision, a); err != nil {
					t.Fatal(err)
				}
			}
			var write context.Context
			select {
			case write = <-source.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not reach committed write")
			}
			r, err := repo.AttachmentRecord(t.Context(), att.ID, a)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "put" && !r.Index.PutStarted || operation == "delete" && !r.Index.DeleteStarted {
				t.Fatal("gate must be after durable marker")
			}
			stopped := make(chan struct{})
			go func() { service.Close(); close(stopped) }()
			deadline := time.Now().Add(3 * time.Second)
			observed := false
			for time.Now().Before(deadline) {
				var problem *agentsdk.Error
				if errors.As(service.ConversationReady(t.Context()), &problem) && problem.Code == "agent.conversation.worker_stopped" {
					observed = true
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !observed || write.Err() != nil {
				t.Fatal("host cancellation interrupted a committed write", observed, write.Err())
			}
			select {
			case <-stopped:
				t.Fatal("host closed before committed write drained")
			default:
			}
			release.Do(func() { close(source.release) })
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("host did not finish draining")
			}
			r, err = repo.AttachmentRecord(t.Context(), att.ID, a)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "put" && !r.Index.PutAcknowledged || operation == "delete" && !r.Index.DeleteAcknowledged {
				t.Fatal("shutdown lost successful write acknowledgement", r.Index)
			}
			service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "put" {
				r = waitState("ready")
				if _, err = service.DeleteAttachment(t.Context(), c.ID, att.ID, r.Attachment.Revision, a); err != nil {
					t.Fatal(err)
				}
			}
			waitState("deleted")
			wire.mu.Lock()
			defer wire.mu.Unlock()
			if wire.puts != 1 || wire.deletes != 1 {
				t.Fatal("shutdown repeated remote effect", wire.puts, wire.deletes)
			}
		})
	}
}
