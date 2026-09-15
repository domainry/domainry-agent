package agent

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"slices"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func TestSubjectExitRollsBackCounterpartCancellationWhenGrantScopeIsInvalid(t *testing.T) {
	repo, issuer, executor, admission := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), admission, issuer)
	if err != nil {
		t.Fatal(err)
	}
	claim := subjectExitClaim(t, repo, issuer, d)
	setOwner := func(user string) {
		t.Helper()
		err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
			q, args, err := query.NewUpdateBuilder(repo.store.Renderer(), conversationAgentGrantTable).Set("owner_user_id", user).Where(query.And(query.Equal("owner_key", conversationOwner(executor)), query.Equal("viewer_key", conversationOwner(issuer)))).Build()
			return conversationCAS(t.Context(), tx, q, args, err)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	setOwner("wrong-physical-owner")
	lifecycle := NewSubjectLifecycle(repo.store, issuer.RuntimeID)
	if _, err := lifecycle.EraseSubjectForRequest(t.Context(), "rollback-exit", issuer.WorkspaceID, issuer.UserID, nil); err == nil {
		t.Fatal("wrong grant owner was accepted")
	}
	if _, err := repo.Get(t.Context(), d.SourceConversationID, issuer); err != nil {
		t.Fatal("failed exit erased issuer data", err)
	}
	if run, err := repo.Run(t.Context(), claim.Run.ConversationID, claim.Run.ID, executor); err != nil || run.Status != "running" {
		t.Fatal("failed exit left counterpart cancelled", run, err)
	}
	input := executionStoreInput()
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal("failed exit invalidated original worker lease", err)
	}
	setOwner(executor.UserID)
	if _, err := lifecycle.EraseSubjectForRequest(t.Context(), "rollback-exit", issuer.WorkspaceID, issuer.UserID, nil); err != nil {
		t.Fatal("corrected retry of original request failed", err)
	}
}

func TestConcurrentSubjectExitRequestsReturnTheSameDurableReceipt(t *testing.T) {
	repo, issuer, _, admission := delegationSubjectFixture(t)
	if _, err := repo.CreateConversationDelegation(t.Context(), admission, issuer); err != nil {
		t.Fatal(err)
	}
	lifecycle := NewSubjectLifecycle(repo.store, issuer.RuntimeID)
	type outcome struct {
		receipt json.RawMessage
		err     error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			receipt, err := lifecycle.EraseSubjectForRequest(t.Context(), "concurrent-exit", issuer.WorkspaceID, issuer.UserID, nil)
			results <- outcome{receipt, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || len(first.receipt) == 0 || !bytes.Equal(first.receipt, second.receipt) {
		t.Fatal("concurrent erasure failed or changed receipt", first.err, second.err, string(first.receipt), string(second.receipt))
	}
}

func subjectExitClaim(t *testing.T, repo *ConversationStore, issuer sdk.ConversationAuthority, d sdk.ConversationDelegation) persistence.ConversationClaim {
	t.Helper()
	launch, found, err := repo.LaunchConversationTask(t.Context(), issuer.RuntimeID)
	if err != nil || !found || launch.Task.ID != d.TaskID {
		t.Fatal("delegation did not launch", launch, found, err)
	}
	claim, found, err := repo.Claim(t.Context(), issuer.RuntimeID, "subject-exit-worker", time.Minute)
	if err != nil || !found || claim.Run.ConversationID != d.ConversationID {
		t.Fatal("delegation worker did not claim", claim, found, err)
	}
	return claim
}

func TestIssuerSubjectExitStopsForeignWorkerAndKeepsCounterpartPrivateData(t *testing.T) {
	repo, issuer, executor, admission := delegationSubjectFixture(t)
	agent, err := repo.ConversationAgent(t.Context(), admission.Agent.ID, executor)
	if err != nil {
		t.Fatal(err)
	}
	members := []string{issuer.UserID, "remaining-member"}
	agent, err = repo.WriteConversationAgent(t.Context(), agent.ID, sdk.ConversationAgentWrite{ClientID: "members", ExpectedRevision: agent.Revision, Name: agent.Name, Instructions: agent.Instructions, ModelKey: agent.ModelKey, Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &members}, executor)
	if err != nil {
		t.Fatal(err)
	}
	admission.Agent.Revision = agent.Revision
	d, err := repo.CreateConversationDelegation(t.Context(), admission, issuer)
	if err != nil {
		t.Fatal(err)
	}
	claim := subjectExitClaim(t, repo, issuer, d)
	input := executionStoreInput()
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	call := sdk.ConversationToolCall{ID: "inflight-write", Name: "create_thing", Arguments: `{"name":"one original effect"}`}
	if err := repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.BeginExecutionTool(t.Context(), claim, 0, call.ID, sdk.ConversationToolAuthorization{Granted: true}); err != nil {
		t.Fatal(err)
	}
	private, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "counterpart-private", Title: "counterpart private data"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Enqueue(t.Context(), private.ID, sdk.ConversationSend{ClientMessageID: "private", Message: "COUNTERPART-PRIVATE"}, executor); err != nil {
		t.Fatal(err)
	}
	note, err := repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: "pending-exit-note", ToAgentID: d.ToAgentID, Content: "original pending input", BriefVersion: 1, AgreementRevision: 1}, "", issuer)
	if err != nil {
		t.Fatal(err)
	}
	before, err := repo.ConversationPeerInbox(t.Context(), d.ConversationID, executor)
	noteReady := false
	for _, current := range before {
		noteReady = noteReady || current.ID == note.ID
	}
	if err != nil || !noteReady {
		t.Fatal("pending foreign inbox positive case missing", before, err)
	}
	otherWorkspace := issuer
	otherWorkspace.WorkspaceID = "other-subject-workspace"
	otherData, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "same-user-other-workspace"}, otherWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := NewSubjectLifecycle(repo.store, issuer.RuntimeID)
	preview, err := lifecycle.PreviewSubject(t.Context(), issuer.WorkspaceID, issuer.UserID)
	var counts map[string]int64
	if err != nil || json.Unmarshal(preview, &counts) != nil || counts[conversationDelegationSubjectTable] != 1 || counts[conversationAgentGrantTable] != 1 {
		t.Fatal("preview omitted collaboration indexes", string(preview), err)
	}
	exported, err := lifecycle.ExportSubjectForRequest(t.Context(), "export", issuer.WorkspaceID, issuer.UserID)
	if err != nil || !bytes.Contains(exported, []byte(conversationDelegationSubjectTable)) || !bytes.Contains(exported, []byte(conversationAgentGrantTable)) || bytes.Contains(exported, []byte("COUNTERPART-PRIVATE")) {
		t.Fatal("export omitted own routing or exposed counterpart data", string(exported), err)
	}
	receipt, err := lifecycle.EraseSubjectForRequest(t.Context(), "issuer-exit", issuer.WorkspaceID, issuer.UserID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := lifecycle.EraseSubjectForRequest(t.Context(), "issuer-exit", issuer.WorkspaceID, issuer.UserID, nil); err != nil || !bytes.Equal(replay, receipt) {
		t.Fatal("subject exit replay changed", string(replay), err)
	}
	restarted := NewConversationStore(repo.store)
	if _, err := restarted.ConversationDelegation(t.Context(), d.ID, executor); err == nil {
		t.Fatal("erased source delegation retained foreign routing")
	}
	task, err := restarted.ConversationTask(t.Context(), d.TaskID, executor)
	if err != nil || task.Status != "cancelled" {
		t.Fatal("counterpart task still active", task, err)
	}
	run, err := restarted.Run(t.Context(), claim.Run.ConversationID, claim.Run.ID, executor)
	if err != nil || run.Status != "cancelled" || len(run.Steps) != 1 || len(run.Steps[0].Calls) != 1 {
		t.Fatal("original foreign execution/effect history was erased", run, err)
	}
	// An already started effect may finish after cancellation. Preserve only
	// that original receipt; it must not restart the cancelled model/run.
	if err := restarted.FinishExecutionTool(t.Context(), claim, 0, call.ID, sdk.ConversationToolResult{Status: "completed", Content: []byte(`{"id":"original-late-receipt"}`)}); err != nil {
		t.Fatal("original late receipt was discarded", err)
	}
	if err := restarted.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "late model completion"}, ""); err == nil {
		t.Fatal("stale foreign worker completed the cancelled run")
	}
	if current, err := restarted.Run(t.Context(), claim.Run.ConversationID, claim.Run.ID, executor); err != nil || current.Status != "cancelled" || current.Steps[0].Calls[0].Status != "completed" {
		t.Fatal("late receipt resumed run or lost original outcome", current, err)
	}
	if _, err := restarted.Get(t.Context(), private.ID, executor); err != nil {
		t.Fatal("counterpart private conversation was removed", err)
	}
	if _, err := restarted.Get(t.Context(), otherData.ID, otherWorkspace); err != nil {
		t.Fatal("subject exit crossed workspace boundary", err)
	}
	if inbox, err := restarted.ConversationPeerInbox(t.Context(), d.ConversationID, executor); err != nil || len(inbox) != 0 {
		t.Fatal("original pending input remained deliverable", inbox, err)
	}
	kept, err := restarted.ConversationAgent(t.Context(), agent.ID, executor)
	if err != nil || !slices.Equal(kept.SharedWithUserIDs, []string{"remaining-member"}) {
		t.Fatal("foreign agent membership was removed wholesale or stale", kept, err)
	}
	if _, err := restarted.ConversationAgent(t.Context(), agent.ID, issuer); err == nil {
		t.Fatal("exited issuer retained peer use grant")
	}
	if _, launched, err := restarted.LaunchConversationPeerMessage(t.Context(), issuer.RuntimeID); err != nil || launched {
		t.Fatal("exit left stale notices blocking the peer queue", launched, err)
	}
}

func TestExecutorSubjectExitRetainsIssuerMetadataAndInvalidatesDependentWork(t *testing.T) {
	repo, issuer, executor, admission := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), admission, issuer)
	if err != nil {
		t.Fatal(err)
	}
	claim := subjectExitClaim(t, repo, issuer, d)
	agent, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "remaining-agent", Name: "Remaining", Instructions: "Continue independent work", ModelKey: "default", Enabled: true, MaxConcurrent: 2}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	childAdmission := persistence.ConversationDelegationAdmission{Request: sdk.ConversationDelegationCreate{ClientID: "dependent", ConversationID: d.SourceConversationID, AgentID: agent.ID, Purpose: "Use original goal", Brief: d.Brief, Budget: d.Budget, Dependencies: []sdk.ConversationDependencyInput{{DelegationID: d.ID, BriefVersion: 1, AgreementRevision: 1, Fields: []string{"goal"}}}}, FromAgentID: "default", SourceAgent: sdk.ConversationAgentSnapshot{ID: "default"}, Agent: sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision}, Task: sdk.ConversationTask{Goal: d.Brief.Goal, SourceConversationID: d.SourceConversationID, Budget: d.Budget}}
	child, err := repo.CreateConversationDelegation(t.Context(), childAdmission, issuer)
	if err != nil {
		t.Fatal(err)
	}
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: executor.UserID, Operations: []string{"view", "communicate"}}, {UserID: "remaining-participant", Operations: []string{"view"}}}
	child, err = repo.SetConversationDelegationParticipants(t.Context(), child.ID, sdk.ConversationDelegationUpdate{ClientID: "participation", ExpectedRevision: child.Revision, Action: "set_participants", Participants: &grants}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	message, err := repo.SendConversationAgentMessage(t.Context(), child.ID, sdk.ConversationAgentMessageSend{ClientID: "pending-participant", ToAgentID: child.ToAgentID, Kind: "message", Content: "pending input from exiting participant", BriefVersion: 1, AgreementRevision: 1}, "", executor)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := NewSubjectLifecycle(repo.store, issuer.RuntimeID)
	if _, err := lifecycle.EraseSubjectForRequest(t.Context(), "executor-exit", executor.WorkspaceID, executor.UserID, nil); err != nil {
		t.Fatal(err)
	}
	restarted := NewConversationStore(repo.store)
	current, err := restarted.ConversationDelegation(t.Context(), d.ID, issuer)
	if err != nil || !current.SubjectExited || current.Status != "cancelled" || current.OwnerUserID != issuer.UserID {
		t.Fatal("issuer lost original metadata or exit state", current, err)
	}
	if _, err := restarted.Get(t.Context(), d.SourceConversationID, issuer); err != nil {
		t.Fatal("issuer source conversation was removed", err)
	}
	if _, err := restarted.Run(t.Context(), claim.Run.ConversationID, claim.Run.ID, executor); err == nil {
		t.Fatal("exited executor's private run survived")
	}
	child, err = restarted.ConversationDelegation(t.Context(), child.ID, issuer)
	if err != nil || child.Status != "needs_update" || len(child.PendingChanges) != 1 || child.PendingChanges[0].SourceDelegationID != d.ID {
		t.Fatal("dependent work ignored vanished execution source", child, err)
	}
	if len(child.Participants) != 1 || child.Participants[0].UserID != "remaining-participant" {
		t.Fatal("participation cleanup lost other grants", child.Participants)
	}
	if task, err := restarted.ConversationTask(t.Context(), child.TaskID, issuer); err != nil || task.Status != "cancelled" {
		t.Fatal("dependent worker remained runnable", task, err)
	}
	if _, err := restarted.ConversationDelegation(t.Context(), child.ID, executor); err == nil {
		t.Fatal("exited participant retained indexed access")
	}
	messages, err := restarted.ConversationAgentMessages(t.Context(), child.ID, issuer)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, current := range messages {
		if current.ID == message.ID {
			found = current.Superseded
		}
	}
	if !found {
		t.Fatal("pending participant input was not superseded", messages)
	}
	if err := restarted.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "late execution"}, ""); err == nil {
		t.Fatal("erased worker committed a late completion")
	}
}

func TestExecutorSubjectExitPreservesFinishedDependentTaskOutcomes(t *testing.T) {
	for _, errorCode := range []string{"", "original_model_failure"} {
		t.Run(map[bool]string{true: "completed", false: "failed"}[errorCode == ""], func(t *testing.T) {
			repo, issuer, executor, admission := delegationSubjectFixture(t)
			root, err := repo.CreateConversationDelegation(t.Context(), admission, issuer)
			if err != nil {
				t.Fatal(err)
			}
			subjectExitClaim(t, repo, issuer, root)
			agent, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "finished-dependent-agent", Name: "Independent receiver", Instructions: "Preserve original outcomes", ModelKey: "default", Enabled: true, MaxConcurrent: 2}, issuer)
			if err != nil {
				t.Fatal(err)
			}
			child, err := repo.CreateConversationDelegation(t.Context(), persistence.ConversationDelegationAdmission{Request: sdk.ConversationDelegationCreate{ClientID: "finished-dependent", ConversationID: root.SourceConversationID, AgentID: agent.ID, Purpose: "Use the original upstream agreement", Brief: root.Brief, Budget: root.Budget, Dependencies: []sdk.ConversationDependencyInput{{DelegationID: root.ID, BriefVersion: 1, AgreementRevision: 1}}}, FromAgentID: "default", SourceAgent: sdk.ConversationAgentSnapshot{ID: "default"}, Agent: sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision}, Task: sdk.ConversationTask{Goal: root.Brief.Goal, SourceConversationID: root.SourceConversationID, Budget: root.Budget}}, issuer)
			if err != nil {
				t.Fatal(err)
			}
			claim := subjectExitClaim(t, repo, issuer, child)
			if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Original dependent outcome"}, errorCode); err != nil {
				t.Fatal(err)
			}
			originalTask, err := repo.ConversationTask(t.Context(), child.TaskID, issuer)
			if err != nil || !originalTask.Terminal() || originalTask.CompletedAt == nil || originalTask.ExecutionRunID == "" {
				t.Fatal("dependent task did not finish", originalTask, err)
			}
			originalRun, err := repo.Run(t.Context(), child.ConversationID, originalTask.ExecutionRunID, issuer)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewSubjectLifecycle(repo.store, issuer.RuntimeID).EraseSubjectForRequest(t.Context(), "finished-upstream-exit", executor.WorkspaceID, executor.UserID, nil); err != nil {
				t.Fatal(err)
			}
			restarted := NewConversationStore(repo.store)
			current, err := restarted.ConversationDelegation(t.Context(), child.ID, issuer)
			if err != nil || current.Status != "needs_update" || len(current.PendingChanges) != 1 || current.PendingChanges[0].SourceDelegationID != root.ID {
				t.Fatal("finished dependent missed the upstream exit", current, err)
			}
			task, err := restarted.ConversationTask(t.Context(), child.TaskID, issuer)
			if err != nil || !bytes.Equal(conversationJSON(task), conversationJSON(originalTask)) {
				t.Fatalf("upstream exit rewrote the finished dependent task: original=%s current=%s decode=%v", originalTask.Status, task.Status, err)
			}
			run, err := restarted.Run(t.Context(), child.ConversationID, originalTask.ExecutionRunID, issuer)
			if err != nil || !bytes.Equal(conversationJSON(run), conversationJSON(originalRun)) {
				t.Fatal("upstream exit rewrote the finished dependent run", run, err)
			}
			if launch, found, err := restarted.LaunchConversationTask(t.Context(), issuer.RuntimeID); err != nil || found {
				t.Fatal("upstream exit launched the finished dependent again", launch, found, err)
			}
		})
	}
}

func TestSubjectExitSupersedesDeletedDelegationNoticesBeforeForeignWake(t *testing.T) {
	repo, a, b, admission := delegationSubjectFixture(t)
	root, err := repo.CreateConversationDelegation(t.Context(), admission, a)
	if err != nil {
		t.Fatal(err)
	}
	c := b
	c.UserID = "remaining-receiver"
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: c.UserID, Operations: []string{"view"}}}
	root, err = repo.SetConversationDelegationParticipants(t.Context(), root.ID, sdk.ConversationDelegationUpdate{ClientID: "remaining-upstream-viewer", ExpectedRevision: root.Revision, Action: "set_participants", Participants: &grants}, a)
	if err != nil {
		t.Fatal(err)
	}
	child := nestedSubjectDependency(t, repo, root, b, c, admission.Agent, "exiting-issued-child")
	if _, err := NewSubjectLifecycle(repo.store, a.RuntimeID).EraseSubjectForRequest(t.Context(), "nested-source-exit", b.WorkspaceID, b.UserID, nil); err != nil {
		t.Fatal(err)
	}
	restarted := NewConversationStore(repo.store)
	if task, err := restarted.ConversationTask(t.Context(), child.TaskID, c); err != nil || task.Status != "cancelled" {
		t.Fatal("deleted issuer left a runnable foreign task", task.Status, err)
	}
	if run, launched, err := restarted.LaunchConversationPeerMessage(t.Context(), a.RuntimeID); err != nil || launched {
		t.Fatal("deleted delegation notice poisoned or restarted the remaining owner's message queue", run.ID, launched, err)
	}
	// The remaining owner can still admit and launch independent work.
	source, err := restarted.Create(t.Context(), sdk.ConversationCreate{ClientID: "independent-after-exit"}, c)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := restarted.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "independent-after-exit", Name: "Independent", Instructions: "Continue remaining work", ModelKey: "default", Enabled: true, MaxConcurrent: 1}, c)
	if err != nil {
		t.Fatal(err)
	}
	work, err := restarted.CreateConversationDelegation(t.Context(), persistence.ConversationDelegationAdmission{Request: sdk.ConversationDelegationCreate{ClientID: "independent-after-exit", ConversationID: source.ID, AgentID: agent.ID, Purpose: "Independent work after another subject exited", Brief: root.Brief, Budget: root.Budget}, FromAgentID: "default", SourceAgent: sdk.ConversationAgentSnapshot{ID: "default"}, Agent: sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision}, Task: sdk.ConversationTask{Goal: root.Brief.Goal, SourceConversationID: source.ID, Budget: root.Budget}}, c)
	if err != nil {
		t.Fatal(err)
	}
	if launch, found, err := restarted.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || !found || launch.Task.ID != work.TaskID || launch.Authority != c {
		t.Fatal("subject exit blocked independent remaining-owner work", launch.Task.ID, found, err)
	}
}
