package application

import (
	"context"
	"encoding/json"

	"github.com/domainry/domainry-agent-sdk/persistence"
)

// A released business/knowledge result can depend on the same delegation's
// working context. Validate that context using current collaboration/source
// rights without requiring permission to run its communication/mutation tool.
// The normal typed traversal still checks every nested source. This permission
// is only for provenance; raw collaboration receipts use the ordinary path.
func (audit *conversationSourceAudit) deliveryCollaborationSource(ctx context.Context, record persistence.ConversationToolExecution) (bool, error) {
	scope := releasedSourcePurpose(ctx)
	if scope == "" || rawExecutionSource(ctx) || record.Result == nil || record.Result.ResourceID != scope {
		return false, nil
	}
	switch record.Call.Name {
	case "agent_delegate", "delegation_get", "delegation_update", "agent_message", "delegation_disagreement":
	default:
		return false, nil
	}
	definition, ok := collaborationTool(record.Call.Name)
	if !ok || conversationDigest(definition) != conversationDigest(record.Definition) || record.Result.Status != "completed" || record.Result.ErrorCode != "" {
		return false, conversationFailure("conflict", "source_reference_invalid")
	}
	var resource struct {
		ID           string `json:"id"`
		DelegationID string `json:"delegation_id"`
	}
	if json.Unmarshal(record.Result.Content, &resource) != nil {
		return false, conversationFailure("conflict", "source_reference_invalid")
	}
	if record.Call.Name == "agent_message" {
		resource.ID = resource.DelegationID
	}
	if record.Call.Name != "delegation_disagreement" && resource.ID != scope {
		return false, conversationFailure("conflict", "source_reference_invalid")
	}
	if err := audit.authorizeReleasedSourceRead(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func deliveryResultSourceContext(ctx context.Context, id string, record persistence.ConversationToolExecution) context.Context {
	if _, local := collaborationTool(record.Call.Name); local {
		// A deliberately released raw collaboration receipt may contain
		// messages or execution detail. It does not inherit the narrower
		// provenance-only exception used to validate another result.
		return deliverySourceContext(ctx, "")
	}
	return deliverySourceContext(ctx, id)
}
