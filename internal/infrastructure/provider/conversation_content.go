package provider

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func modelContentText(blocks []agentsdk.ConversationContentBlock) string {
	out := ""
	for _, block := range blocks {
		if block.Type == "text" {
			out += block.Text
		}
	}
	return out
}

func validateModelContent(role, content string, blocks []agentsdk.ConversationContentBlock, imageInput bool) error {
	if !validModelText(content) || len(blocks) > 16 {
		return fmt.Errorf("invalid conversation message content")
	}
	if len(blocks) == 0 {
		return nil
	}
	attachments := 0
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Image != nil || block.File != nil || !validModelText(block.Text) {
				return fmt.Errorf("invalid text content block")
			}
		case "image":
			attachments++
			if role != "user" || !imageInput || attachments > agentsdk.TaskAttachmentMaxCount || block.Text != "" || block.File != nil || block.Image == nil || len(block.Image.Data) == 0 || int64(len(block.Image.Data)) != block.Image.Bytes || block.Image.Revision < 1 ||
				(block.Image.ContentType != "image/png" && block.Image.ContentType != "image/jpeg" && block.Image.ContentType != "image/gif" && block.Image.ContentType != "image/webp") ||
				(block.Image.Detail != "auto" && block.Image.Detail != "low" && block.Image.Detail != "high") {
				return fmt.Errorf("invalid image content block")
			}
			digest := sha256.Sum256(block.Image.Data)
			if hex.EncodeToString(digest[:]) != block.Image.SHA256 {
				return fmt.Errorf("image content hash mismatch")
			}
		case "file":
			attachments++
			if role != "user" || !imageInput || attachments > agentsdk.TaskAttachmentMaxCount || block.Text != "" || block.Image != nil || block.File == nil || len(block.File.Data) == 0 || int64(len(block.File.Data)) != block.File.Bytes || block.File.Revision < 1 || block.File.ContentType != "application/pdf" || block.File.Filename == "" {
				return fmt.Errorf("invalid file content block")
			}
			digest := sha256.Sum256(block.File.Data)
			if hex.EncodeToString(digest[:]) != block.File.SHA256 {
				return fmt.Errorf("file content hash mismatch")
			}
		default:
			return fmt.Errorf("unsupported content block")
		}
	}
	text := modelContentText(blocks)
	if attachments == 0 && text == "" || content != text {
		return fmt.Errorf("content block text mismatch")
	}
	return nil
}

func encodeModelContent(protocol, content string, blocks []agentsdk.ConversationContentBlock) any {
	if len(blocks) == 0 {
		return content
	}
	out := make([]any, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" {
			typeName := "text"
			if protocol == ConversationProtocolResponses {
				typeName = "input_text"
			}
			out = append(out, map[string]any{"type": typeName, "text": block.Text})
			continue
		}
		if block.Type == "file" {
			file := block.File
			fileData := "data:" + file.ContentType + ";base64," + base64.StdEncoding.EncodeToString(file.Data)
			switch protocol {
			case ConversationProtocolChat:
				out = append(out, map[string]any{"type": "file", "file": map[string]any{"filename": file.Filename, "file_data": fileData}})
			case ConversationProtocolMessages:
				out = append(out, map[string]any{"type": "document", "source": map[string]any{"type": "base64", "media_type": file.ContentType, "data": base64.StdEncoding.EncodeToString(file.Data)}})
			case ConversationProtocolResponses:
				out = append(out, map[string]any{"type": "input_file", "filename": file.Filename, "file_data": fileData})
			}
			continue
		}
		image := block.Image
		dataURL := "data:" + image.ContentType + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
		switch protocol {
		case ConversationProtocolChat:
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL, "detail": image.Detail}})
		case ConversationProtocolMessages:
			out = append(out, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": image.ContentType, "data": base64.StdEncoding.EncodeToString(image.Data)}})
		case ConversationProtocolResponses:
			out = append(out, map[string]any{"type": "input_image", "image_url": dataURL, "detail": image.Detail})
		}
	}
	return out
}
