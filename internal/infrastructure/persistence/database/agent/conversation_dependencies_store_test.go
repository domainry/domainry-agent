package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func dependentPeer(t *testing.T, repo *ConversationStore, a sdk.ConversationAuthority, source sdk.ConversationDelegation, key string, refs ...sdk.ConversationDependencyInput) sdk.ConversationDelegation {
	t.Helper()
	agent, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: key, Name: key, Instructions: "Review independently", ModelKey: "default", Enabled: true, MaxConcurrent: 1}, a)
	if err != nil {
		t.Fatal(err)
	}
	brief := sdk.ConversationTaskBrief{Version: 1, Goal: key, Deliverable: "Findings", CompletionConditions: []string{"Check evidence"}}
	budget := sdk.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 2048, TimeoutSeconds: 60}
	d, err := repo.CreateConversationDelegation(t.Context(), persistence.ConversationDelegationAdmission{Request: sdk.ConversationDelegationCreate{ClientID: key, ConversationID: source.SourceConversationID, AgentID: agent.ID, Purpose: key, Brief: brief, Dependencies: refs}, FromAgentID: source.FromAgentID, SourceAgent: *source.SourceAgent, Agent: sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision}, Task: sdk.ConversationTask{Goal: brief.Goal, SourceConversationID: source.SourceConversationID, Budget: budget, MaxInputBytes: 32768}}, a)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func currentPeer(t *testing.T, repo *ConversationStore, a sdk.ConversationAuthority, id string) sdk.ConversationDelegation {
	t.Helper()
	d, err := repo.ConversationDelegation(t.Context(), id, a)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func decidePeer(t *testing.T, repo *ConversationStore, a sdk.ConversationAuthority, d sdk.ConversationDelegation, action string, refs *[]sdk.ConversationDependencyInput) sdk.ConversationDelegation {
	t.Helper()
	d = currentPeer(t, repo, a, d.ID)
	in := sdk.ConversationDelegationUpdate{ClientID: fmt.Sprintf("%s-%d", action, d.Revision), ExpectedRevision: d.Revision, Action: action, Reason: "Reviewed current requirements", Dependencies: refs}
	if action == "accept_delivery" {
		in.Review = peerAcceptanceReview(d)
	}
	out, err := repo.UpdateConversationDelegation(t.Context(), d.ID, in, a)
	if err != nil {
		t.Fatal(action, err)
	}
	return out
}
func TestPeerDependencyChangeStopsOnlyAffectedWorkAndRebasesTransitively(t *testing.T) {
	repo, a, root := peerFixture(t)
	ref := func(d sdk.ConversationDelegation, fields ...string) sdk.ConversationDependencyInput {
		return sdk.ConversationDependencyInput{DelegationID: d.ID, BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, Fields: fields}
	}
	affected := dependentPeer(t, repo, a, root, "affected", ref(root, "constraints"))
	unaffected := dependentPeer(t, repo, a, root, "unaffected", ref(root, "audience"))
	transitive := dependentPeer(t, repo, a, root, "transitive", ref(affected))
	claims := map[string]persistence.ConversationClaim{}
	for range 4 {
		launch, ok, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID)
		if err != nil || !ok {
			t.Fatalf("launch %+v %v", launch, err)
		}
		claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "worker", time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim %v %v", ok, err)
		}
		input := executionStoreInput()
		if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
			t.Fatal(err)
		}
		claims[claim.Run.BackgroundTask.DelegationID] = claim
	}
	root = currentPeer(t, repo, a, root.ID)
	brief := root.Brief
	brief.Version++
	brief.Constraints = []string{"All figures must use EUR"}
	request := sdk.ConversationDelegationUpdate{ClientID: "change-currency", ExpectedRevision: root.Revision, Action: "update_brief", Reason: "Currency corrected", Brief: &brief}
	root, err := repo.UpdateConversationDelegation(t.Context(), root.ID, request, a)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := NewConversationStore(repo.store).UpdateConversationDelegation(t.Context(), root.ID, request, a)
	if err != nil || replay.Revision != root.Revision {
		t.Fatal("non-idempotent change", err)
	}
	for _, d := range []sdk.ConversationDelegation{root, affected, transitive} {
		current := currentPeer(t, NewConversationStore(repo.store), a, d.ID)
		if current.Status != "needs_update" || len(current.PendingChanges) != 1 || current.AdoptedAgreementRevision != 1 {
			t.Fatalf("not invalidated: %+v", current)
		}
		if err = repo.Finish(t.Context(), claims[d.ID], sdk.ConversationModelResult{Content: "obsolete"}, ""); err == nil {
			t.Fatal("stale worker committed")
		}
		run, err := repo.Run(t.Context(), d.ConversationID, claims[d.ID].Run.ID, a)
		if err != nil || run.Status != "cancelled" {
			t.Fatal("old run not stopped", err)
		}
		messages, err := repo.ConversationAgentMessages(t.Context(), d.ID, a)
		if err != nil || len(messages) != 2 {
			t.Fatalf("notices count=%d %v", len(messages), err)
		}
		for _, m := range messages {
			if m.Change == nil || m.Change.SourceAgreementRevision != 2 || m.FromUserID != "" || m.FromAgentID != "" {
				t.Fatalf("notice identity/version %+v", m)
			}
		}
	}
	if currentPeer(t, repo, a, unaffected.ID).Status != "running" {
		t.Fatal("unrelated field stopped worker")
	}
	affected = currentPeer(t, repo, a, affected.ID)
	if _, err = repo.UpdateConversationDelegation(t.Context(), affected.ID, sdk.ConversationDelegationUpdate{ClientID: "blind-resume", ExpectedRevision: affected.Revision, Action: "resume", Reason: "No review"}, a); err == nil {
		t.Fatal("silently adopted changed dependency")
	}
	transitive = currentPeer(t, repo, a, transitive.ID)
	refs := []sdk.ConversationDependencyInput{ref(affected)}
	if _, err = repo.UpdateConversationDelegation(t.Context(), transitive.ID, sdk.ConversationDelegationUpdate{ClientID: "premature-resume", ExpectedRevision: transitive.Revision, Action: "resume", Reason: "Upstream not reviewed", Dependencies: &refs}, a); err == nil {
		t.Fatal("resumed through invalid upstream")
	}
	refs = []sdk.ConversationDependencyInput{ref(root, "constraints")}
	affected = decidePeer(t, repo, a, affected, "resume", &refs)
	if affected.AgreementRevision != 2 || affected.AdoptedAgreementRevision != 1 {
		t.Fatal("resume counted as execution adoption")
	}
	refs = []sdk.ConversationDependencyInput{ref(affected)}
	transitive = decidePeer(t, repo, a, transitive, "resume", &refs)
	task, err := repo.ConversationTask(t.Context(), transitive.TaskID, a)
	if err != nil || !strings.Contains(sdk.ConversationTaskPrompt(task), "All figures must use EUR") || len(task.Dependencies) != 2 {
		t.Fatalf("transitive requirements missing %+v %v", task, err)
	}
	decidePeer(t, repo, a, unaffected, "pause", nil)
	for range 2 {
		_, ok, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID)
		if err != nil || !ok {
			t.Fatal(err)
		}
		claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "new-worker", time.Minute)
		if err != nil || !ok {
			t.Fatal(err)
		}
		input := executionStoreInput()
		if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
			t.Fatal(err)
		}
		d := currentPeer(t, repo, a, claim.Run.BackgroundTask.DelegationID)
		if d.AdoptedAgreementRevision != d.AgreementRevision || d.AdoptedAt == nil {
			t.Fatal("missing execution adoption")
		}
	}
	history, err := repo.ConversationAgreementHistory(t.Context(), root.ID, 0, a)
	if err != nil || len(history.Items) != 2 || history.Items[1].Brief.Version != 1 || len(history.Items[1].Brief.Constraints) != 0 || history.Items[0].Brief.Constraints[0] != brief.Constraints[0] {
		t.Fatalf("history mutated %+v %v", history, err)
	}
}

func TestPeerDependencyGraphRejectsCyclesAndCrossGoal(t *testing.T) {
	repo, a, root := peerFixture(t)
	d := dependentPeer(t, repo, a, root, "consumer", sdk.ConversationDependencyInput{DelegationID: root.ID, BriefVersion: 1})
	for _, ref := range []sdk.ConversationDependencyInput{{DelegationID: root.ID, BriefVersion: 1}, {DelegationID: d.ID, BriefVersion: 1}} {
		refs := []sdk.ConversationDependencyInput{ref}
		if _, err := repo.UpdateConversationDelegation(t.Context(), root.ID, sdk.ConversationDelegationUpdate{ClientID: ref.DelegationID, ExpectedRevision: root.Revision, Action: "set_dependencies", Reason: "cycle", Dependencies: &refs}, a); err == nil {
			t.Fatal("cycle accepted")
		}
	}
	other, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "other-goal"}, a)
	if err != nil {
		t.Fatal(err)
	}
	different := root
	different.SourceConversationID = other.ID
	foreign := dependentPeer(t, repo, a, different, "other-consumer")
	refs := []sdk.ConversationDependencyInput{{DelegationID: foreign.ID, BriefVersion: 1}}
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "cross-goal", ExpectedRevision: d.Revision, Action: "set_dependencies", Reason: "wrong goal", Dependencies: &refs}, a); err == nil {
		t.Fatal("cross-goal edge accepted")
	}
	otherOwner := a
	otherOwner.UserID = "someone-else"
	if _, err = repo.ConversationAgreementHistory(t.Context(), root.ID, 0, otherOwner); err == nil {
		t.Fatal("cross-owner history")
	}
}

func TestPeerNextRunQueueAndExactQuestionReply(t *testing.T) {
	repo, a, d := peerFixture(t)
	if _, ok, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || !ok {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	send := func(id, kind, mode, reply, recipient, sender string) sdk.ConversationAgentMessage {
		t.Helper()
		m, err := repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: id, Kind: kind, DeliveryMode: mode, ReplyToID: reply, ToAgentID: recipient, Content: id, BriefVersion: 1}, sender, a)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	later := send("later", "message", "next_run", "", d.ToAgentID, "")
	question := send("question", "question", "next_step", "", d.ToAgentID, d.SourceConversationID)
	inbox, err := repo.ConversationPeerInbox(t.Context(), d.ConversationID, a)
	if err != nil || len(inbox) != 1 || inbox[0].ID != question.ID || later.AfterRunID != claim.Run.ID {
		t.Fatalf("queue head blocked immediate message %+v %v", inbox, err)
	}
	input := executionStoreInput()
	input.InboxMessageIDs = []string{later.ID}
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err == nil {
		t.Fatal("next-run message consumed in old run")
	}
	input.InboxMessageIDs = []string{question.ID}
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	reply := send("reply", "reply", "next_step", question.ID, d.FromAgentID, d.ConversationID)
	retry := send("reply", "reply", "next_step", question.ID, d.FromAgentID, d.ConversationID)
	if retry.ID != reply.ID || reply.FromAgentID != d.ToAgentID || reply.FromUserID != "" {
		t.Fatal("reply identity or duplicate")
	}
	if _, err = repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: "second-reply", Kind: "reply", ReplyToID: question.ID, ToAgentID: d.FromAgentID, Content: "different", BriefVersion: 1}, d.ConversationID, a); err == nil {
		t.Fatal("question answered twice")
	}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "done"}}); err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "done"}, ""); err != nil {
		t.Fatal(err)
	}
	inbox, err = NewConversationStore(repo.store).ConversationPeerInbox(t.Context(), d.ConversationID, a)
	if err != nil || len(inbox) != 1 || inbox[0].ID != later.ID {
		t.Fatal("next-run message did not become ready", err)
	}
	d = currentPeer(t, repo, a, d.ID)
	brief := d.Brief
	brief.Version++
	brief.Goal = "Corrected goal"
	_, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "new-requirement", ExpectedRevision: d.Revision, Action: "update_brief", Reason: "Correction", Brief: &brief}, a)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := repo.ConversationAgentMessages(t.Context(), d.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range messages {
		if m.ID == later.ID && !m.Superseded {
			t.Fatal("queued stale message not marked")
		}
		if m.ID == question.ID && m.AnsweredByID != reply.ID {
			t.Fatal("reply receipt missing")
		}
	}
}

func TestPeerOldAcceptedDeliveryAndHistoryRemainIdentifiable(t *testing.T) {
	repo, a, d := peerFixture(t)
	if _, ok, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || !ok {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "checked"}, ""); err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, a, d.ID)
	oldDelivery := &sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Old evidence"}
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "deliver", ExpectedRevision: d.Revision, Action: "deliver", Reason: "checked", Delivery: oldDelivery}, a)
	if err != nil {
		t.Fatal(err)
	}
	d = decidePeer(t, repo, a, d, "accept_delivery", nil)
	for v := 2; v <= 23; v++ {
		brief := d.Brief
		brief.Version = int64(v)
		brief.Goal = fmt.Sprintf("Scope %d", v)
		d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: fmt.Sprintf("scope-%d", v), ExpectedRevision: d.Revision, Action: "update_brief", Reason: "Correction", Brief: &brief}, a)
		if err != nil {
			t.Fatal(err)
		}
	}
	if d.Delivery == nil || d.Delivery.Summary != "Old evidence" || d.Delivery.AgreementRevision != 1 || d.AgreementRevision != 23 {
		t.Fatal("old delivery overwritten")
	}
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "accept-stale", ExpectedRevision: d.Revision, Action: "accept_delivery", Reason: "wrong"}, a); err == nil {
		t.Fatal("old delivery accepted")
	}
	page, err := repo.ConversationAgreementHistory(t.Context(), d.ID, 0, a)
	if err != nil || len(page.Items) != 20 || page.Complete || page.NextBefore != 4 {
		t.Fatalf("history page %+v %v", page, err)
	}
	older, err := repo.ConversationAgreementHistory(t.Context(), d.ID, page.NextBefore, a)
	if err != nil || len(older.Items) != 3 || !older.Complete || older.Items[2].Revision != 1 {
		t.Fatalf("older history %+v %v", older, err)
	}
	if _, err = repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: "stale-agreement", ToAgentID: d.ToAgentID, BriefVersion: d.Brief.Version, Content: "old agreement"}, "", a); err == nil {
		t.Fatal("message omitted updated agreement")
	}
}

func TestPeerSameBriefDependencyRevisionMustBeExplicit(t *testing.T) {
	repo, a, root := peerFixture(t)
	d := dependentPeer(t, repo, a, root, "consumer", sdk.ConversationDependencyInput{DelegationID: root.ID, BriefVersion: 1})
	other := dependentPeer(t, repo, a, root, "additional-source")
	refs := []sdk.ConversationDependencyInput{{DelegationID: other.ID, BriefVersion: 1, AgreementRevision: 1}}
	root = decidePeer(t, repo, a, root, "set_dependencies", &refs)
	if root.Brief.Version != 1 || root.AgreementRevision != 2 {
		t.Fatal("dependencies did not version agreement")
	}
	d = currentPeer(t, repo, a, d.ID)
	refs = []sdk.ConversationDependencyInput{{DelegationID: root.ID, BriefVersion: 1, AgreementRevision: 1}}
	if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "stale-dependency", ExpectedRevision: d.Revision, Action: "resume", Reason: "Read before upstream change", Dependencies: &refs}, a); err == nil {
		t.Fatal("unchanged brief silently adopted new dependency requirements")
	}
	refs[0].AgreementRevision = 2
	d = decidePeer(t, repo, a, d, "resume", &refs)
	if d.Dependencies[0].AgreementRevision != 2 {
		t.Fatal("reviewed agreement missing")
	}
}

func TestPeerStructuredInputChangesVersionOnlyRelevantDependents(t *testing.T) {
	repo, a, root := peerFixture(t)
	inputPeer := dependentPeer(t, repo, a, root, "input-consumer", sdk.ConversationDependencyInput{DelegationID: root.ID, BriefVersion: 1, Fields: []string{"input"}})
	separate := dependentPeer(t, repo, a, root, "audience-consumer", sdk.ConversationDependencyInput{DelegationID: root.ID, BriefVersion: 1, Fields: []string{"audience"}})
	input := &sdk.ConversationStructuredInput{Schema: []byte(`{"type":"object","required":["currency"]}`), Data: []byte(`{"currency":"EUR"}`)}
	root, err := repo.UpdateConversationDelegation(t.Context(), root.ID, sdk.ConversationDelegationUpdate{ClientID: "typed-data", ExpectedRevision: root.Revision, Action: "update_input", Reason: "Use the verified currency", StructuredInput: input}, a)
	if err != nil {
		t.Fatal(err)
	}
	if root.AgreementRevision != 2 || root.Brief.Version != 1 || currentPeer(t, repo, a, inputPeer.ID).Status != "needs_update" || currentPeer(t, repo, a, separate.ID).Status != "accepted" {
		t.Fatal("input impact is incorrect")
	}
	root = decidePeer(t, repo, a, root, "resume", nil)
	refs := []sdk.ConversationDependencyInput{{DelegationID: root.ID, BriefVersion: 1, AgreementRevision: 2, Fields: []string{"input"}}}
	inputPeer = decidePeer(t, repo, a, inputPeer, "resume", &refs)
	for _, d := range []sdk.ConversationDelegation{root, inputPeer} {
		task, err := repo.ConversationTask(t.Context(), d.TaskID, a)
		if err != nil || !strings.Contains(sdk.ConversationTaskPrompt(task), `"currency":"EUR"`) {
			t.Fatalf("typed input missing %+v %v", task, err)
		}
	}
	history, err := repo.ConversationAgreementHistory(t.Context(), root.ID, 0, a)
	if err != nil || history.Items[0].StructuredInput == nil || history.Items[1].StructuredInput != nil {
		t.Fatalf("input history %+v %v", history, err)
	}
}

func TestPeerPendingChangesRetainUnadoptedFieldsAcrossRevisions(t *testing.T) {
	repo, a, root := peerFixture(t)
	d := dependentPeer(t, repo, a, root, "all-inputs", sdk.ConversationDependencyInput{DelegationID: root.ID, BriefVersion: 1})
	brief := root.Brief
	brief.Version++
	brief.Constraints = []string{"EUR"}
	var err error
	root, err = repo.UpdateConversationDelegation(t.Context(), root.ID, sdk.ConversationDelegationUpdate{ClientID: "change-constraint", ExpectedRevision: root.Revision, Action: "update_brief", Reason: "Currency", Brief: &brief}, a)
	if err != nil {
		t.Fatal(err)
	}
	root, err = repo.UpdateConversationDelegation(t.Context(), root.ID, sdk.ConversationDelegationUpdate{ClientID: "change-data", ExpectedRevision: root.Revision, Action: "update_input", Reason: "Data", StructuredInput: &sdk.ConversationStructuredInput{Data: []byte(`{"amount":20}`)}}, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []sdk.ConversationDelegation{root, currentPeer(t, repo, a, d.ID)} {
		if len(item.PendingChanges) != 1 || len(item.PendingChanges[0].ChangedFields) != 2 || item.PendingChanges[0].ChangedFields[0] != "constraints" || item.PendingChanges[0].ChangedFields[1] != "input" || item.PendingChanges[0].SourceAgreementRevision != 3 {
			t.Fatalf("unadopted change forgotten %+v", item.PendingChanges)
		}
	}
}
