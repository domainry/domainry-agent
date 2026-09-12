package agent

import (
	"context"
	"database/sql"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func runScheduledFollowUp(t *testing.T, repo *ConversationStore, authority agentsdk.ConversationAuthority, conversationID, planID, window, result, code string) agentsdk.ConversationTask {
	t.Helper()
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 2, MaxOutputBytes: 4096, TimeoutSeconds: 30}
	scope := &agentsdk.ConversationFollowUpScope{CompletionCondition: "all open release tasks are complete"}
	request := agentsdk.ScheduledConversationTaskRequest{
		ContractVersion: agentsdk.ScheduledConversationTaskContractVersion, PlanID: planID, SchedulerRunID: "scheduler-" + window,
		IdempotencyKey: window, ScheduledFor: time.Now().UTC().Truncate(time.Millisecond), Authority: authority, ConversationID: conversationID,
		Input: agentsdk.ConversationTaskStart{Goal: "Follow release tasks", Input: `{"status":"open"}`, Budget: budget, FollowUp: scope},
	}
	prepared := agentsdk.ConversationTask{Goal: request.Input.Goal, Input: request.Input.Input, Budget: budget, FollowUp: scope, SourceConversationID: conversationID, ToolScope: []agentsdk.ConversationTaskToolScope{}}
	receipt, err := repo.AcceptScheduledConversationTask(t.Context(), request, prepared)
	if err != nil {
		t.Fatal(err)
	}
	launch, found, err := repo.LaunchConversationTask(t.Context(), authority.RuntimeID)
	if err != nil || !found || launch.Task.ID != receipt.Task.ID || launch.Run.BackgroundTask == nil || launch.Run.BackgroundTask.FollowUp == nil {
		t.Fatalf("launch=%+v found=%t err=%v", launch, found, err)
	}
	claim, found, err := repo.Claim(t.Context(), authority.RuntimeID, "worker-"+window, time.Minute)
	if err != nil || !found || claim.Run.ID != launch.Run.ID {
		t.Fatalf("claim=%+v found=%t err=%v", claim, found, err)
	}
	if err = repo.Finish(t.Context(), claim, agentsdk.ConversationModelResult{Content: result}, code); err != nil {
		t.Fatal(err)
	}
	task, err := repo.ConversationTask(t.Context(), receipt.Task.ID, authority)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func claimFollowUp(t *testing.T, repo *ConversationStore, runtimeID string) (persistence.ConversationFollowUpEventClaim, bool) {
	t.Helper()
	claim, found, err := repo.ClaimConversationFollowUpEvent(t.Context(), runtimeID, "publisher", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return claim, found
}

func TestScheduledFollowUpBaselineChangeCompletionAndOutboxReplay(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "follow-up", Title: "Follow-up"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	runScheduledFollowUp(t, repo, authority, conversation.ID, "plan-follow", "window-1", `{"status":"active","observation":"{\"open\":2,\"blocked\":0}","summary":"Two tasks remain"}`, "")
	if claim, found := claimFollowUp(t, repo, authority.RuntimeID); found {
		t.Fatalf("baseline emitted notification: %+v", claim)
	}
	runScheduledFollowUp(t, repo, authority, conversation.ID, "plan-follow", "window-2", `{"status":"active","observation":"{\"blocked\":0,\"open\":2}","summary":"Still two tasks"}`, "")
	if claim, found := claimFollowUp(t, repo, authority.RuntimeID); found {
		t.Fatalf("same canonical observation emitted notification: %+v", claim)
	}
	runScheduledFollowUp(t, repo, authority, conversation.ID, "plan-follow", "window-3", `{"status":"active","observation":"{\"open\":1,\"blocked\":0}","summary":"One task remains"}`, "")
	changed, found := claimFollowUp(t, repo, authority.RuntimeID)
	if !found || changed.Event.Kind != agentsdk.ConversationFollowUpEventChanged || changed.Event.Occurrence != 3 || changed.Event.Summary != "One task remains" {
		t.Fatalf("changed event=%+v found=%t", changed, found)
	}
	if err = repo.ReleaseConversationFollowUpEvent(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	retried, found := claimFollowUp(t, NewConversationStore(store), authority.RuntimeID)
	if !found || retried.Event.ID != changed.Event.ID || retried.Fence <= changed.Fence {
		t.Fatalf("retried event=%+v found=%t", retried, found)
	}
	if err = repo.CompleteConversationFollowUpEvent(t.Context(), retried); err != nil {
		t.Fatal(err)
	}
	runScheduledFollowUp(t, repo, authority, conversation.ID, "plan-follow", "window-4", `{"status":"completed","observation":"{\"open\":0,\"blocked\":0}","summary":"All release tasks are complete"}`, "")
	completed, found := claimFollowUp(t, repo, authority.RuntimeID)
	if !found || completed.Event.Kind != agentsdk.ConversationFollowUpEventCompleted || completed.Event.Occurrence != 4 {
		t.Fatalf("completed event=%+v found=%t", completed, found)
	}
	if err = repo.CompleteConversationFollowUpEvent(t.Context(), completed); err != nil {
		t.Fatal(err)
	}
	runScheduledFollowUp(t, repo, authority, conversation.ID, "plan-follow", "window-5", `{"status":"active","observation":"{\"open\":1}","summary":"Raced stale window"}`, "")
	if claim, found := claimFollowUp(t, repo, authority.RuntimeID); found {
		t.Fatalf("terminal follow-up reopened: %+v", claim)
	}
	runScheduledFollowUp(t, repo, authority, conversation.ID, "plan-follow", "window-6", "", "provider_timeout")
	if claim, found := claimFollowUp(t, repo, authority.RuntimeID); found {
		t.Fatalf("terminal follow-up emitted stale failure: %+v", claim)
	}
}

func TestScheduledFollowUpFailureInvalidReportAndNeedsActionAreDurable(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "follow-up-errors", Title: "Follow-up errors"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	failed := runScheduledFollowUp(t, repo, authority, conversation.ID, "plan-failed", "failed-1", "", "provider_timeout")
	claim, found := claimFollowUp(t, repo, authority.RuntimeID)
	if failed.Status != agentsdk.ConversationTaskStatusFailed || !found || claim.Event.Kind != agentsdk.ConversationFollowUpEventFailed || claim.Event.ErrorCode != "provider_timeout" {
		t.Fatalf("failed task=%+v event=%+v found=%t", failed, claim, found)
	}
	if err = repo.CompleteConversationFollowUpEvent(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	invalid := runScheduledFollowUp(t, repo, authority, conversation.ID, "plan-invalid", "invalid-1", "plain text", "")
	claim, found = claimFollowUp(t, repo, authority.RuntimeID)
	if invalid.Status != agentsdk.ConversationTaskStatusFailed || invalid.ErrorCode != "follow_up_report_invalid" || !found || claim.Event.ErrorCode != "follow_up_report_invalid" {
		t.Fatalf("invalid task=%+v event=%+v found=%t", invalid, claim, found)
	}
	if err = repo.CompleteConversationFollowUpEvent(t.Context(), claim); err != nil {
		t.Fatal(err)
	}

	// Persist the same fact through the helper used by WaitExecution. The event
	// shares the task transaction and is deduplicated by interaction revision.
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 2, MaxToolCalls: 1, MaxOutputBytes: 1024, TimeoutSeconds: 30}
	scope := &agentsdk.ConversationFollowUpScope{CompletionCondition: "approval received"}
	request := agentsdk.ScheduledConversationTaskRequest{ContractVersion: agentsdk.ScheduledConversationTaskContractVersion, PlanID: "plan-action", SchedulerRunID: "scheduler-action", IdempotencyKey: "action-1", ScheduledFor: time.Now().UTC(), Authority: authority, ConversationID: conversation.ID, Input: agentsdk.ConversationTaskStart{Goal: "Check approval", Budget: budget, FollowUp: scope}}
	receipt, err := repo.AcceptScheduledConversationTask(t.Context(), request, agentsdk.ConversationTask{Goal: request.Input.Goal, Budget: budget, FollowUp: scope, SourceConversationID: conversation.ID, ToolScope: []agentsdk.ConversationTaskToolScope{}})
	if err != nil {
		t.Fatal(err)
	}
	launch, launched, err := repo.LaunchConversationTask(t.Context(), authority.RuntimeID)
	if err != nil || !launched || launch.Task.ID != receipt.Task.ID {
		t.Fatalf("action launch=%+v launched=%t err=%v", launch, launched, err)
	}
	interaction := agentsdk.ConversationInteraction{ID: "interaction-1", Revision: 1, Question: "Approve the release?"}
	err = repo.transaction(t.Context(), func(tx *sql.Tx) error {
		if err := repo.recordConversationFollowUpWaiting(context.Background(), tx, launch.Run, authority, interaction, time.Now().UTC().Truncate(time.Millisecond)); err != nil {
			return err
		}
		return repo.recordConversationFollowUpWaiting(context.Background(), tx, launch.Run, authority, interaction, time.Now().UTC().Truncate(time.Millisecond))
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, found = claimFollowUp(t, repo, authority.RuntimeID)
	if !found || claim.Event.Kind != agentsdk.ConversationFollowUpEventNeedsAction || claim.Event.Question != interaction.Question {
		t.Fatalf("needs action event=%+v found=%t", claim, found)
	}
	var count int
	if err = store.Database().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_conversation_follow_up_events WHERE event_id = ?`, claim.Event.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("needs action dedupe count=%d err=%v", count, err)
	}
}
