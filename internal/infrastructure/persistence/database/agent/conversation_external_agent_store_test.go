package agent

import (
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func externalAgentFixture(t *testing.T, capabilities sdk.ConversationExternalAgentCapabilities) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationDelegation) {
	t.Helper()
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	a := conversationTestAuthority()
	external := &sdk.ConversationExternalAgentConfig{Protocol: sdk.ConversationExternalAgentProtocolV1, Version: "1", Capabilities: capabilities}
	agent, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "external-receiver", Name: "External reviewer", Instructions: "Review independently", External: external, Enabled: true, MaxConcurrent: 1}, a)
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "external-source"}, a)
	if err != nil {
		t.Fatal(err)
	}
	brief := sdk.ConversationTaskBrief{Version: 1, Goal: "Review the report", Deliverable: "Findings", CompletionConditions: []string{"Check totals"}}
	budget := sdk.ConversationTaskBudget{MaxSteps: 6, MaxToolCalls: 6, MaxOutputBytes: 2048, TimeoutSeconds: 60}
	snapshot := sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision, External: external}
	task := sdk.ConversationTask{Goal: brief.Goal, SourceConversationID: source.ID, Budget: budget, Agent: &snapshot, ExternalExecution: &sdk.ConversationExternalAgentExecution{Protocol: external.Protocol, Version: external.Version, Status: "waiting_claim", Attempt: 1, Capabilities: capabilities, Events: []sdk.ConversationExternalAgentEvent{}, EventsComplete: true, DetailsAvailable: capabilities.ExecutionDetails}}
	in := persistence.ConversationDelegationAdmission{Request: sdk.ConversationDelegationCreate{ClientID: "external-delegation", ConversationID: source.ID, AgentID: agent.ID, Purpose: "Independent review", Brief: brief, Budget: budget}, FromAgentID: "default", SourceAgent: sdk.ConversationAgentSnapshot{ID: "default"}, Agent: snapshot, Task: task}
	d, err := repo.CreateConversationDelegation(t.Context(), in, a)
	if err != nil {
		t.Fatal(err)
	}
	return repo, a, d
}

func TestExternalAgentClaimReportMessageAndDeliveryAreDurableAndIdempotent(t *testing.T) {
	capabilities := sdk.ConversationExternalAgentCapabilities{Steering: true, Cancellation: true, Resume: true, StructuredOutput: true, ExecutionDetails: true}
	repo, a, d := externalAgentFixture(t, capabilities)
	tasks, complete, err := repo.ConversationExternalAgentTasks(t.Context(), d.ToAgentID, 8, a)
	if err != nil || !complete || len(tasks) != 1 || tasks[0].ID != d.TaskID {
		t.Fatalf("assignments=%+v complete=%v err=%v", tasks, complete, err)
	}
	if _, launched, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || launched {
		t.Fatalf("external task entered local worker: %v %v", launched, err)
	}
	wrong := capabilities
	wrong.ExecutionDetails = false
	if _, err = repo.ClaimConversationExternalAgentTask(t.Context(), d.TaskID, sdk.ConversationExternalAgentClaim{ClientID: "wrong-capabilities", AgentID: d.ToAgentID, Capabilities: wrong}, a); err == nil {
		t.Fatal("capability mismatch claimed task")
	}
	claim := sdk.ConversationExternalAgentClaim{ClientID: "claim-one", AgentID: d.ToAgentID, Capabilities: capabilities}
	receipt, err := repo.ClaimConversationExternalAgentTask(t.Context(), d.TaskID, claim, a)
	if err != nil || receipt.Replay || receipt.Task.ExternalExecution.SessionID == "" || receipt.Task.Status != sdk.ConversationTaskStatusRunning {
		t.Fatalf("claim=%+v err=%v", receipt, err)
	}
	replay, err := repo.ClaimConversationExternalAgentTask(t.Context(), d.TaskID, claim, a)
	if err != nil || !replay.Replay || replay.Task.ExternalExecution.SessionID != receipt.Task.ExternalExecution.SessionID {
		t.Fatalf("claim replay=%+v err=%v", replay, err)
	}
	message, err := repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: "external-message", ToAgentID: d.ToAgentID, Content: "Also check currency", BriefVersion: 1, ExecutionAgent: receipt.Task.Agent}, "", a)
	if err != nil {
		t.Fatal(err)
	}
	report := sdk.ConversationExternalAgentReport{ClientID: "report-one", AgentID: d.ToAgentID, SessionID: receipt.Task.ExternalExecution.SessionID, ExpectedLastEventSeq: 0, AcknowledgedMessages: []string{message.ID}, Events: []sdk.ConversationExternalAgentEvent{{Seq: 1, Kind: "progress", Phase: "checking", Summary: "Checked totals", Detail: "Compared every row"}, {Seq: 2, Kind: "tool", Phase: "checking", Summary: "Read source data", Tool: "remote.read"}}}
	reported, err := repo.ReportConversationExternalAgentTask(t.Context(), d.TaskID, report, a)
	if err != nil || reported.Replay || reported.Task.ExternalExecution.LastEventSeq != 2 || len(reported.Task.ExternalExecution.Events) != 2 {
		t.Fatalf("report=%+v err=%v", reported, err)
	}
	replayedReport, err := repo.ReportConversationExternalAgentTask(t.Context(), d.TaskID, report, a)
	if err != nil || !replayedReport.Replay || replayedReport.Task.ExternalExecution.LastEventSeq != 2 {
		t.Fatalf("report replay=%+v err=%v", replayedReport, err)
	}
	if inbox, err := repo.ConversationPeerInbox(t.Context(), d.ConversationID, a); err != nil || len(inbox) != 0 {
		t.Fatalf("message acknowledgement=%+v err=%v", inbox, err)
	}
	finish := sdk.ConversationExternalAgentReport{ClientID: "finish-before-delivery", AgentID: d.ToAgentID, SessionID: receipt.Task.ExternalExecution.SessionID, ExpectedLastEventSeq: 2, Events: []sdk.ConversationExternalAgentEvent{{Seq: 3, Kind: "completed", Summary: "Review complete"}}}
	if _, err = repo.ReportConversationExternalAgentTask(t.Context(), d.TaskID, finish, a); err == nil {
		t.Fatal("final text completed task before structured delivery")
	}
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "deliver-external", ExpectedRevision: reported.Delegation.Revision, Action: "deliver", Reason: "Submit checked findings", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Totals and currency checked", Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Every row checked"}}, Evidence: []sdk.ConversationRunReference{}, Unresolved: []string{}}}, a)
	if err != nil || d.Status != "delivered" {
		t.Fatalf("delivery=%+v err=%v", d, err)
	}
	finish.ClientID = "finish-after-delivery"
	finished, err := repo.ReportConversationExternalAgentTask(t.Context(), d.TaskID, finish, a)
	if err != nil || finished.Task.Status != sdk.ConversationTaskStatusCompleted || finished.Task.ExternalExecution.Status != "completed" || finished.Task.CompletionEventID == "" {
		t.Fatalf("finish=%+v err=%v", finished, err)
	}
}

func TestExternalAgentCancelRequiresCapabilityAndAcknowledgementBeforeResume(t *testing.T) {
	t.Run("unsupported cancellation", func(t *testing.T) {
		unsupported := sdk.ConversationExternalAgentCapabilities{ExecutionDetails: true}
		repo, a, d := externalAgentFixture(t, unsupported)
		if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "unsupported-stop", ExpectedRevision: d.Revision, Action: "cancel", Reason: "Stop"}, a); err == nil {
			t.Fatal("unsupported cancellation accepted")
		}
	})
	t.Run("acknowledged stop", func(t *testing.T) {
		capabilities := sdk.ConversationExternalAgentCapabilities{Cancellation: true, Resume: true, ExecutionDetails: true}
		repo, a, d := externalAgentFixture(t, capabilities)
		claim, err := repo.ClaimConversationExternalAgentTask(t.Context(), d.TaskID, sdk.ConversationExternalAgentClaim{ClientID: "claim-stop", AgentID: d.ToAgentID, Capabilities: capabilities}, a)
		if err != nil {
			t.Fatal(err)
		}
		d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "request-stop", ExpectedRevision: claim.Delegation.Revision, Action: "pause", Reason: "Pause for new requirements"}, a)
		if err != nil || d.Status != "paused" {
			t.Fatalf("pause=%+v err=%v", d, err)
		}
		if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "resume-too-early", ExpectedRevision: d.Revision, Action: "resume", Reason: "Continue"}, a); err == nil {
			t.Fatal("resumed before external acknowledgement")
		}
		stopped, err := repo.ReportConversationExternalAgentTask(t.Context(), d.TaskID, sdk.ConversationExternalAgentReport{ClientID: "ack-stop", AgentID: d.ToAgentID, SessionID: claim.Task.ExternalExecution.SessionID, ExpectedLastEventSeq: 0, EffectState: "known", Events: []sdk.ConversationExternalAgentEvent{{Seq: 1, Kind: "cancelled", Summary: "Stopped after current read"}}}, a)
		if err != nil || !stopped.Task.ExternalExecution.StopAcknowledged {
			t.Fatalf("stop ack=%+v err=%v", stopped, err)
		}
		d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "resume-after-ack", ExpectedRevision: d.Revision, Action: "resume", Reason: "Continue after confirmed stop"}, a)
		if err != nil || d.Status != "accepted" {
			t.Fatalf("resume=%+v err=%v", d, err)
		}
		task, err := repo.ConversationTask(t.Context(), d.TaskID, a)
		if err != nil || task.Status != sdk.ConversationTaskStatusQueued || task.ExternalExecution.Attempt != 2 || task.ExternalExecution.SessionID != "" || len(task.ExternalExecution.Events) != 1 {
			t.Fatalf("resumed task=%+v err=%v", task, err)
		}
		if _, launched, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || launched {
			t.Fatalf("resumed external task entered local worker: %v %v", launched, err)
		}
	})
}

func TestExternalAgentWithoutExecutionDetailsRejectsInventedProcessTelemetry(t *testing.T) {
	capabilities := sdk.ConversationExternalAgentCapabilities{}
	repo, a, d := externalAgentFixture(t, capabilities)
	claim, err := repo.ClaimConversationExternalAgentTask(t.Context(), d.TaskID, sdk.ConversationExternalAgentClaim{ClientID: "claim-no-details", AgentID: d.ToAgentID, Capabilities: capabilities}, a)
	if err != nil {
		t.Fatal(err)
	}
	for index, event := range []sdk.ConversationExternalAgentEvent{
		{Seq: 1, Kind: "tool", Summary: "Used a remote tool", Tool: "remote.read"},
		{Seq: 1, Kind: "progress", Summary: "Inspected data", Detail: "private process detail"},
		{Seq: 1, Kind: "progress", Summary: "Measured usage", Usage: []byte(`{"tokens":10}`)},
	} {
		_, err = repo.ReportConversationExternalAgentTask(t.Context(), d.TaskID, sdk.ConversationExternalAgentReport{ClientID: "invalid-no-details-" + string(rune('a'+index)), AgentID: d.ToAgentID, SessionID: claim.Task.ExternalExecution.SessionID, ExpectedLastEventSeq: 0, Events: []sdk.ConversationExternalAgentEvent{event}}, a)
		if err == nil {
			t.Fatalf("event with unavailable execution details was accepted: %+v", event)
		}
	}
	reported, err := repo.ReportConversationExternalAgentTask(t.Context(), d.TaskID, sdk.ConversationExternalAgentReport{ClientID: "summary-no-details", AgentID: d.ToAgentID, SessionID: claim.Task.ExternalExecution.SessionID, ExpectedLastEventSeq: 0, Events: []sdk.ConversationExternalAgentEvent{{Seq: 1, Kind: "progress", Summary: "Still running"}}}, a)
	if err != nil || reported.Task.ExternalExecution.DetailsAvailable || len(reported.Task.ExternalExecution.Events) != 1 {
		t.Fatalf("bounded status summary=%+v err=%v", reported, err)
	}
}
