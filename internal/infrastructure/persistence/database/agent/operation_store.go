package agent

import (
	"errors"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/entityid"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
)

func agentOperationCommand(kind, action, clientID string, input any, a agentsdk.ConversationAuthority) sharedoperation.Command {
	key := conversationHash([]string{conversationOwner(a), clientID})
	id, _ := entityid.Derive("op", "agent.operation", a.WorkspaceID, kind, key)
	return sharedoperation.Command{
		OperationID: id, Scope: sharedoperation.Scope{WorkspaceID: a.WorkspaceID, ResourceType: "agent_owner", ResourceID: conversationOwner(a)},
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
