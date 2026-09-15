package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

const imageInputPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

type imageInputModel struct {
	mu       sync.Mutex
	enabled  bool
	requests []agentsdk.ConversationModelRequest
}

func (m *imageInputModel) GenerateConversation(_ context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	m.mu.Lock()
	m.requests = append(m.requests, in)
	m.mu.Unlock()
	if in.Purpose == "summary" {
		return agentsdk.ConversationModelResult{Content: `{"goal":"inspect image","constraints":[],"facts":[],"decisions":[],"open_items":[]}`, Model: "image-fixture"}, nil
	}
	return agentsdk.ConversationModelResult{Content: "已读取图片。", Model: "image-fixture"}, nil
}

func (m *imageInputModel) ConversationModelCapabilities() agentsdk.ConversationModelCapabilities {
	return agentsdk.ConversationModelCapabilities{ContextTokenLimit: 64_000, ImageInput: m.enabled}
}
func (*imageInputModel) ConversationModelDefaultReasoningEffort() string { return "" }

func (m *imageInputModel) snapshot() []agentsdk.ConversationModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]agentsdk.ConversationModelRequest(nil), m.requests...)
}

type imageInputAttachmentPolicy struct {
	downloads atomic.Int32
	denyAfter atomic.Int32
}

func (p *imageInputAttachmentPolicy) AuthorizeConversationAttachment(_ context.Context, operation string, _ agentsdk.ConversationAuthority) error {
	if agentsdk.ConversationAttachmentPermission(operation) == nil {
		return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.attachment_access_denied"}
	}
	if operation == "attachments_download" {
		count := p.downloads.Add(1)
		if limit := p.denyAfter.Load(); limit > 0 && count > limit {
			return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.attachment_access_denied"}
		}
	}
	return nil
}

func imageInputBytes(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(imageInputPNG)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func imageInputBlocks(id string) []agentsdk.ConversationContentBlock {
	return []agentsdk.ConversationContentBlock{
		{Type: "text", Text: "请读取这张图"},
		{Type: "image", Image: &agentsdk.ConversationImageReference{AttachmentID: id, Detail: "high"}},
	}
}

func findHydratedImage(requests []agentsdk.ConversationModelRequest) *agentsdk.ConversationImageReference {
	for _, request := range requests {
		for _, message := range request.Messages {
			for _, block := range message.ContentBlocks {
				if block.Type == "image" && block.Image != nil && len(block.Image.Data) > 0 {
					return block.Image
				}
			}
		}
	}
	return nil
}

func TestConversationImageInputPersistsReferencesAndRehydratesHistoryAfterRestart(t *testing.T) {
	repo, authority := conversationRepository(t), conversationAuthority()
	files, err := knowledgemodule.NewAttachmentFiles(filepath.Join(t.TempDir(), "attachments"))
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	policy := &imageInputAttachmentPolicy{}
	model := &imageInputModel{enabled: true}
	options := conversationOptions()
	options.AttachmentStorage, options.AttachmentAuthorizer = files, policy
	service, err := conversationassembly.NewService(repo, model, authority.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "image-history"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	data := imageInputBytes(t)
	attachment, err := service.UploadAttachment(t.Context(), conversation.ID, agentsdk.ConversationAttachmentUpload{ClientID: "pixel", Filename: "pixel.png", Data: data}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "image-message", Message: "请读取这张图", Content: imageInputBlocks(attachment.ID)}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if final := waitConversation(t, service, conversation.ID, run.ID); final.Status != "completed" {
		t.Fatalf("image run failed: %+v", final)
	}
	hydrated := findHydratedImage(model.snapshot())
	if hydrated == nil || !bytes.Equal(hydrated.Data, data) || hydrated.AttachmentID != attachment.ID || hydrated.Detail != "high" {
		t.Fatalf("provider did not receive authorized image bytes: %+v", hydrated)
	}
	messages, err := service.Messages(t.Context(), conversation.ID, agentsdk.ConversationMessageQuery{}, authority)
	if err != nil || len(messages.Items) != 2 || len(messages.Items[0].ContentBlocks) != 2 || len(messages.Items[0].ContentBlocks[1].Image.Data) != 0 {
		t.Fatalf("persisted message did not retain a byte-free image reference: %+v %v", messages, err)
	}
	digest := sha256.Sum256(data)
	image := messages.Items[0].ContentBlocks[1].Image
	if image.SHA256 != hex.EncodeToString(digest[:]) || image.ContentType != "image/png" || image.ConversationID != conversation.ID || image.Revision < 1 {
		t.Fatalf("frozen image identity is incomplete: %+v", image)
	}
	snapshot, err := repo.ConversationSourceSnapshot(t.Context(), agentsdk.ConversationRunReference{ConversationID: conversation.ID, RunID: run.ID}, authority)
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := json.Marshal(snapshot)
	if strings.Contains(string(persisted), imageInputPNG) || strings.Contains(string(persisted), `"data"`) {
		t.Fatal("image bytes entered the durable run snapshot")
	}
	service.Close()

	restartedModel := &imageInputModel{enabled: true}
	service, err = conversationassembly.NewService(repo, restartedModel, authority.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	followUp, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "after-restart", Message: "继续分析"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if final := waitConversation(t, service, conversation.ID, followUp.ID); final.Status != "completed" {
		t.Fatalf("history recovery failed: %+v", final)
	}
	rehydrated := findHydratedImage(restartedModel.snapshot())
	if rehydrated == nil || !bytes.Equal(rehydrated.Data, data) || rehydrated.AttachmentID != attachment.ID {
		t.Fatal("restart did not reauthorize and rehydrate historical image")
	}
}

func TestConversationImageInputRequiresDeclaredModelCapabilityBeforeEnqueue(t *testing.T) {
	repo, authority := conversationRepository(t), conversationAuthority()
	files, err := knowledgemodule.NewAttachmentFiles(filepath.Join(t.TempDir(), "attachments"))
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	policy := &imageInputAttachmentPolicy{}
	model := &imageInputModel{}
	options := conversationOptions()
	options.AttachmentStorage, options.AttachmentAuthorizer = files, policy
	service, err := conversationassembly.NewService(repo, model, authority.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "image-capability"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.UploadAttachment(t.Context(), conversation.ID, agentsdk.ConversationAttachmentUpload{ClientID: "spoofed", Filename: "spoofed.png", Data: []byte("this is not a png")}, authority); err == nil {
		t.Fatal("image extension spoofing was accepted")
	}
	attachment, err := service.UploadAttachment(t.Context(), conversation.ID, agentsdk.ConversationAttachmentUpload{ClientID: "pixel", Filename: "pixel.png", Data: imageInputBytes(t)}, authority)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "unsupported", Message: "请读取这张图", Content: imageInputBlocks(attachment.ID)}, authority)
	var coded *agentsdk.Error
	if !errors.As(err, &coded) || coded.Code != "agent.conversation.model_image_input_unsupported" || len(model.snapshot()) != 0 {
		t.Fatal("image was not rejected before enqueue/model call", err)
	}
	messages, readErr := service.Messages(t.Context(), conversation.ID, agentsdk.ConversationMessageQuery{}, authority)
	if readErr != nil || len(messages.Items) != 0 {
		t.Fatal("rejected image created durable conversation messages", readErr)
	}
}

func TestConversationImageInputRechecksAttachmentPermissionBeforeModelCall(t *testing.T) {
	repo, authority := conversationRepository(t), conversationAuthority()
	files, err := knowledgemodule.NewAttachmentFiles(filepath.Join(t.TempDir(), "attachments"))
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	policy := &imageInputAttachmentPolicy{}
	policy.denyAfter.Store(2) // Freeze succeeds; the worker's fresh read is denied.
	model := &imageInputModel{enabled: true}
	options := conversationOptions()
	options.AttachmentStorage, options.AttachmentAuthorizer = files, policy
	service, err := conversationassembly.NewService(repo, model, authority.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "image-revoke"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := service.UploadAttachment(t.Context(), conversation.ID, agentsdk.ConversationAttachmentUpload{ClientID: "pixel", Filename: "pixel.png", Data: imageInputBytes(t)}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "revoked", Message: "请读取这张图", Content: imageInputBlocks(attachment.ID)}, authority)
	if err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, conversation.ID, run.ID)
	if final.Status != "failed" || final.ErrorCode != "attachment_access_denied" || len(model.snapshot()) != 0 {
		t.Fatalf("revoked image reached the model: %+v calls=%d", final, len(model.snapshot()))
	}
	messages, err := service.Messages(t.Context(), conversation.ID, agentsdk.ConversationMessageQuery{}, authority)
	if err != nil || len(messages.Items) != 1 || len(messages.Items[0].ContentBlocks) != 0 || messages.Items[0].AccessError == "" {
		t.Fatalf("revoked historical image remained readable: %+v %v", messages, err)
	}
}
