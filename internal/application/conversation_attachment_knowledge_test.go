package application

import (
	"context"
	"encoding/json"
	knowledge "github.com/domainry/domainry-knowledge/module"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type attachmentKnowledgeTestRepository struct {
	persistence.ConversationRepository
	persistence.ConversationAttachmentRepository
	record persistence.ConversationAttachmentRecord
}

func (r *attachmentKnowledgeTestRepository) Get(_ context.Context, id string, _ agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	return agentsdk.Conversation{ID: id}, nil
}
func (r *attachmentKnowledgeTestRepository) AttachmentKnowledgeRecords(context.Context, string, agentsdk.ConversationAuthority) ([]persistence.ConversationAttachmentRecord, error) {
	return []persistence.ConversationAttachmentRecord{r.record}, nil
}

type attachmentKnowledgeTestPolicy struct{}

func (attachmentKnowledgeTestPolicy) AuthorizeConversationAttachment(context.Context, string, agentsdk.ConversationAuthority) error {
	return nil
}
func (attachmentKnowledgeTestPolicy) AuthorizeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: true}, nil
}

type attachmentKnowledgeTestSource struct {
	agentsdk.ConversationAttachmentKnowledgeSource
}

func (attachmentKnowledgeTestSource) AttachmentKnowledgeSourceIdentity() string {
	return strings.Repeat("a", 64)
}
func (s attachmentKnowledgeTestSource) ResolveAttachmentKnowledge(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentKnowledgeScope, error) {
	return agentsdk.ConversationAttachmentKnowledgeScope{Source: s, PermissionID: "scope:private"}, nil
}
func (s attachmentKnowledgeTestSource) KnowledgeDocumentSourceIdentity() string {
	return s.AttachmentKnowledgeSourceIdentity()
}
func (attachmentKnowledgeTestSource) KnowledgeDocumentAccessPolicySHA256() string {
	return strings.Repeat("b", 64)
}
func (attachmentKnowledgeTestSource) KnowledgeDocumentManagementReady() error { return nil }
func (attachmentKnowledgeTestSource) KnowledgeDocumentMaxBytes() int64        { return 1024 }
func (attachmentKnowledgeTestSource) SearchKnowledgeDocumentPassages(context.Context, string, agentsdk.ConversationAuthority) ([]agentsdk.KnowledgeDocumentPassage, error) {
	return []agentsdk.KnowledgeDocumentPassage{{DocumentID: "remote", Content: "Authorized Connector passage", Location: &agentsdk.DocumentLocation{Sheet: "Actual sheet", Row: 5, Cell: "B5"}}}, nil
}
func (s attachmentKnowledgeTestSource) ReadKnowledgeDocumentPassages(ctx context.Context, _ string, a agentsdk.ConversationAuthority) ([]agentsdk.KnowledgeDocumentPassage, error) {
	return s.SearchKnowledgeDocumentPassages(ctx, "", a)
}

func TestAttachmentReceiptRejectsTamperingAndStaysScoped(t *testing.T) {
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	conversation, id := "conv_"+strings.Repeat("a", 32), "att_"+strings.Repeat("b", 32)
	source := attachmentKnowledgeTestSource{}
	repo := &attachmentKnowledgeTestRepository{record: persistence.ConversationAttachmentRecord{Attachment: agentsdk.ConversationAttachment{ID: id, ConversationID: conversation, Filename: "original.xlsx", State: "ready", SHA256: strings.Repeat("c", 64)}, BodyRef: "must-never-read", Source: &persistence.ConversationAttachmentSource{DocID: "remote", Identity: source.KnowledgeDocumentSourceIdentity(), AccessPolicySHA256: source.KnowledgeDocumentAccessPolicySHA256(), PermissionID: "scope:private"}, Index: &persistence.ConversationAttachmentIndex{Actor: a, IndexObserved: true}}}
	// An embedded nil storage would panic if retrieval attempted original I/O.
	storage := struct {
		agentsdk.ConversationAttachmentStorage
	}{}
	service := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{KnowledgeFactory: knowledge.NewFactory(), AttachmentStorage: storage, AttachmentAuthorizer: attachmentKnowledgeTestPolicy{}, PersonalAuthorizer: attachmentKnowledgeTestPolicy{}, AttachmentKnowledge: []agentsdk.ConversationAttachmentKnowledgeBinding{{WorkspaceID: a.WorkspaceID, Knowledge: source}}}}
	definition, _ := attachmentKnowledgeTool("attachment_read")
	in := agentsdk.ConversationToolRequest{Authority: a, ConversationID: conversation, Definition: definition, Call: agentsdk.ConversationToolCall{Name: definition.Key, Arguments: `{"attachment_id":"` + id + `"}`}}
	receipt, err := service.attachmentKnowledge(t.Context(), conversation, "fetch", "", id, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Citations) != 1 || receipt.Citations[0].Location.Cell != "B5" {
		t.Fatal("source position lost")
	}
	raw, _ := json.Marshal(receipt)
	result := agentsdk.ConversationToolResult{Status: "completed", Content: raw}
	if err = service.authorizeStoredToolResult(t.Context(), in, result); err != nil {
		t.Fatal("valid receipt rejected", err)
	}
	for _, change := range []func(map[string]any){
		func(v map[string]any) { v["conversation_id"] = "conv_" + strings.Repeat("d", 32) },
		func(v map[string]any) { v["extra_private_text"] = "not verified" },
		func(v map[string]any) { v["scope_sha256"] = strings.Repeat("f", 64) },
		func(v map[string]any) {
			v["citations"].([]any)[0].(map[string]any)["url"] = "https://untrusted.example/file"
		},
		func(v map[string]any) {
			v["data"].(map[string]any)["passages"].([]any)[0].(map[string]any)["content"] = "forged"
		},
		func(v map[string]any) {
			v["data"].(map[string]any)["documents"].([]any)[0].(map[string]any)["sha256"] = strings.Repeat("f", 64)
		},
	} {
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		change(value)
		changed, _ := json.Marshal(value)
		if err = service.authorizeStoredToolResult(t.Context(), in, agentsdk.ConversationToolResult{Status: "completed", Content: changed}); err == nil {
			t.Fatal("tampered receipt authorized")
		}
	}
	for _, field := range []string{"user_id", "workspace_id", "conversation_id", "permission_ids", "url", "library_id", "doc_id"} {
		args := map[string]any{"attachment_id": id, field: "forged"}
		encoded, _ := json.Marshal(args)
		forged := in
		forged.Call.Arguments = string(encoded)
		if _, err = attachmentToolArguments(forged); err == nil {
			t.Fatal("model-selected scope accepted", field)
		}
	}
	repo.record.Attachment.State = "deleting"
	if err = service.authorizeStoredToolResult(t.Context(), in, result); err == nil {
		t.Fatal("deleted source authorized")
	}
	repo.record.Attachment.State = "ready"
	service.options.AttachmentKnowledge = nil
	if err = service.authorizeStoredToolResult(t.Context(), in, result); err == nil {
		t.Fatal("removed source authorized")
	}
}
