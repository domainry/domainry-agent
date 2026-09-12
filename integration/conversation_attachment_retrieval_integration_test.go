package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

type attachmentRetrievalStorage struct {
	agentsdk.ConversationAttachmentStorage
	reads atomic.Int32
}

func (s *attachmentRetrievalStorage) ReadAttachmentContent(ctx context.Context, ref, sha string, a agentsdk.ConversationAuthority) ([]byte, error) {
	s.reads.Add(1)
	return s.ConversationAttachmentStorage.ReadAttachmentContent(ctx, ref, sha, a)
}

type attachmentRetrievalPolicy struct {
	personalReadAuthorizer
	denied atomic.Value
}

func (p *attachmentRetrievalPolicy) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	denied, _ := p.denied.Load().(string)
	if denied == in.Definition.Key {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	return p.personalReadAuthorizer.AuthorizeConversationTool(ctx, in)
}

func TestPrivateAttachmentConnectorRetrievalSaaSHistoryAndRestart(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	attachmentPolicy, toolPolicy := &attachmentTestPolicy{}, &attachmentRetrievalPolicy{}
	wire := &privateIndexProtocol{docs: map[string]privateIndexRemoteDocument{}}
	var revokeDuringRead, changedContent atomic.Bool
	var remoteSearches atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded := httptest.NewRecorder()
		wire.serve(recorded, r)
		var data map[string]any
		if json.Unmarshal(recorded.Body.Bytes(), &data) != nil {
			t.Error("invalid protocol response")
			w.WriteHeader(500)
			return
		}
		text := "CONNECTOR-ONLY-CONTENT"
		if changedContent.Load() {
			text = "CONNECTOR-CHANGED-CONTENT"
		}
		if r.URL.Path == "/v1/kb/search" {
			remoteSearches.Add(1)
			hits, _ := data["hits"].([]any)
			for _, hit := range hits {
				hit.(map[string]any)["body"] = text
			}
			// Simulate a broken upstream ACL. Local allowlisting remains mandatory.
			data["hits"] = append(hits, map[string]any{"doc_id": "foreign", "body": "FOREIGN-CONTENT", "url": "https://untrusted.example/original"})
		}
		if r.URL.Path == "/v1/kb/fetch" {
			if document, ok := data["data"].(map[string]any); ok {
				document["body"] = text
				if revokeDuringRead.Swap(false) {
					attachmentPolicy.denied.Store("attachments_download")
				}
			}
		}
		w.WriteHeader(recorded.Code)
		_ = json.NewEncoder(w).Encode(data)
	}))
	defer upstream.Close()
	source, err := provider.NewAttachmentKnowledge(privateIndexConfig(upstream.URL, a.WorkspaceID), a.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := knowledgemodule.NewAttachmentFiles(filepath.Join(t.TempDir(), "originals"))
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	storage := &attachmentRetrievalStorage{ConversationAttachmentStorage: files}
	personal, err := application.NewPersonalConversationHost(repo, toolPolicy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	var attachment, conversation string
	var receipt agentsdk.ConversationKnowledgeResult
	var frozen agentsdk.ConversationStepRequest
	var probe atomic.Value
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		last := in.Messages[len(in.Messages)-1]
		if strings.Contains(string(mustJSON(in)), "FOREIGN-CONTENT") || strings.Contains(string(mustJSON(in)), "ORIGINAL-ONLY-BYTES") {
			t.Error("foreign hit or original bytes reached model")
		}
		switch n {
		case 1:
			if !strings.Contains(in.Messages[0].Content, "[[cite:ID]]") {
				t.Error("attachment-only context lacks citation instruction")
			}
			found := map[string]bool{}
			for _, d := range in.Tools {
				found[d.Key] = true
			}
			if !found["attachment_search"] || !found["attachment_read"] || !found["calculate"] || found["knowledge_attachments"] {
				t.Error("incorrect composed catalog")
			}
			return resultToolCall("attachment_search", "forged", map[string]any{"query": "private", "conversation_id": conversation, "permission_ids": []string{"admin"}}), nil
		case 2:
			if !last.IsError || !strings.Contains(last.Content, "arguments_invalid") || remoteSearches.Load() != 0 {
				t.Error("model chose private scope")
			}
			return resultToolCall("attachment_search", "search", map[string]string{"query": "private"}), nil
		case 3:
			if last.IsError || !strings.Contains(last.Content, "CONNECTOR-ONLY-CONTENT") || !strings.Contains(last.Content, attachment) {
				t.Errorf("search missing actual Connector evidence: %s", last.Content)
			}
			return resultToolCall("attachment_read", "read", map[string]string{"attachment_id": attachment}), nil
		case 4, 5:
			var envelope agentsdk.ConversationToolResult
			if last.IsError || json.Unmarshal([]byte(last.Content), &envelope) != nil || json.Unmarshal(envelope.Content, &receipt) != nil || receipt.ConversationID != conversation || receipt.DocumentID != attachment || receipt.Provider != "agent_conversation_documents" || len(receipt.Citations) != 1 {
				t.Errorf("invalid private receipt: %s", last.Content)
			}
			for _, secret := range []string{"dka_", "aput_", "scope:agent:attachment:", "body_ref", "untrusted.example"} {
				if strings.Contains(last.Content, secret) {
					t.Error("remote capability leaked", secret)
				}
			}
			if n == 4 {
				frozen = in
				return agentsdk.ConversationStepResult{}, fmt.Errorf("freeze before reply")
			}
			if string(mustJSON(in)) != string(mustJSON(frozen)) {
				t.Error("restart replaced frozen input")
			}
			if len(receipt.Citations) == 0 {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("missing citation")
			}
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "CONNECTOR-ONLY-CONTENT [[cite:" + receipt.Citations[0].ID + "]]"}, FinishReason: "stop"}, nil
		default:
			if strings.Contains(string(mustJSON(in)), "CONNECTOR-ONLY-CONTENT") {
				t.Error("private evidence crossed conversation")
			}
			if last.Role == "user" {
				return probe.Load().(agentsdk.ConversationStepResult), nil
			}
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "No private content."}, FinishReason: "stop"}, nil
		}
	}}
	options := application.ConversationOptions{ToolHost: personal, PersonalAuthorizer: toolPolicy, AttachmentStorage: storage, AttachmentAuthorizer: attachmentPolicy, DocumentPoll: 10 * time.Millisecond, AttachmentKnowledge: []agentsdk.ConversationAttachmentKnowledgeBinding{{WorkspaceID: a.WorkspaceID, Knowledge: source}}}
	var service *application.ConversationService
	var api agentsdk.ConversationService
	var closeRPC func()
	start := func() {
		t.Helper()
		service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
		if err != nil {
			t.Fatal(err)
		}
		server, e := agentserver.New(agentserver.Config{APIKey: "synthetic", Conversations: service, ConversationRuntimeID: a.RuntimeID})
		if e != nil {
			t.Fatal(e)
		}
		host := httptest.NewServer(server.Handler())
		binding, e := agentremote.NewFactory(agentremote.Options{BaseURL: host.URL, APIKey: "synthetic", Client: host.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
		if e != nil {
			t.Fatal(e)
		}
		api = binding.(agentsdk.ConversationBinding).Conversations()
		closeRPC = func() { _ = binding.Close(context.Background()); host.Close() }
	}
	start()
	defer func() { closeRPC(); service.Close() }()
	c, err := api.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "private-retrieval"}, a)
	if err != nil {
		t.Fatal(err)
	}
	conversation = c.ID
	att, err := api.(agentsdk.ConversationAttachmentService).UploadAttachment(t.Context(), c.ID, agentsdk.ConversationAttachmentUpload{ClientID: "original", Filename: "原件.txt", Data: []byte("ORIGINAL-ONLY-BYTES")}, a)
	if err != nil {
		t.Fatal(err)
	}
	attachment = att.ID
	if _, err = api.(agentsdk.ConversationAttachmentIndexService).IndexAttachment(t.Context(), c.ID, att.ID, att.Revision, a); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		att, err = api.(agentsdk.ConversationAttachmentService).Attachment(t.Context(), c.ID, att.ID, a)
		if err != nil {
			t.Fatal(err)
		}
		if att.State == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("index not ready", att)
		}
		time.Sleep(10 * time.Millisecond)
	}
	readsBefore := storage.reads.Load()
	run, err := api.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "read", Message: "Read this conversation's private attachment."}, a)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, api, c.ID, run.ID); done.Status != "failed" || done.ErrorCode != "provider_failed" {
		t.Fatalf("freeze failed: %+v", done)
	}
	closeRPC()
	service.Close()
	start()
	attachmentPolicy.denied.Store("attachments_download")
	if _, err = api.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, api, c.ID, run.ID); done.Status != "failed" {
		t.Fatal("revoked source resumed")
	}
	attachmentPolicy.denied.Store("")
	if _, err = api.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	done := waitConversation(t, api, c.ID, run.ID)
	if done.Status != "completed" || len(done.Steps) != 4 || len(done.Steps[2].Calls[0].Citations) != 1 {
		t.Fatalf("citation was not persisted: %+v", done)
	}
	if storage.reads.Load() != readsBefore {
		t.Fatal("retrieval read original bytes")
	}
	messages, err := api.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil {
		t.Fatal(err)
	}
	var messageID string
	for _, m := range messages.Items {
		if m.Role == "assistant" {
			messageID = m.ID
			if len(m.Citations) != 1 || m.Citations[0].ConversationID != c.ID {
				t.Error("message lost citation scope")
			}
		}
	}
	if messageID == "" {
		t.Fatal("missing assistant message")
	}
	reference := done.Steps[2].Calls[0].ResultReference
	if reference == nil {
		t.Fatal("missing result reference")
	}
	for i, call := range []agentsdk.ConversationStepResult{
		resultToolCall("attachment_read", "cross-read", map[string]any{"attachment_id": att.ID}),
		resultToolCall("attachment_search", "cross-search", map[string]any{"query": "private"}),
		resultToolCall("history_search", "history-search", map[string]any{"query": "CONNECTOR"}),
		resultToolCall("history_read", "history-read", map[string]any{"conversation_id": c.ID, "message_id": messageID}),
		resultToolCall("tool_result_read", "result-read", agentsdk.ConversationResultRead{Reference: *reference}),
		resultToolCall("execution_read", "execution-read", agentsdk.ConversationExecutionRead{ConversationID: c.ID, RunID: run.ID}),
	} {
		probe.Store(call)
		other, e := api.Create(t.Context(), agentsdk.ConversationCreate{ClientID: fmt.Sprintf("cross-%d", i)}, a)
		if e != nil {
			t.Fatal(e)
		}
		r, e := api.Send(t.Context(), other.ID, agentsdk.ConversationSend{ClientMessageID: "probe", Message: "Probe source boundary."}, a)
		if e != nil {
			t.Fatal(e)
		}
		result := waitConversation(t, api, other.ID, r.ID)
		if result.Status != "completed" || len(result.Steps) != 2 || len(result.Steps[0].Calls) != 1 {
			t.Fatalf("probe did not actually run: %+v", result)
		}
		if strings.Contains(string(mustJSON(result)), "CONNECTOR-ONLY-CONTENT") {
			t.Fatal("private source leaked in probe")
		}
	}
	assertHidden := func(label string) {
		t.Helper()
		r, e := api.Run(t.Context(), c.ID, run.ID, a)
		if e != nil {
			t.Fatal(e)
		}
		if r.AccessError == "" || strings.Contains(string(mustJSON(r)), "CONNECTOR-ONLY-CONTENT") {
			t.Fatalf("%s did not hide source: %+v", label, r)
		}
	}
	toolPolicy.denied.Store("attachment_read")
	assertHidden("tool revoked")
	toolPolicy.denied.Store("")
	changedContent.Store(true)
	assertHidden("remote content changed")
	changedContent.Store(false)
	revokeDuringRead.Store(true)
	assertHidden("in-flight download revocation")
	attachmentPolicy.denied.Store("")
	otherUser := a
	otherUser.UserID = "other"
	if _, err = api.Run(t.Context(), c.ID, run.ID, otherUser); err == nil {
		t.Fatal("other owner read private run")
	}
	closeRPC()
	service.Close()
	options.AttachmentKnowledge = nil
	start()
	assertHidden("source removed")
	closeRPC()
	service.Close()
	options.AttachmentKnowledge = []agentsdk.ConversationAttachmentKnowledgeBinding{{WorkspaceID: a.WorkspaceID, Knowledge: source}}
	start()
	if restored, e := api.Run(t.Context(), c.ID, run.ID, a); e != nil || restored.AccessError != "" {
		t.Fatal("restored source not readable", e)
	}
	if _, err = api.(agentsdk.ConversationAttachmentService).DeleteAttachment(t.Context(), c.ID, att.ID, att.Revision, a); err != nil {
		t.Fatal(err)
	}
	assertHidden("attachment deleted")
	if storage.reads.Load() != readsBefore {
		t.Fatal("source revalidation read originals")
	}
}
