package agent

import "testing"

func TestAgentOperationCommandUsesBoundedStableIdempotencyKey(t *testing.T) {
	authority := conversationTestAuthority()
	first := agentOperationCommand("agent.conversation_delete", "conversation.delete", "delete-request", []any{"conversation", int64(3)}, authority)
	replay := agentOperationCommand("agent.conversation_delete", "conversation.delete", "delete-request", []any{"conversation", int64(3)}, authority)

	if len(first.IdempotencyKey) != 64 {
		t.Fatalf("Agent idempotency key length = %d; want one SHA-256 digest that fits the shared 128-character column", len(first.IdempotencyKey))
	}
	if first.IdempotencyKey != replay.IdempotencyKey || first.ID != replay.ID {
		t.Fatal("Agent operation identity is not stable for an exact replay")
	}

	differentRequest := agentOperationCommand("agent.conversation_delete", "conversation.delete", "another-delete-request", []any{"conversation", int64(3)}, authority)
	if differentRequest.IdempotencyKey == first.IdempotencyKey || differentRequest.ID == first.ID {
		t.Fatal("different Agent requests share one operation identity")
	}

	otherAuthority := authority
	otherAuthority.UserID = "another-user"
	differentOwner := agentOperationCommand("agent.conversation_delete", "conversation.delete", "delete-request", []any{"conversation", int64(3)}, otherAuthority)
	if differentOwner.IdempotencyKey == first.IdempotencyKey || differentOwner.ID == first.ID {
		t.Fatal("different Agent owners share one operation identity")
	}
}
