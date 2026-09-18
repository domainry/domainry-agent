package application

import (
	"bytes"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestNormalizeTaskAttachmentsUsesTaskContract(t *testing.T) {
	t.Parallel()

	body := []byte("image bytes")
	schema := &agentsdk.AgentTaskAttachmentSchema{
		MinItems:            1,
		MaxItems:            1,
		MaxBytes:            int64(len(body)),
		AllowedContentTypes: []string{"image/jpeg"},
		ImageDetail:         "high",
	}
	attachments, err := normalizeTaskAttachments(schema, []agentsdk.TaskAttachment{{
		ContentType: "IMAGE/JPEG", Bytes: int64(len(body)), Detail: "low", Data: body,
	}})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(attachments) != 1 || attachments[0].ContentType != "image/jpeg" || attachments[0].Detail != "high" || !bytes.Equal(attachments[0].Data, body) {
		t.Fatalf("unexpected normalized attachment: %#v", attachments)
	}
	body[0] = 'X'
	if bytes.Equal(attachments[0].Data, body) {
		t.Fatal("attachment bytes were not frozen into a private copy")
	}
}

func TestNormalizeTaskAttachmentsRejectsContractMismatch(t *testing.T) {
	t.Parallel()

	schema := &agentsdk.AgentTaskAttachmentSchema{
		MinItems: 1, MaxItems: 1, MaxBytes: 4,
		AllowedContentTypes: []string{"image/png"},
	}
	if _, err := normalizeTaskAttachments(schema, nil); err == nil {
		t.Fatal("missing required attachment accepted")
	}
	if _, err := normalizeTaskAttachments(schema, []agentsdk.TaskAttachment{{ContentType: "application/pdf", Bytes: 4}}); err == nil {
		t.Fatal("disallowed content type accepted")
	}
	if _, err := normalizeTaskAttachments(schema, []agentsdk.TaskAttachment{{ContentType: "image/png", Bytes: 5}}); err == nil {
		t.Fatal("oversized attachment accepted")
	}
	if _, err := normalizeTaskAttachments(nil, []agentsdk.TaskAttachment{{ContentType: "image/png", Bytes: 1}}); err == nil {
		t.Fatal("text-only task accepted an attachment")
	}
	if attachments, err := normalizeTaskAttachments(nil, nil); err != nil || len(attachments) != 0 {
		t.Fatalf("text-only task rejected empty attachments: %#v, %v", attachments, err)
	}
}
