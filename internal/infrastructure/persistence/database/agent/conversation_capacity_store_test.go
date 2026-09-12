package agent

import (
	"errors"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationCapacityAdmissionIsAtomicAndWorkerSkipsSaturatedWorkspace(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	limits := agentsdk.ConversationExecutionLimits{MaxQueuedPerUser: 1, MaxQueuedPerWorkspace: 2, MaxRunningPerUser: 1, MaxRunningPerWorkspace: 1}
	if err := repo.ConfigureConversationExecutionLimits(limits); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	a := conversationTestAuthority()
	b := a
	b.UserID = "user-b"
	c := a
	c.UserID = "user-c"
	otherWorkspace := a
	otherWorkspace.WorkspaceID = "workspace-b"
	create := func(client string, authority agentsdk.ConversationAuthority) agentsdk.Conversation {
		conversation, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: client}, authority)
		if err != nil {
			t.Fatal(err)
		}
		return conversation
	}
	a1, a2 := create("a-one", a), create("a-two", a)
	b1, c1 := create("b-one", b), create("c-one", c)
	other := create("other-workspace", otherWorkspace)

	type enqueueResult struct {
		run agentsdk.ConversationRun
		err error
	}
	results := make(chan enqueueResult, 2)
	var wg sync.WaitGroup
	for index, conversation := range []agentsdk.Conversation{a1, a2} {
		wg.Add(1)
		go func(index int, conversation agentsdk.Conversation) {
			defer wg.Done()
			run, err := repo.Enqueue(ctx, conversation.ID, agentsdk.ConversationSend{ClientMessageID: "message-" + string(rune('a'+index)), Message: "queued"}, a)
			results <- enqueueResult{run: run, err: err}
		}(index, conversation)
	}
	wg.Wait()
	close(results)
	accepted := agentsdk.ConversationRun{}
	rejected := 0
	for result := range results {
		if result.err == nil {
			accepted = result.run
			continue
		}
		var coded *agentsdk.Error
		if !errors.As(result.err, &coded) || coded.Code != "agent.conversation.user_queue_full" {
			t.Fatalf("unexpected concurrent admission error: %v", result.err)
		}
		rejected++
	}
	if accepted.ID == "" || rejected != 1 {
		t.Fatalf("atomic admission accepted=%+v rejected=%d", accepted, rejected)
	}
	bRun, err := repo.Enqueue(ctx, b1.ID, agentsdk.ConversationSend{ClientMessageID: "message-b", Message: "queued"}, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(ctx, c1.ID, agentsdk.ConversationSend{ClientMessageID: "message-c", Message: "workspace full"}, c); err == nil {
		t.Fatal("workspace queue limit was not enforced")
	} else {
		var coded *agentsdk.Error
		if !errors.As(err, &coded) || coded.Code != "agent.conversation.workspace_queue_full" {
			t.Fatalf("workspace admission error=%v", err)
		}
	}
	// Claim order is persisted at millisecond precision. Put the independent
	// workspace in the next ordering bucket so the test exercises skip logic.
	time.Sleep(2 * time.Millisecond)
	otherRun, err := repo.Enqueue(ctx, other.ID, agentsdk.ConversationSend{ClientMessageID: "message-other", Message: "independent"}, otherWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	capacity, err := repo.ConversationExecutionCapacity(ctx, a)
	if err != nil || capacity.UserQueued != 1 || capacity.WorkspaceQueued != 2 || capacity.UserRunning != 0 || capacity.WorkspaceRunning != 0 {
		t.Fatalf("capacity=%+v err=%v", capacity, err)
	}

	first, found, err := repo.Claim(ctx, a.RuntimeID, "worker-one", time.Minute)
	if err != nil || !found || first.Run.ID != accepted.ID && first.Run.ID != bRun.ID {
		t.Fatalf("first claim=%+v found=%v err=%v", first, found, err)
	}
	second, found, err := repo.Claim(ctx, a.RuntimeID, "worker-two", time.Minute)
	if err != nil || !found || second.Run.ID != otherRun.ID {
		t.Fatalf("worker did not skip saturated workspace: claim=%+v found=%v err=%v", second, found, err)
	}
	if err = repo.Finish(ctx, first, agentsdk.ConversationModelResult{Content: "done"}, ""); err != nil {
		t.Fatal(err)
	}
	third, found, err := repo.Claim(ctx, a.RuntimeID, "worker-three", time.Minute)
	remaining := bRun.ID
	if first.Run.ID == bRun.ID {
		remaining = accepted.ID
	}
	if err != nil || !found || third.Run.ID != remaining {
		t.Fatalf("workspace slot was not released: claim=%+v found=%v err=%v", third, found, err)
	}
}

func TestScheduledTaskBacklogSharesCapacityAndReplaySurvivesFullQueue(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	if err := repo.ConfigureConversationExecutionLimits(agentsdk.ConversationExecutionLimits{MaxQueuedPerUser: 1, MaxQueuedPerWorkspace: 1, MaxRunningPerUser: 1, MaxRunningPerWorkspace: 1}); err != nil {
		t.Fatal(err)
	}
	a := conversationTestAuthority()
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "scheduled-capacity"}, a)
	if err != nil {
		t.Fatal(err)
	}
	request := agentsdk.ScheduledConversationTaskRequest{
		ContractVersion: agentsdk.ScheduledConversationTaskContractVersion, PlanID: "plan", SchedulerRunID: "scheduler-one", IdempotencyKey: "one",
		ScheduledFor: time.Now().UTC().Truncate(time.Millisecond), Authority: a, ConversationID: conversation.ID,
		Input: agentsdk.ConversationTaskStart{Goal: "one", Budget: agentsdk.ConversationTaskBudget{MaxSteps: 1, MaxToolCalls: 1, MaxOutputBytes: 256, TimeoutSeconds: 30}},
	}
	prepared := agentsdk.ConversationTask{Goal: request.Input.Goal, Budget: request.Input.Budget, SourceConversationID: conversation.ID, ToolScope: []agentsdk.ConversationTaskToolScope{}}
	first, err := repo.AcceptScheduledConversationTask(t.Context(), request, prepared)
	if err != nil || first.Replay {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replay, err := repo.AcceptScheduledConversationTask(t.Context(), request, prepared)
	if err != nil || !replay.Replay || replay.Task.ID != first.Task.ID {
		t.Fatalf("full-queue replay=%+v err=%v", replay, err)
	}
	request.SchedulerRunID, request.IdempotencyKey, request.Input.Goal = "scheduler-two", "two", "two"
	prepared.Goal = "two"
	if _, err = repo.AcceptScheduledConversationTask(t.Context(), request, prepared); err == nil {
		t.Fatal("scheduled task bypassed shared queue capacity")
	} else {
		var coded *agentsdk.Error
		if !errors.As(err, &coded) || coded.Code != "agent.conversation.user_queue_full" {
			t.Fatalf("scheduled capacity error=%v", err)
		}
	}
}
