package provider

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

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
	return validateModelContentMode(role, content, blocks, imageInput, true)
}

func validateModelContentForSizing(role, content string, blocks []agentsdk.ConversationContentBlock, imageInput bool) error {
	return validateModelContentMode(role, content, blocks, imageInput, false)
}

func validateModelContentMode(role, content string, blocks []agentsdk.ConversationContentBlock, imageInput, requireData bool) error {
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
			if role != "user" || !imageInput || attachments > agentsdk.TaskAttachmentMaxCount || block.Text != "" || block.File != nil || block.Image == nil {
				return fmt.Errorf("invalid image content block envelope")
			}
			if block.Image.Bytes < 1 || block.Image.Bytes > agentsdk.ConversationAttachmentMaxBytes {
				return fmt.Errorf("invalid image content block size")
			}
			if (requireData && len(block.Image.Data) == 0) || (len(block.Image.Data) > 0 && int64(len(block.Image.Data)) != block.Image.Bytes) {
				return fmt.Errorf("invalid image content block bytes: got=%d want=%d", len(block.Image.Data), block.Image.Bytes)
			}
			if block.Image.Revision < 1 {
				return fmt.Errorf("invalid image content block revision")
			}
			if block.Image.ContentType != "image/png" && block.Image.ContentType != "image/jpeg" && block.Image.ContentType != "image/gif" && block.Image.ContentType != "image/webp" {
				return fmt.Errorf("invalid image content block type")
			}
			if block.Image.Detail != "auto" && block.Image.Detail != "low" && block.Image.Detail != "high" {
				return fmt.Errorf("invalid image content block detail")
			}
			if decoded, err := hex.DecodeString(block.Image.SHA256); err != nil || len(decoded) != sha256.Size {
				return fmt.Errorf("invalid image content hash")
			}
			if len(block.Image.Data) > 0 {
				digest := sha256.Sum256(block.Image.Data)
				if hex.EncodeToString(digest[:]) != block.Image.SHA256 {
					return fmt.Errorf("image content hash mismatch")
				}
			}
		case "file":
			attachments++
			if role != "user" || !imageInput || attachments > agentsdk.TaskAttachmentMaxCount || block.Text != "" || block.Image != nil || block.File == nil || block.File.Bytes < 1 || block.File.Bytes > agentsdk.ConversationAttachmentMaxBytes || block.File.Revision < 1 || block.File.ContentType != "application/pdf" || block.File.Filename == "" {
				return fmt.Errorf("invalid file content block")
			}
			if (requireData && len(block.File.Data) == 0) || (len(block.File.Data) > 0 && int64(len(block.File.Data)) != block.File.Bytes) {
				return fmt.Errorf("invalid file content block bytes")
			}
			if decoded, err := hex.DecodeString(block.File.SHA256); err != nil || len(decoded) != sha256.Size {
				return fmt.Errorf("invalid file content hash")
			}
			if len(block.File.Data) > 0 {
				digest := sha256.Sum256(block.File.Data)
				if hex.EncodeToString(digest[:]) != block.File.SHA256 {
					return fmt.Errorf("file content hash mismatch")
				}
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
	return encodeModelContentMode(protocol, content, blocks, false)
}

func encodeModelContentForSizing(protocol, content string, blocks []agentsdk.ConversationContentBlock) any {
	return encodeModelContentMode(protocol, content, blocks, true)
}

func encodeModelData(data []byte, declared int64, sizing bool) string {
	if sizing && len(data) == 0 {
		return strings.Repeat("A", base64.StdEncoding.EncodedLen(int(declared)))
	}
	return base64.StdEncoding.EncodeToString(data)
}

func encodeModelContentMode(protocol, content string, blocks []agentsdk.ConversationContentBlock, sizing bool) any {
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
			encoded := encodeModelData(file.Data, file.Bytes, sizing)
			fileData := "data:" + file.ContentType + ";base64," + encoded
			switch protocol {
			case ConversationProtocolChat:
				out = append(out, map[string]any{"type": "file", "file": map[string]any{"filename": file.Filename, "file_data": fileData}})
			case ConversationProtocolMessages:
				out = append(out, map[string]any{"type": "document", "source": map[string]any{"type": "base64", "media_type": file.ContentType, "data": encoded}})
			case ConversationProtocolResponses:
				out = append(out, map[string]any{"type": "input_file", "filename": file.Filename, "file_data": fileData})
			}
			continue
		}
		image := block.Image
		encoded := encodeModelData(image.Data, image.Bytes, sizing)
		dataURL := "data:" + image.ContentType + ";base64," + encoded
		switch protocol {
		case ConversationProtocolChat:
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL, "detail": image.Detail}})
		case ConversationProtocolMessages:
			out = append(out, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": image.ContentType, "data": encoded}})
		case ConversationProtocolResponses:
			out = append(out, map[string]any{"type": "input_image", "image_url": dataURL, "detail": image.Detail})
		}
	}
	return out
}
