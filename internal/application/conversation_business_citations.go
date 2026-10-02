package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

func businessEvidenceCitations(evidence agentsdk.ConversationBusinessEvidence) ([]toolsdk.Citation, error) {
	var input struct {
		ObjectKey string `json:"object_key"`
	}
	if json.Unmarshal(evidence.Input, &input) != nil {
		return nil, conversationFailure("conflict", "business_response_invalid")
	}
	switch evidence.Operation {
	case "business_catalog", "invoke_action", "workflow_start", "workflow_get":
		return nil, nil
	case "get_record":
		var record agentsdk.ConversationBusinessRecord
		if json.Unmarshal(evidence.Data, &record) != nil {
			return nil, conversationFailure("conflict", "business_response_invalid")
		}
		return businessResultCitations(evidence.Source, evidence.Operation, input.ObjectKey, record), nil
	case "query_records":
		var page agentsdk.ConversationBusinessRecordPage
		if json.Unmarshal(evidence.Data, &page) != nil {
			return nil, conversationFailure("conflict", "business_response_invalid")
		}
		return businessResultCitations(evidence.Source, evidence.Operation, input.ObjectKey, page), nil
	case "query_related_records":
		var page agentsdk.ConversationBusinessRelatedPage
		if json.Unmarshal(evidence.Data, &page) != nil {
			return nil, conversationFailure("conflict", "business_response_invalid")
		}
		return businessResultCitations(evidence.Source, evidence.Operation, page.ObjectKey, page), nil
	default:
		return nil, conversationFailure("conflict", "business_response_invalid")
	}
}

func businessResultCitations(source, operation, objectKey string, output any) []toolsdk.Citation {
	var records []agentsdk.ConversationBusinessRecord
	switch value := output.(type) {
	case agentsdk.ConversationBusinessRecord:
		records = []agentsdk.ConversationBusinessRecord{value}
	case agentsdk.ConversationBusinessRecordPage:
		records = value.Items
		if objectKey == "" {
			objectKey = value.ObjectKey
		}
	case agentsdk.ConversationBusinessRelatedPage:
		records = value.Items
		if objectKey == "" {
			objectKey = value.ObjectKey
		}
	default:
		return nil
	}
	if strings.TrimSpace(objectKey) == "" {
		return nil
	}
	return businessRecordCitations(source, operation, objectKey, records)
}

func businessRecordCitations(source, operation, objectKey string, records []agentsdk.ConversationBusinessRecord) []toolsdk.Citation {
	if len(records) == 0 {
		return nil
	}
	source, operation, objectKey = strings.TrimSpace(source), strings.TrimSpace(operation), strings.TrimSpace(objectKey)
	if len(source) > 255 {
		source = "business:" + conversationDigest(source)[:32]
	}
	values := make([]toolsdk.Citation, 0, len(records))
	for _, record := range records {
		raw, _ := json.Marshal(record.Data)
		digest := sha256.Sum256([]byte(strings.Join([]string{source, operation, objectKey, record.ID, record.Version, string(raw)}, "\x00")))
		values = append(values, toolsdk.Citation{
			ID: "business_" + hex.EncodeToString(digest[:16]), Source: source, Operation: operation,
			ObjectKey: objectKey, RecordID: record.ID, Title: businessCitationTitle(record), Excerpt: businessCitationExcerpt(raw),
		})
	}
	return values
}

func businessCitationTitle(record agentsdk.ConversationBusinessRecord) string {
	for _, key := range []string{"name", "subject", "title"} {
		var value string
		if json.Unmarshal(record.Data[key], &value) == nil && strings.TrimSpace(value) != "" {
			return businessCitationText(strings.TrimSpace(value), 512)
		}
	}
	return ""
}

func businessCitationExcerpt(raw []byte) string {
	return strings.TrimSpace(businessCitationText(string(raw), 3072))
}

func businessCitationText(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func businessCitationsEqual(left, right []toolsdk.Citation) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftRaw) == string(rightRaw)
}
