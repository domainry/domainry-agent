package agent

import (
	"context"
	"database/sql"
	"strconv"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// The scope is a bounded set of frozen logical calls. Deriving every field in
// this transaction prevents a browser list, partial model stream or stale
// application snapshot from changing the operations the user approves.
func frozenConfirmationOperations(step persistence.ConversationExecutionStep, wait persistence.ConversationWait) ([]agentsdk.ConversationInteractionOperation, error) {
	ids := wait.OperationCallIDs
	if len(ids) == 0 {
		return nil, nil
	}
	invalid := func() ([]agentsdk.ConversationInteractionOperation, error) {
		return nil, conversationError("bad_request", "interaction_scope_invalid")
	}
	if wait.Kind != "confirmation" || len(ids) < 2 || len(ids) > 20 || ids[0] != wait.CallID || step.Result == nil || step.Number != wait.Step {
		return invalid()
	}
	var operations []agentsdk.ConversationInteractionOperation
	for _, call := range step.Result.Message.ToolCalls {
		if len(operations) == len(ids) {
			break
		}
		if call.ID != ids[len(operations)] {
			continue
		}
		var definition agentsdk.ConversationToolDefinition
		for _, candidate := range step.Input.Tools {
			if candidate.Key == call.Name {
				definition = candidate
				break
			}
		}
		if definition.Effect != "write" || definition.Version == "" || definition.ActionKey == "" {
			return invalid()
		}
		operations = append(operations, agentsdk.ConversationInteractionOperation{CallID: call.ID, Tool: call.Name, ToolVersion: definition.Version, ActionKey: definition.ActionKey, Arguments: call.Arguments, ArgumentsHash: conversationHash(call.Arguments), DefinitionHash: conversationHash(definition)})
	}
	if len(operations) != len(ids) {
		return invalid()
	}
	return operations, nil
}

func (s *ConversationStore) approveListedOperations(ctx context.Context, tx *sql.Tx, row conversationRunRow, root *agentsdk.ConversationInteraction) error {
	claim := persistence.ConversationClaim{Authority: row.Authority, Run: row.Run}
	var step persistence.ConversationExecutionStep
	found, err := s.executionRead(ctx, tx, "_agent_conversation_steps", executionScope(claim, root.Step), &step)
	if err != nil {
		return err
	}
	if !found || root.Status != "approved" || root.RespondedAt == nil || root.RespondedBy != row.Authority.UserID {
		return conversationError("conflict", "interaction_scope_invalid")
	}
	ids := make([]string, 0, len(root.Operations))
	for _, operation := range root.Operations {
		ids = append(ids, operation.CallID)
	}
	derived, err := frozenConfirmationOperations(step, persistence.ConversationWait{Step: root.Step, CallID: root.CallID, Kind: "confirmation", OperationCallIDs: ids})
	if err != nil {
		return err
	}
	if len(derived) < 2 || conversationHash(derived) != conversationHash(root.Operations) {
		return conversationError("conflict", "interaction_scope_invalid")
	}
	for _, operation := range derived {
		var call persistence.ConversationToolExecution
		started, err := s.readExecutionTool(ctx, tx, claim, root.Step, operation.CallID, &call)
		if err != nil {
			return err
		}
		if started {
			return conversationError("conflict", "tool_already_started")
		}
		if operation.CallID == root.CallID {
			continue
		}
		_, exists, err := s.readInteraction(ctx, tx, claim, root.Step, operation.CallID, "confirmation")
		if err != nil {
			return err
		}
		if exists {
			return conversationError("conflict", "interaction_closed")
		}
		// Each approved call keeps the existing exact-target confirmation
		// contract used by local mutations and remote business hosts. The
		// parent identifies the single authenticated user response that granted
		// them; the ordinary call/idempotency ledger prevents repeated effects.
		child := agentsdk.ConversationInteraction{ID: conversationID("cint_"), ConversationID: root.ConversationID, RunID: root.RunID, Step: root.Step, CallID: operation.CallID, Kind: "confirmation", Status: "approved", Question: "已包含在本次明确授权的操作范围内。", Tool: operation.Tool, ToolVersion: operation.ToolVersion, ActionKey: operation.ActionKey, Arguments: operation.Arguments, ArgumentsHash: operation.ArgumentsHash, DefinitionHash: operation.DefinitionHash, Revision: 1, RespondedBy: root.RespondedBy, CreatedAt: root.CreatedAt, ExpiresAt: root.ExpiresAt, RespondedAt: root.RespondedAt, AuthorizationID: root.ID}
		if err := s.writeInteraction(ctx, tx, row.Authority, persistence.ConversationInteractionRecord{Interaction: child}, true); err != nil {
			return err
		}
	}
	root.AuthorizationID = root.ID
	root.ApprovedScope = "listed_operations"
	return nil
}

func confirmationResponseMessage(i agentsdk.ConversationInteraction, response agentsdk.ConversationInteractionResponse) string {
	if response.Scope == "listed_operations" {
		return "确认执行列出的 " + strconv.Itoa(len(i.Operations)) + " 项操作"
	}
	return map[string]string{"approve": "确认执行：", "reject": "拒绝执行："}[response.Decision] + i.Tool
}
