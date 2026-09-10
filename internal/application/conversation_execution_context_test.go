package application

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestResultPreviewPreservesOutcomeAndBoundsEscapedUnicode(t *testing.T) {
	for _, status := range []string{"completed", "failed", "pending"} {
		content, _ := json.Marshal(map[string]string{"details": strings.Repeat("中文\n\"\\", 4000)})
		result := agentsdk.ConversationToolResult{Status: status, ResourceID: "resource-waiting-review", ErrorCode: "known-error", Content: content}
		ref := agentsdk.ConversationResultReference{ConversationID: "c", RunID: "r", CallID: "call", SHA256: conversationDigest(result)}
		for _, budget := range []int{1024, 2048, 8192} {
			text, err := previewConversationResult(result, ref, budget)
			if err != nil || len(text) > budget || !json.Valid([]byte(text)) {
				t.Fatalf("preview budget=%d bytes=%d err=%v", budget, len(text), err)
			}
			var preview conversationResultPreview
			if json.Unmarshal([]byte(text), &preview) != nil || preview.ContentComplete || preview.Status != status || preview.ErrorCode != result.ErrorCode || preview.ResourceID != result.ResourceID || preview.Reference != ref || !utf8.ValidString(preview.JSONPreview) || preview.PreviewBytes != len(preview.JSONPreview) {
				t.Fatal("preview changed an outcome or lost a resource")
			}
			full, _ := json.Marshal(result)
			if preview.TotalBytes != len(full) || !strings.HasPrefix(string(full), preview.JSONPreview) || preview.PreviewBytes == 0 {
				t.Fatal("excerpt is not a real prefix of the full stored result")
			}
		}
	}
}
