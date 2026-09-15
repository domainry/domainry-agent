package application

import (
	"context"
	"encoding/json"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Older step inputs flattened scoped tool output into naked original runs.
// Recover only a reference explained by an actual successful earlier receipt
// in this same immutable snapshot. The receipt's typed traversal still checks
// current permissions, publication, exact original bytes and cycle guards.
func (audit *conversationSourceAudit) earlierScopedSource(ctx context.Context, owner sdk.ConversationRunReference, snapshot persistence.ConversationSourceSnapshot, step int, source sdk.ConversationRunReference) (bool, []sdk.ConversationRunReference, error) {
	if source.ConversationID == "" || source.RunID == "" || source.BeforeStep < 0 || source.BeforeStep > 257 {
		return false, nil, nil
	}
	for _, record := range snapshot.Calls {
		if record.Step >= step || record.State != "completed" || record.Result == nil || record.Result.Status != "completed" || record.Result.ErrorCode != "" {
			continue
		}
		var explained []sdk.ConversationRunReference
		switch record.Call.Name {
		case "delegation_execution_read":
			var args sdk.ConversationDelegationExecutionRead
			if json.Unmarshal([]byte(record.Call.Arguments), &args) == nil {
				explained = append(explained, args.Reference)
			}
		case "delegation_execution_result_read":
			var args sdk.ConversationDelegationExecutionResultRead
			if json.Unmarshal([]byte(record.Call.Arguments), &args) == nil {
				ref := args.Read.Reference
				explained = append(explained, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2})
			}
		case "agent_delegate", "delegation_get", "delegation_update":
			var saved sdk.ConversationDelegationDetail
			if json.Unmarshal(record.Result.Content, &saved) != nil {
				continue
			}
			if saved.Task != nil && saved.Task.Result != nil && saved.Task.ExecutionRunID != "" {
				explained = append(explained, sdk.ConversationRunReference{ConversationID: saved.ConversationID, RunID: saved.Task.ExecutionRunID})
			}
			if saved.Delivery != nil {
				explained = append(explained, saved.Delivery.Evidence...)
				for _, condition := range saved.Delivery.Conditions {
					for _, ref := range condition.Receipts {
						explained = append(explained, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2})
					}
				}
			}
		}
		for _, ref := range explained {
			if ref.ConversationID == owner.ConversationID && ref.RunID == owner.RunID || !sourcePrefixContains(ref, source) {
				continue
			}
			ctx = context.WithValue(ctx, conversationAgentContextKey{}, snapshot.Run.Agent)
			roots, err := audit.record(ctx, owner, record)
			return true, roots, err
		}
	}
	return false, nil, nil
}
