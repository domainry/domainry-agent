package agent

import (
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

func testMemoryCandidate(id string) agentsdk.AgentMemoryCandidate {
	return agentsdk.AgentMemoryCandidate{
		ID: id, Kind: agentsdk.ConversationMemoryKindUserPreference, Scope: agentsdk.ConversationMemoryScopeWorkspace,
		Title: "Response style", Content: "Lead with the conclusion", Confidence: 0.95,
	}
}

func TestAgentTaskMemoryCandidateDeliveryIsDurablyDiscoverable(t *testing.T) {
	store, _ := openAgentStore(t)
	repository := NewAgentTaskRunStore(store)
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	run := agentmodel.AgentTaskRun{
		ID: "memory-task-run", WorkspaceID: "workspace-a", TaskKey: "review", TaskVersion: "1", Status: agentmodel.AgentTaskRunPending,
		IdempotencyKey: "memory-task-idempotency", MaxAttempts: 1, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if _, replayed, err := repository.Create(t.Context(), run); err != nil || replayed {
		t.Fatalf("create replayed=%v err=%v", replayed, err)
	}
	claim, found, err := repository.ClaimAgentTaskRun(t.Context(), run.WorkspaceID, run.ID, "memory-worker", now, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim found=%v err=%v", found, err)
	}
	completedAt := now.Add(time.Second)
	claim.Run.Status, claim.Run.CompletedAt, claim.Run.UpdatedAt = agentmodel.AgentTaskRunSucceeded, &completedAt, completedAt
	claim.Run.Evidence.Memory = &agentsdk.AgentMemoryEvidence{Candidates: []agentsdk.AgentMemoryCandidate{testMemoryCandidate("task-candidate")}}
	if err = repository.SaveRunning(t.Context(), claim.Run, claim.Lease.Owner, claim.Lease.FencingToken); err != nil {
		t.Fatal(err)
	}
	scope := agentpersistence.SystemScope{Kind: agentpersistence.AgentSystemScopeKindGlobal, Purpose: "memory candidate recovery test"}
	pending, err := repository.ListAgentTaskRunsForWorker(t.Context(), scope, agentpersistence.AgentTaskRunFilter{MemoryCandidatesPending: true, Limit: 10})
	if err != nil || len(pending) != 1 || pending[0].Evidence.Memory == nil || len(pending[0].Evidence.Memory.Candidates) != 1 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	receipts := []agentsdk.AgentMemoryCandidateReceipt{{CandidateID: "task-candidate", Disposition: "auto_approved", MemoryID: "memory-1", Revision: "1"}}
	if _, err = repository.SaveAgentTaskMemoryCandidateReceipts(t.Context(), pending[0], receipts); err != nil {
		t.Fatal(err)
	}
	if pending, err = repository.ListAgentTaskRunsForWorker(t.Context(), scope, agentpersistence.AgentTaskRunFilter{MemoryCandidatesPending: true, Limit: 10}); err != nil || len(pending) != 0 {
		t.Fatalf("delivered task remained pending: %+v err=%v", pending, err)
	}
}

func TestConversationMemoryCandidateDeliveryIsDurablyDiscoverable(t *testing.T) {
	store, _ := openAgentStore(t)
	repository := newTestConversationStore(t, store)
	authority := conversationTestAuthority()
	conversation, err := repository.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "memory-candidate-conversation", MemoryEnabled: true}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repository.Enqueue(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "memory-candidate-message", Message: "Remember that I prefer concise answers"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := repository.Claim(t.Context(), authority.RuntimeID, "memory-worker", time.Minute)
	if err != nil || !found || claim.Run.ID != run.ID {
		t.Fatalf("claim=%+v found=%v err=%v", claim, found, err)
	}
	result := agentsdk.ConversationModelResult{
		Content: "Understood.",
		Memory:  &agentsdk.AgentMemoryEvidence{Candidates: []agentsdk.AgentMemoryCandidate{testMemoryCandidate("conversation-candidate")}},
	}
	if err = repository.Finish(t.Context(), claim, result, ""); err != nil {
		t.Fatal(err)
	}
	pending, err := repository.PendingConversationMemoryCandidateDeliveries(t.Context(), authority.RuntimeID, 10)
	if err != nil || len(pending) != 1 || pending[0].Run.ID != run.ID || pending[0].Authority.UserID != authority.UserID {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	receipts := []agentsdk.AgentMemoryCandidateReceipt{{CandidateID: "conversation-candidate", Disposition: "review_required", MemoryID: "memory-2", Revision: "1"}}
	if _, err = repository.RecordConversationMemoryCandidateReceipts(t.Context(), conversation.ID, run.ID, receipts, authority); err != nil {
		t.Fatal(err)
	}
	if pending, err = repository.PendingConversationMemoryCandidateDeliveries(t.Context(), authority.RuntimeID, 10); err != nil || len(pending) != 0 {
		t.Fatalf("delivered conversation remained pending: %+v err=%v", pending, err)
	}
}
