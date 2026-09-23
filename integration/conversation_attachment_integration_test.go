package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
)

type attachmentTestPolicy struct{ denied atomic.Value }

func (p *attachmentTestPolicy) AuthorizeConversationAttachment(_ context.Context, operation string, _ agentsdk.ConversationAuthority) error {
	denied, _ := p.denied.Load().(string)
	if denied == operation || agentsdk.ConversationAttachmentPermission(operation) == nil {
		return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.attachment_access_denied"}
	}
	return nil
}

func TestAttachmentsSaaSBinaryRoundTripPermissionsAndDeferredCleanup(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	policy := &attachmentTestPolicy{}
	options := application.ConversationOptions{AttachmentAuthorizer: policy}
	service, err := conversationassembly.NewService(repo, nil, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if service != nil {
			service.Close()
		}
	}()
	server, err := agentserver.New(agentserver.Config{APIKey: "attachment-saas-fixture", Conversations: service, ConversationRuntimeID: a.RuntimeID})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(server.Handler())
	defer upstream.Close()
	binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: upstream.URL, APIKey: "attachment-saas-fixture", Client: upstream.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close(context.Background())
	conversations := binding.(agentsdk.ConversationBinding).Conversations()
	api := conversations.(agentsdk.ConversationAttachmentService)
	conversation, err := conversations.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "attachment-parent"}, a)
	if err != nil {
		t.Fatal(err)
	}
	input := agentsdk.ConversationAttachmentUpload{ClientID: "upload-1", Filename: "私有资料.txt", Data: bytes.Repeat([]byte("private source\n"), 240000)}
	created, err := api.UploadAttachment(t.Context(), conversation.ID, input, a)
	if err != nil || created.State != "stored" || created.Bytes != int64(len(input.Data)) || created.Visibility != "conversation_private" {
		t.Fatal("upload did not round trip beyond prior 2 MiB RPC limit", created, err)
	}
	public, _ := json.Marshal(created)
	if strings.Contains(string(public), "body_ref") || strings.Contains(string(public), "private source") {
		t.Fatal("public metadata contains private storage data")
	}
	replay, err := api.UploadAttachment(t.Context(), conversation.ID, input, a)
	if err != nil || replay.ID != created.ID || replay.Revision != created.Revision {
		t.Fatal("upload duplicated", err)
	}
	changed := input
	changed.Data = []byte("different")
	_, err = api.UploadAttachment(t.Context(), conversation.ID, changed, a)
	artifactErrorClass(t, err, "conflict")
	download, err := api.DownloadAttachment(t.Context(), conversation.ID, created.ID, a)
	if err != nil || !bytes.Equal(download.Data, input.Data) {
		t.Fatal("download corrupted original", err)
	}
	otherConversation, err := conversations.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "other-parent"}, a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.DownloadAttachment(t.Context(), otherConversation.ID, created.ID, a)
	artifactErrorClass(t, err, "not_found")
	other := a
	other.UserID = "another-user"
	_, err = api.Attachment(t.Context(), conversation.ID, created.ID, other)
	artifactErrorClass(t, err, "not_found")
	policy.denied.Store("attachments_download")
	_, err = api.DownloadAttachment(t.Context(), conversation.ID, created.ID, a)
	artifactErrorClass(t, err, "forbidden")
	policy.denied.Store("")
	messages, err := conversations.Messages(t.Context(), conversation.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(messages.Items) != 0 {
		t.Fatal("file bytes were inserted into transcript", err)
	}
	_, err = api.DeleteAttachment(t.Context(), conversation.ID, created.ID, created.Revision+1, a)
	artifactErrorClass(t, err, "conflict")
	deleted, err := api.DeleteAttachment(t.Context(), conversation.ID, created.ID, created.Revision, a)
	if err != nil || deleted.State != "deleting" {
		t.Fatal("deletion pretended physical success", deleted, err)
	}
	_, err = api.DownloadAttachment(t.Context(), conversation.ID, created.ID, a)
	artifactErrorClass(t, err, "not_found")
	page, err := api.Attachments(t.Context(), conversation.ID, "", 20, a)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("deleting upload remained listed", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		record, err := repo.AttachmentRecord(t.Context(), created.ID, a)
		if err != nil {
			t.Fatal(err)
		}
		if record.Attachment.State == "deleted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restart did not finish cleanup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = repo.AttachmentContent(t.Context(), created.ID, a); err == nil {
		t.Fatal("deleted shared Artifact content remained readable")
	}
}
