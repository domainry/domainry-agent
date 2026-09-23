package agent

import (
	"errors"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
)

func agentOperationCommand(kind, action, clientID string, input any, a agentsdk.ConversationAuthority) sharedoperation.Command {
	key := conversationOwner(a) + ":" + conversationHash(clientID)
	id := "agent-operation:" + conversationHash([]string{a.WorkspaceID, kind, key})
	return sharedoperation.Command{
		ID: id, Scope: sharedoperation.Scope{WorkspaceID: a.WorkspaceID, ResourceType: "agent_owner", ResourceID: conversationOwner(a)},
		Owner: "agent", Kind: kind, ActionKey: action, IdempotencyKey: key, RequestFingerprint: conversationHash([]any{action, input}),
		RequestedBy: a.UserID, Reason: "Agent mutation", Reference: clientID, StatusURL: "agent://operations/" + id, CreatedAt: time.Now().UTC(),
	}
}

func agentOperationError(err error) error {
	if errors.Is(err, sharedoperation.ErrIdempotencyConflict) {
		return conversationError("conflict", "idempotency_conflict")
	}
	return err
}
