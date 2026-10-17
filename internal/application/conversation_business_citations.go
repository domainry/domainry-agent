package application

import (
	"encoding/json"
	"strings"

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
	return agentsdk.ConversationBusinessRecordCitations(source, operation, objectKey, records, 0)
}

func businessCitationsEqual(left, right []toolsdk.Citation) bool {
	return agentsdk.ConversationCitationsEqual(left, right)
}
