package application

import (
	"context"
	"encoding/json"
	"strings"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func executionCollaborationTool(key string) bool {
	switch key {
	case "delegation_execution_publish", "delegation_executions", "delegation_execution_read", "delegation_execution_result_read":
		return true
	}
	return false
}

func (s *ConversationService) invokeDelegationExecutionTool(ctx context.Context, in sdk.ConversationToolRequest, clientID string) (any, string, error) {
	switch in.Definition.Key {
	case "delegation_execution_publish":
		var args sdk.ConversationDelegationExecutionPublish
		if err := json.Unmarshal([]byte(in.Call.Arguments), &args); err != nil {
			return nil, "", err
		}
		args.Publication.ClientID = clientID
		value, err := s.PublishConversationDelegationExecution(ctx, args.ID, args.Publication, in.Authority)
		return value, args.ID, err
	case "delegation_executions":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(in.Call.Arguments), &args); err != nil {
			return nil, "", err
		}
		values, err := s.ConversationDelegationExecutions(ctx, args.ID, in.Authority)
		return sdk.ConversationDelegationExecutionIndex{DelegationID: args.ID, Publications: values}, args.ID, err
	case "delegation_execution_read":
		var args sdk.ConversationDelegationExecutionRead
		if err := json.Unmarshal([]byte(in.Call.Arguments), &args); err != nil {
			return nil, "", err
		}
		if args.Reference.ConversationID == in.ConversationID && args.Reference.RunID == in.RunID {
			return nil, args.ID, conversationFailure("forbidden", "execution_self_reference")
		}
		value, err := s.ReadConversationDelegationExecution(ctx, args.ID, args.Reference, in.Authority)
		return sdk.ConversationDelegationExecutionView{DelegationID: args.ID, Run: value}, args.ID, err
	case "delegation_execution_result_read":
		var args sdk.ConversationDelegationExecutionResultRead
		if err := json.Unmarshal([]byte(in.Call.Arguments), &args); err != nil {
			return nil, "", err
		}
		ref := args.Read.Reference
		if ref.ConversationID == in.ConversationID && ref.RunID == in.RunID && ref.Step >= in.Step {
			return nil, args.ID, conversationFailure("forbidden", "execution_self_reference")
		}
		value, err := s.ReadConversationDelegationExecutionResult(ctx, args.ID, args.Read, in.Authority)
		return sdk.ConversationDelegationExecutionResult{DelegationID: args.ID, Result: value}, args.ID, err
	}
	return nil, "", conversationFailure("bad_request", "execution_tool_invalid")
}

// A scoped execution read remains a scoped read when its recorded output is
// used in another model turn, history, a delivery or a nested result wrapper.
// Share the parent's traversal guards so wrappers cannot hide reference cycles.
func (audit *conversationSourceAudit) executionToolRecord(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) ([]sdk.ConversationRunReference, error) {
	definition, ok := collaborationTool(record.Call.Name)
	if !ok || conversationDigest(definition) != conversationDigest(record.Definition) || record.Result.ErrorCode != "" {
		return nil, invalidPersonalReceipt()
	}
	if err := audit.connectedTool(ctx, definition.Key); err != nil {
		return nil, err
	}
	id := ""
	var body struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(record.Call.Arguments), &body) != nil || !conversationKey(body.ID) || record.Result.ResourceID != body.ID {
		return nil, invalidPersonalReceipt()
	}
	id = body.ID
	switch record.Call.Name {
	case "delegation_execution_publish":
		var args sdk.ConversationDelegationExecutionPublish
		var saved sdk.ConversationExecutionPublication
		if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil || decodePersonalReceipt(record.Result.Content, &saved) != nil || saved.Reference != args.Publication.Reference || saved.Withdrawn != args.Publication.Withdraw || saved.Revision != args.Publication.ExpectedRevision+1 || saved.Publisher != audit.evidenceAuthority(owner) {
			return nil, invalidPersonalReceipt()
		}
		// This is only a write receipt, with no business contents. Revocation
		// must not require reading old data or retaining publication rights.
		if err := audit.s.authorizeCollaborationID(ctx, id, "view", audit.a); err != nil {
			return nil, err
		}
	case "delegation_executions":
		var saved sdk.ConversationDelegationExecutionIndex
		if decodePersonalReceipt(record.Result.Content, &saved) != nil || saved.DelegationID != id || len(saved.Publications) > 32 {
			return nil, invalidPersonalReceipt()
		}
		current, err := audit.s.ConversationDelegationExecutions(ctx, id, audit.a)
		if err != nil {
			return nil, err
		}
		seen := map[sdk.ConversationRunReference]bool{}
		for _, publication := range saved.Publications {
			found := false
			for _, value := range current {
				found = found || conversationDigest(value) == conversationDigest(publication)
			}
			if !found || seen[publication.Reference] {
				return nil, conversationFailure("forbidden", "execution_not_shared")
			}
			seen[publication.Reference] = true
		}
	case "delegation_execution_read":
		var args sdk.ConversationDelegationExecutionRead
		var saved sdk.ConversationDelegationExecutionView
		if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil || decodePersonalReceipt(record.Result.Content, &saved) != nil || saved.DelegationID != id || args.Reference.ConversationID == owner.ConversationID && args.Reference.RunID == owner.RunID {
			return nil, invalidPersonalReceipt()
		}
		current, err := audit.s.readDelegationExecution(ctx, id, args.Reference, audit.a, audit.childSourceAudit())
		if err != nil {
			return nil, err
		}
		if !verifiedExecutionObservation(saved.Run, current) {
			return nil, conversationFailure("conflict", "source_snapshot_changed")
		}
	case "delegation_execution_result_read":
		var args sdk.ConversationDelegationExecutionResultRead
		var saved sdk.ConversationDelegationExecutionResult
		if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil || decodePersonalReceipt(record.Result.Content, &saved) != nil || saved.DelegationID != id {
			return nil, invalidPersonalReceipt()
		}
		ref := args.Read.Reference
		if ref.ConversationID == owner.ConversationID && ref.RunID == owner.RunID && ref.Step >= record.Step {
			return nil, invalidPersonalReceipt()
		}
		current, err := audit.s.delegationExecutionResultRecord(ctx, id, args.Read.Reference, audit.a, audit.childSourceAudit())
		if err != nil {
			return nil, err
		}
		if err := verifyDelegationSourcePage(sdk.ConversationDelegationSourceRead{ID: id, ConversationResultRead: args.Read}, sdk.ConversationDelegationSourceSlice{DelegationID: id, ConversationResultSlice: saved.Result}, *current.Result); err != nil {
			return nil, err
		}
	}
	// Keep the scoped tool receipt as provenance. Every later traversal must
	// verify this exact publication and observation/page again, rather than
	// treating the underlying run as an ordinary private execution source.
	return []sdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: record.Step + 2}}, nil
}

func verifiedExecutionObservation(saved, current sdk.ConversationRun) bool {
	if saved.Interaction != nil || saved.WriteScope != nil || saved.AccessError != "" {
		return false
	}
	if saved.Status != "running" && saved.Status != "queued" {
		return conversationDigest(saved) == conversationDigest(current)
	}
	// While execution advances, earlier completed steps/calls remain exact.
	// Validate text prefixes and every public payload instead of replacing an
	// old observation with the newer run or requiring an unchanged live hash.
	if saved.ID != current.ID || saved.ConversationID != current.ConversationID || saved.Attempt != current.Attempt || saved.ClientMessageID != current.ClientMessageID || saved.UserSeq != current.UserSeq || conversationDigest(saved.Agent) != conversationDigest(current.Agent) || conversationDigest(saved.BackgroundTask) != conversationDigest(current.BackgroundTask) || !strings.HasPrefix(current.DraftText, saved.DraftText) || len(saved.Steps) > len(current.Steps) {
		return false
	}
	for i, step := range saved.Steps {
		actual := current.Steps[i]
		if step.Number != actual.Number || step.Attempt != actual.Attempt || !strings.HasPrefix(actual.Text, step.Text) || len(step.Calls) > len(actual.Calls) {
			return false
		}
		for j, call := range step.Calls {
			other := actual.Calls[j]
			if call.Status == "completed" && call.AccessError == "" {
				if conversationDigest(call) != conversationDigest(other) {
					return false
				}
			} else {
				public := sdk.ConversationToolView{ID: other.ID, Name: other.Name, Effect: other.Effect, Status: call.Status, StartedAt: call.StartedAt, CompletedAt: call.CompletedAt, DurationMilliseconds: call.DurationMilliseconds, AccessError: "execution_result_not_readable"}
				if conversationDigest(call) != conversationDigest(public) {
					return false
				}
			}
		}
	}
	return true
}
