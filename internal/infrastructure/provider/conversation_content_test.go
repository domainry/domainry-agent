package provider

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

const conversationImagePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestConversationImageInputUsesNativeProtocolBlocksWithoutLeakingReferences(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(conversationImagePNG)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	image := &agentsdk.ConversationImageReference{
		AttachmentID:   "att_0123456789abcdef0123456789abcdef",
		ConversationID: "conv_private_source",
		Filename:       "pixel.png",
		ContentType:    "image/png",
		Bytes:          int64(len(data)),
		SHA256:         hex.EncodeToString(digest[:]),
		Revision:       2,
		Detail:         "high",
		Data:           data,
	}
	blocks := []agentsdk.ConversationContentBlock{{Type: "text", Text: "describe"}, {Type: "image", Image: image}}

	for _, protocol := range []string{ConversationProtocolChat, ConversationProtocolMessages, ConversationProtocolResponses} {
		t.Run(protocol, func(t *testing.T) {
			var body []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer upstream.Close()
			model, err := NewConversationModel(ConversationModelConfig{
				Provider: ConversationProviderCompatible, Protocol: protocol, URL: upstream.URL,
				Model: "vision-model", ImageInput: true, Client: upstream.Client(),
			})
			if err != nil {
				t.Fatal(err)
			}
			input := agentsdk.ConversationModelRequest{
				Purpose: "reply", IdempotencyKey: "image-input", MaxOutputBytes: 1024,
				ModelIdentity: model.ConversationModelIdentity(), ModelCapabilities: model.ConversationModelCapabilities(),
				Messages: []agentsdk.ConversationModelMessage{
					{Role: "system", Content: "instructions"},
					{Role: "user", Content: "describe", ContentBlocks: blocks},
				},
			}
			response, err := model.request(context.Background(), input, false)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			raw := string(body)
			for _, private := range []string{image.AttachmentID, image.ConversationID, image.Filename, image.SHA256, `"revision"`, `"bytes"`} {
				if strings.Contains(raw, private) {
					t.Fatalf("server-only image reference leaked to %s payload: %s", protocol, private)
				}
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			key := "messages"
			messageIndex := 1
			if protocol == ConversationProtocolMessages {
				messageIndex = 0
			}
			if protocol == ConversationProtocolResponses {
				key = "input"
			}
			var messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(payload[key], &messages); err != nil || len(messages) <= messageIndex {
				t.Fatalf("invalid %s multimodal payload: %s (%v)", protocol, body, err)
			}
			var content []map[string]any
			if err := json.Unmarshal(messages[messageIndex].Content, &content); err != nil || len(content) != 2 {
				t.Fatalf("invalid %s multimodal content: %s (%v)", protocol, messages[messageIndex].Content, err)
			}
			textBlock, imageBlock := content[0], content[1]
			switch protocol {
			case ConversationProtocolChat:
				imageURL, _ := imageBlock["image_url"].(map[string]any)
				if textBlock["type"] != "text" || imageBlock["type"] != "image_url" || imageURL["url"] != "data:image/png;base64,"+conversationImagePNG || imageURL["detail"] != "high" {
					t.Fatalf("incorrect chat image blocks: %#v", content)
				}
			case ConversationProtocolMessages:
				source, _ := imageBlock["source"].(map[string]any)
				if textBlock["type"] != "text" || imageBlock["type"] != "image" || source["type"] != "base64" || source["media_type"] != "image/png" || source["data"] != conversationImagePNG {
					t.Fatalf("incorrect Messages image blocks: %#v", content)
				}
			case ConversationProtocolResponses:
				if textBlock["type"] != "input_text" || imageBlock["type"] != "input_image" || imageBlock["image_url"] != "data:image/png;base64,"+conversationImagePNG || imageBlock["detail"] != "high" {
					t.Fatalf("incorrect Responses image blocks: %#v", content)
				}
			}
		})
	}
}

func TestConversationImageInputRejectsMissingCapabilityAndTamperedBytes(t *testing.T) {
	data, _ := base64.StdEncoding.DecodeString(conversationImagePNG)
	digest := sha256.Sum256(data)
	image := &agentsdk.ConversationImageReference{ContentType: "image/png", Bytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), Revision: 1, Detail: "auto", Data: data}
	blocks := []agentsdk.ConversationContentBlock{{Type: "image", Image: image}}
	if err := validateModelContent("user", "", blocks, false); err == nil {
		t.Fatal("image reached a model without image capability")
	}
	image.Data = append([]byte(nil), data...)
	image.Data[len(image.Data)-1] ^= 1
	if err := validateModelContent("user", "", blocks, true); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatal("tampered image bytes were accepted", err)
	}
	image.Data = data
	if err := validateModelContent("assistant", "", blocks, true); err == nil {
		t.Fatal("assistant image input was accepted")
	}
}
