package application

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"regexp"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

const conversationImageMaxCount = 4

const (
	conversationReferenceMaxCount  = 32
	conversationReferenceMaxBytes  = 16 * 1024
	conversationReferencesMaxBytes = 64 * 1024
)

var conversationImageAttachmentID = regexp.MustCompile(`^art_[a-f0-9]{32}$`)

func conversationImageType(value string) bool {
	switch value {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func conversationContentText(blocks []agentsdk.ConversationContentBlock) string {
	var text strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

func cloneConversationContentBlocks(in []agentsdk.ConversationContentBlock) []agentsdk.ConversationContentBlock {
	out := make([]agentsdk.ConversationContentBlock, len(in))
	for index, block := range in {
		out[index] = block
		if block.Image != nil {
			image := *block.Image
			image.Data = append([]byte(nil), block.Image.Data...)
			if block.Image.Source != nil {
				source := *block.Image.Source
				image.Source = &source
			}
			out[index].Image = &image
		}
	}
	return out
}

func cloneConversationReferences(in []agentsdk.ConversationReference) []agentsdk.ConversationReference {
	out := make([]agentsdk.ConversationReference, len(in))
	for index, reference := range in {
		out[index] = reference
		out[index].Payload = append([]byte(nil), reference.Payload...)
	}
	return out
}

func freezeConversationReferences(in []agentsdk.ConversationReference) ([]agentsdk.ConversationReference, error) {
	if len(in) > conversationReferenceMaxCount {
		return nil, conversationFailure("bad_request", "message_references_invalid")
	}
	out := make([]agentsdk.ConversationReference, 0, len(in))
	total := 0
	for _, reference := range in {
		if !conversationKey(reference.Kind) || len(reference.Payload) == 0 || len(reference.Payload) > conversationReferenceMaxBytes {
			return nil, conversationFailure("bad_request", "message_references_invalid")
		}
		decoder := json.NewDecoder(bytes.NewReader(reference.Payload))
		decoder.UseNumber()
		var payload map[string]any
		if err := decoder.Decode(&payload); err != nil || payload == nil {
			return nil, conversationFailure("bad_request", "message_references_invalid")
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			return nil, conversationFailure("bad_request", "message_references_invalid")
		}
		frozen, err := json.Marshal(payload)
		if err != nil || len(frozen) > conversationReferenceMaxBytes {
			return nil, conversationFailure("bad_request", "message_references_invalid")
		}
		total += len(reference.Kind) + len(frozen)
		if total > conversationReferencesMaxBytes {
			return nil, conversationFailure("bad_request", "message_references_invalid")
		}
		out = append(out, agentsdk.ConversationReference{Kind: reference.Kind, Payload: frozen})
	}
	return out, nil
}

func (s *ConversationService) freezeConversationSendContent(ctx context.Context, conversationID string, in agentsdk.ConversationSend, a agentsdk.ConversationAuthority) (agentsdk.ConversationSend, error) {
	if len(in.Content) == 0 {
		if !conversationText(in.Message, s.options.MaxInputBytes, len(in.References) == 0) {
			return in, conversationFailure("bad_request", "message_invalid")
		}
		return in, nil
	}
	if len(in.Content) > 16 {
		return in, conversationFailure("bad_request", "message_content_invalid")
	}
	frozen := make([]agentsdk.ConversationContentBlock, 0, len(in.Content))
	images, textBytes := 0, 0
	for _, block := range in.Content {
		switch block.Type {
		case "text":
			if block.Image != nil || !conversationText(block.Text, s.options.MaxInputBytes-textBytes, false) {
				return in, conversationFailure("bad_request", "message_content_invalid")
			}
			textBytes += len(block.Text)
			frozen = append(frozen, agentsdk.ConversationContentBlock{Type: "text", Text: block.Text})
		case "image":
			images++
			if images > conversationImageMaxCount || block.Text != "" || block.Image == nil || !conversationImageAttachmentID.MatchString(block.Image.AttachmentID) ||
				block.Image.ConversationID != "" || block.Image.Filename != "" || block.Image.ContentType != "" || block.Image.Bytes != 0 || block.Image.SHA256 != "" || block.Image.Revision != 0 || block.Image.Source != nil || len(block.Image.Data) != 0 ||
				(block.Image.Detail != "" && block.Image.Detail != "auto" && block.Image.Detail != "low" && block.Image.Detail != "high") {
				return in, conversationFailure("bad_request", "message_image_invalid")
			}
			download, err := s.DownloadAttachment(ctx, conversationID, block.Image.AttachmentID, a)
			if err != nil {
				return in, err
			}
			if !conversationImageType(download.Attachment.ContentType) {
				return in, conversationFailure("bad_request", "message_image_type_unsupported")
			}
			detail := block.Image.Detail
			if detail == "" {
				detail = "auto"
			}
			image := agentsdk.ConversationImageReference{
				AttachmentID: download.Attachment.ID, ConversationID: conversationID,
				Filename: download.Attachment.Filename, ContentType: download.Attachment.ContentType,
				Bytes: download.Attachment.Bytes, SHA256: download.Attachment.SHA256,
				Revision: download.Attachment.Revision, Detail: detail,
			}
			frozen = append(frozen, agentsdk.ConversationContentBlock{Type: "image", Image: &image})
		default:
			return in, conversationFailure("bad_request", "message_content_invalid")
		}
	}
	if len(frozen) == 0 || textBytes == 0 && images == 0 {
		return in, conversationFailure("bad_request", "message_content_invalid")
	}
	text := conversationContentText(frozen)
	if in.Message != "" && in.Message != text {
		return in, conversationFailure("bad_request", "message_content_invalid")
	}
	descriptor, err := s.currentConversationModelDescriptor(ctx)
	if err != nil {
		return in, err
	}
	if images > 0 && !descriptor.Capabilities.ImageInput {
		return in, conversationFailure("bad_request", "model_image_input_unsupported")
	}
	in.Message, in.Content = text, frozen
	return in, nil
}

func sameConversationImage(a, b agentsdk.ConversationImageReference) bool {
	return a.AttachmentID == b.AttachmentID && a.ConversationID == b.ConversationID && a.Filename == b.Filename &&
		a.ContentType == b.ContentType && a.Bytes == b.Bytes && a.SHA256 == b.SHA256
}

func (s *ConversationService) readConversationImage(ctx context.Context, image agentsdk.ConversationImageReference, a agentsdk.ConversationAuthority) ([]byte, error) {
	if _, err := hex.DecodeString(image.SHA256); !conversationImageAttachmentID.MatchString(image.AttachmentID) || !conversationKey(image.ConversationID) || !conversationImageType(image.ContentType) || image.Bytes < 1 || image.Bytes > agentsdk.ConversationAttachmentMaxBytes || err != nil || len(image.SHA256) != 64 || image.Revision < 1 ||
		(image.Detail != "auto" && image.Detail != "low" && image.Detail != "high") {
		return nil, conversationFailure("unavailable", "message_image_reference_invalid")
	}
	download, err := s.DownloadAttachment(ctx, image.ConversationID, image.AttachmentID, a)
	if err != nil {
		return nil, err
	}
	current := agentsdk.ConversationImageReference{
		AttachmentID: download.Attachment.ID, ConversationID: download.Attachment.ConversationID,
		Filename: download.Attachment.Filename, ContentType: download.Attachment.ContentType,
		Bytes: download.Attachment.Bytes, SHA256: download.Attachment.SHA256,
	}
	if !sameConversationImage(image, current) || download.Attachment.Revision < image.Revision {
		return nil, conversationFailure("unavailable", "message_image_reference_changed")
	}
	return download.Data, nil
}

func snapshotContainsConversationImage(snapshot persistence.ConversationSourceSnapshot, image agentsdk.ConversationImageReference) bool {
	contains := func(blocks []agentsdk.ConversationContentBlock) bool {
		for _, block := range blocks {
			if block.Image != nil && block.Image.Source == nil && sameConversationImage(*block.Image, image) {
				return true
			}
		}
		return false
	}
	if snapshot.Input != nil {
		for _, message := range snapshot.Input.Messages {
			if contains(message.ContentBlocks) {
				return true
			}
		}
	}
	for _, step := range snapshot.Steps {
		for _, message := range step.Input.Messages {
			if contains(message.ContentBlocks) {
				return true
			}
		}
	}
	return false
}

func (s *ConversationService) reauthorizeSnapshotImages(ctx context.Context, snapshot persistence.ConversationSourceSnapshot) error {
	check := func(blocks []agentsdk.ConversationContentBlock) error {
		for _, block := range blocks {
			if block.Type != "image" || block.Image == nil || block.Image.Source != nil {
				continue
			}
			if _, err := s.readConversationImage(ctx, *block.Image, snapshot.Authority); err != nil {
				return err
			}
		}
		return nil
	}
	if snapshot.Input != nil {
		for _, message := range snapshot.Input.Messages {
			if err := check(message.ContentBlocks); err != nil {
				return err
			}
		}
	}
	for _, step := range snapshot.Steps {
		for _, message := range step.Input.Messages {
			if err := check(message.ContentBlocks); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ConversationService) hydrateConversationImage(ctx context.Context, claim persistence.ConversationClaim, image agentsdk.ConversationImageReference) ([]byte, error) {
	if image.Source == nil {
		if image.ConversationID != claim.Run.ConversationID {
			return nil, conversationFailure("forbidden", "message_image_source_unavailable")
		}
		return s.readConversationImage(ctx, image, claim.Authority)
	}
	ref := *image.Source
	if ref.ConversationID != image.ConversationID || ref.RunID == "" {
		return nil, conversationFailure("unavailable", "message_image_reference_invalid")
	}
	sourceCtx := ctx
	var err error
	if claim.Run.BackgroundTask != nil && claim.Run.BackgroundTask.DelegationID != "" {
		sourceCtx, err = s.delegationRunSourceContext(sourceCtx, claim.Run, claim.Authority)
		if err != nil {
			return nil, err
		}
	}
	if _, err = s.sourceAudit(claim.Authority, claim.Run.ConversationID).run(sourceCtx, ref); err != nil {
		return nil, err
	}
	producer, err := s.sharedEvidenceAuthority(sourceCtx, ref, claim.Authority)
	if err != nil {
		return nil, err
	}
	repo, ok := s.repo.(persistence.ConversationSourceRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "source_read_unavailable")
	}
	snapshot, err := repo.ConversationSourceSnapshot(sourceCtx, ref, producer)
	if err != nil {
		return nil, err
	}
	if !snapshotContainsConversationImage(snapshot, image) {
		return nil, conversationFailure("forbidden", "message_image_source_unavailable")
	}
	return s.readConversationImage(sourceCtx, image, producer)
}

func (s *ConversationService) hydrateConversationBlocks(ctx context.Context, claim persistence.ConversationClaim, blocks []agentsdk.ConversationContentBlock) ([]agentsdk.ConversationContentBlock, error) {
	out := cloneConversationContentBlocks(blocks)
	for index := range out {
		if out[index].Type != "image" || out[index].Image == nil {
			continue
		}
		data, err := s.hydrateConversationImage(ctx, claim, *out[index].Image)
		if err != nil {
			return nil, err
		}
		out[index].Image.Data = data
	}
	return out, nil
}

func (s *ConversationService) hydrateConversationModelRequest(ctx context.Context, claim persistence.ConversationClaim, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelRequest, error) {
	out := in
	out.ConversationID = claim.Run.ConversationID
	out.Messages = append([]agentsdk.ConversationModelMessage(nil), in.Messages...)
	for index := range out.Messages {
		blocks, err := s.hydrateConversationBlocks(ctx, claim, in.Messages[index].ContentBlocks)
		if err != nil {
			return in, err
		}
		out.Messages[index].ContentBlocks = blocks
	}
	return out, nil
}

func (s *ConversationService) hydrateConversationStepRequest(ctx context.Context, claim persistence.ConversationClaim, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepRequest, error) {
	out := in
	out.ConversationID = claim.Run.ConversationID
	out.Messages = append([]agentsdk.ConversationStepMessage(nil), in.Messages...)
	for index := range out.Messages {
		blocks, err := s.hydrateConversationBlocks(ctx, claim, in.Messages[index].ContentBlocks)
		if err != nil {
			return in, err
		}
		out.Messages[index].ContentBlocks = blocks
	}
	return out, nil
}

func conversationBlocksHaveImages(blocks []agentsdk.ConversationContentBlock) bool {
	for _, block := range blocks {
		if block.Type == "image" && block.Image != nil {
			return true
		}
	}
	return false
}

func conversationModelMessagesHaveImages(messages []agentsdk.ConversationModelMessage) bool {
	for _, message := range messages {
		if conversationBlocksHaveImages(message.ContentBlocks) {
			return true
		}
	}
	return false
}

func (s *ConversationService) reauthorizeConversationMessageImages(ctx context.Context, message agentsdk.ConversationMessage, a agentsdk.ConversationAuthority) error {
	if !conversationBlocksHaveImages(message.ContentBlocks) {
		return nil
	}
	run, err := s.repo.Run(ctx, message.ConversationID, message.RunID, a)
	if err != nil {
		return err
	}
	claim := persistence.ConversationClaim{Run: run, Authority: a}
	_, err = s.hydrateConversationBlocks(ctx, claim, message.ContentBlocks)
	return err
}

func (s *ConversationService) inheritedConversationRunImages(ctx context.Context, conversationID, runID string, beforeStep int, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationContentBlock, error) {
	if runID == "" || s.repo == nil {
		return nil, nil
	}
	run, err := s.repo.Run(ctx, conversationID, runID, a)
	if err != nil {
		return nil, err
	}
	history, err := s.repo.History(ctx, conversationID, run.UserSeq-1, run.UserSeq, 1, a)
	if err != nil {
		return nil, err
	}
	if len(history) != 1 || history[0].RunID != runID || history[0].Role != "user" {
		return nil, conversationFailure("unavailable", "message_image_source_unavailable")
	}
	ref := agentsdk.ConversationRunReference{ConversationID: conversationID, RunID: runID, BeforeStep: beforeStep}
	out := []agentsdk.ConversationContentBlock{}
	for _, block := range history[0].ContentBlocks {
		if block.Type != "image" || block.Image == nil {
			continue
		}
		copy := cloneConversationContentBlocks([]agentsdk.ConversationContentBlock{block})[0]
		copy.Image.Source = &ref
		out = append(out, copy)
	}
	return out, nil
}

func (s *ConversationService) requireConversationImageModel(ctx context.Context, blocks []agentsdk.ConversationContentBlock) error {
	if !conversationBlocksHaveImages(blocks) {
		return nil
	}
	descriptor, err := s.currentConversationModelDescriptor(ctx)
	if err != nil {
		return err
	}
	if !descriptor.Capabilities.ImageInput {
		return conversationFailure("bad_request", "model_image_input_unsupported")
	}
	return nil
}
