package agent

import (
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func delegationSubjectFixture(t *testing.T, sameUser ...bool) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationAuthority, persistence.ConversationDelegationAdmission) {
	t.Helper()
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	source := conversationTestAuthority()
	source.RoleKey = "issuer-role"
	executor := source
	executor.UserID, executor.RoleKey = "professional", "reviewer-role"
	shared := []string{source.UserID}
	if len(sameUser) > 0 && sameUser[0] {
		executor.UserID = source.UserID
		shared = []string{}
	}
	agent, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "professional", Name: "Professional", Instructions: "Review", ModelKey: "default", Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &shared}, executor)
	if err != nil {
		t.Fatal(err)
	}
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "issuer"}, source)
	if err != nil {
		t.Fatal(err)
	}
	brief := sdk.ConversationTaskBrief{Version: 1, Goal: "Review source totals", Deliverable: "Findings", CompletionConditions: []string{"Compare totals"}}
	budget := sdk.ConversationTaskBudget{MaxSteps: 8, MaxToolCalls: 8, MaxOutputBytes: 2048, TimeoutSeconds: 60}
	input := persistence.ConversationDelegationAdmission{
		ExecutionAuthority: &executor, Request: sdk.ConversationDelegationCreate{ClientID: "cross-subject", ConversationID: c.ID, AgentID: agent.ID, Purpose: "Professional review", Brief: brief, Budget: budget},
		FromAgentID: "default", SourceAgent: sdk.ConversationAgentSnapshot{ID: "default"},
		Agent: sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision, OwnerUserID: executor.UserID, ExecutionSubject: &sdk.ConversationExecutionSubject{RuntimeID: executor.RuntimeID, WorkspaceID: executor.WorkspaceID, UserID: executor.UserID}},
		Task:  sdk.ConversationTask{Goal: brief.Goal, SourceConversationID: c.ID, Budget: budget},
	}
	return repo, source, executor, input
}

func TestDelegationSameIdentityAuthorityLookupPreservesOriginalRoleForNewReaders(t *testing.T) {
	repo, original, _, in := delegationSubjectFixture(t, true)
	in.ExecutionAuthority = &original
	d, err := repo.CreateConversationDelegation(t.Context(), in, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.delegationSubjects(t.Context(), repo.store.Database(), d.ID, original); err != nil || found {
		t.Fatal("fixture did not exercise the unmapped same-identity assignment", found, err)
	}
	reader := original
	reader.RoleKey = "another-reading-role"
	for _, lookup := range []sdk.ConversationAuthority{original, reader} {
		authorities, err := NewConversationStore(repo.store).ConversationDelegationAuthorities(t.Context(), d.ID, lookup)
		if err != nil || authorities.Issuer != original || authorities.Executor != original {
			t.Fatal("reading role replaced original authority", authorities, lookup, err)
		}
	}
	reader.UserID = "unrelated"
	if _, err := repo.ConversationDelegationAuthorities(t.Context(), d.ID, reader); err == nil {
		t.Fatal("foreign reader resolved private assignment authorities")
	}
}

func TestDelegationSubjectsSeparateExecutionAndReturnMessagesToIssuer(t *testing.T) {
	repo, source, executor, input := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), input, source)
	if err != nil || d.OwnerUserID != source.UserID || d.ExecutionSubject == nil || d.ExecutionSubject.UserID != executor.UserID {
		t.Fatal("independent admission", d, err)
	}
	again, err := repo.CreateConversationDelegation(t.Context(), input, source)
	if err != nil || again.ID != d.ID {
		t.Fatal("admission retry", again, err)
	}
	if _, err := repo.Get(t.Context(), d.SourceConversationID, executor); err == nil {
		t.Fatal("executor acquired issuer's private conversation")
	}
	if _, err := repo.Get(t.Context(), d.ConversationID, source); err == nil {
		t.Fatal("issuer acquired executor's private conversation")
	}
	if _, err := repo.ConversationTask(t.Context(), d.TaskID, source); err == nil {
		t.Fatal("issuer acquired executor's private task")
	}
	for _, conversationID := range []string{"", d.ConversationID} {
		list, err := NewConversationStore(repo.store).ConversationDelegations(t.Context(), conversationID, executor)
		if err != nil || len(list) != 1 || list[0].ID != d.ID || list[0].OwnerUserID != source.UserID {
			t.Fatal("receiver lost canonical delegation", list, err)
		}
	}
	question, err := repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: "question", ToAgentID: d.ToAgentID, Kind: "question", Content: "Which totals differ?", BriefVersion: 1}, "", source)
	if err != nil {
		t.Fatal(err)
	}
	launch, ok, err := repo.LaunchConversationTask(t.Context(), source.RuntimeID)
	if err != nil || !ok || launch.Authority != executor || launch.Run.ConversationID != d.ConversationID {
		t.Fatal("launch changed execution user or role", launch, err)
	}
	claim, ok, err := repo.Claim(t.Context(), source.RuntimeID, "worker", time.Minute)
	if err != nil || !ok || claim.Authority != executor {
		t.Fatal("claim changed execution user or role", claim, err)
	}
	inbox, err := repo.ConversationPeerInbox(t.Context(), d.ConversationID, executor)
	if err != nil || len(inbox) != 1 || inbox[0].ID != question.ID || inbox[0].FromUserID != source.UserID {
		t.Fatal("issuer message did not reach receiver inbox", inbox, err)
	}
	frozen := executionStoreInput()
	frozen.InboxMessageIDs = []string{question.ID}
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &frozen); err != nil {
		t.Fatal(err)
	}
	reply, err := repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: "reply", ToAgentID: d.FromAgentID, Kind: "reply", ReplyToID: question.ID, Content: "The current month differs", BriefVersion: 1}, d.ConversationID, executor)
	if err != nil || reply.FromAgentID != d.ToAgentID {
		t.Fatal("receiver could not reply across inbox owners", reply, err)
	}
	if err := repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "Reviewed"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Reviewed"}, ""); err != nil {
		t.Fatal(err)
	}
	repo = NewConversationStore(repo.store)
	current, err := repo.ConversationDelegation(t.Context(), d.ID, source)
	if err != nil || current.OwnerUserID != source.UserID || current.Status != "awaiting_delivery" || current.AdoptedAgreementRevision != 1 {
		t.Fatal("execution did not update canonical state", current, err)
	}
	delivery := sdk.ConversationDelegationUpdate{ClientID: "receiver-delivery", ExpectedRevision: current.Revision, Action: "deliver", Reason: "Submit review", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Current month differs", Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Compared the requested totals"}}}}
	if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, delivery, source); err == nil {
		t.Fatal("issuer impersonated receiver's delivery")
	}
	current, err = repo.UpdateConversationDelegation(t.Context(), d.ID, delivery, executor)
	if err != nil || current.Verification == nil || current.Verification.ActorID != executor.UserID {
		t.Fatal("delivery lost actual receiver identity", current, err)
	}
	for _, actor := range []sdk.ConversationAuthority{source, executor} {
		history, err := repo.ConversationDeliveryHistory(t.Context(), d.ID, 0, actor)
		if err != nil || len(history.Items) != 1 || history.Items[0].Verification.ActorID != executor.UserID {
			t.Fatal("delivery history split across record owners", history, err)
		}
	}
	current, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "issuer-accept", ExpectedRevision: current.Revision, Action: "accept_delivery", Reason: "Reviewed findings", Review: peerAcceptanceReview(current)}, source)
	if err != nil || current.Status != "accepted_delivery" || current.Verification.ActorID != source.UserID {
		t.Fatal("issuer acceptance lost task or reviewer identity", current, err)
	}
	messages, err := repo.ConversationAgentMessages(t.Context(), d.ID, executor)
	if err != nil || len(messages) != 3 {
		t.Fatal("two inbox owners lost common communication", messages, err)
	}
	notices, err := repo.ConversationPeerInbox(t.Context(), d.SourceConversationID, source)
	if err != nil || len(notices) != 2 {
		t.Fatal("reply and completion did not return to issuer", notices, err)
	}
	callback, found, err := repo.LaunchConversationPeerMessage(t.Context(), source.RuntimeID)
	if err != nil || !found || callback.ConversationID != d.SourceConversationID {
		t.Fatal("issuer callback did not launch", callback, err)
	}
	callbackClaim, found, err := repo.Claim(t.Context(), source.RuntimeID, "issuer-worker", time.Minute)
	if err != nil || !found || callbackClaim.Authority != source {
		t.Fatal("receiver role leaked into issuer callback", callbackClaim, err)
	}
}

func TestDelegationSubjectsRejectUnrelatedPrincipalsAndIdentityChangingRetries(t *testing.T) {
	repo, source, executor, input := delegationSubjectFixture(t)
	for _, field := range []string{"user", "workspace", "runtime"} {
		invalid := executor
		switch field {
		case "user":
			invalid.UserID = "unrelated"
		case "workspace":
			invalid.WorkspaceID = "foreign"
		case "runtime":
			invalid.RuntimeID = "foreign"
		}
		attempt := input
		attempt.ExecutionAuthority = &invalid
		if _, err := repo.CreateConversationDelegation(t.Context(), attempt, source); err == nil {
			t.Fatal("invalid execution subject admitted", field)
		}
	}
	d, err := repo.CreateConversationDelegation(t.Context(), input, source)
	if err != nil {
		t.Fatal(err)
	}
	changed := executor
	changed.RoleKey = "different-role"
	input.ExecutionAuthority = &changed
	if _, err := repo.CreateConversationDelegation(t.Context(), input, source); err == nil {
		t.Fatal("retry silently changed bound execution role")
	}
	unrelated := source
	unrelated.UserID = "unrelated"
	if _, err := repo.ConversationDelegation(t.Context(), d.ID, unrelated); err == nil {
		t.Fatal("unrelated user used execution mapping")
	}
	grants := []sdk.ConversationDelegationParticipantInput{}
	if _, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "take-ownership", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}, executor); err == nil {
		t.Fatal("executor replaced issuer's participation grants")
	}
}

func TestDelegationSubjectsIssuerControlPreservesExecutorAcrossResume(t *testing.T) {
	repo, source, executor, input := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), input, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.LaunchConversationTask(t.Context(), source.RuntimeID); err != nil || !found {
		t.Fatal("launch", err)
	}
	claim, found, err := repo.Claim(t.Context(), source.RuntimeID, "worker", time.Minute)
	if err != nil || !found {
		t.Fatal("claim", err)
	}
	d, err = repo.ConversationDelegation(t.Context(), d.ID, source)
	if err != nil {
		t.Fatal(err)
	}
	pause := sdk.ConversationDelegationUpdate{ClientID: "issuer-pause", ExpectedRevision: d.Revision, Action: "pause", Reason: "Review remaining work"}
	if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, pause, executor); err == nil {
		t.Fatal("execution assignment granted issuer management")
	}
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, pause, source)
	if err != nil || d.Status != "paused" {
		t.Fatal("issuer could not pause receiver task", d, err)
	}
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "late"}, ""); err == nil {
		t.Fatal("paused receiver lease committed late work")
	}
	task, err := repo.ConversationTask(t.Context(), d.TaskID, executor)
	if err != nil || task.Status != "cancelled" {
		t.Fatal("pause did not control receiver-owned task", task, err)
	}
	repo = NewConversationStore(repo.store)
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "issuer-resume", ExpectedRevision: d.Revision, Action: "resume", Reason: "Continue agreed work"}, source)
	if err != nil {
		t.Fatal("resume", err)
	}
	launch, found, err := repo.LaunchConversationTask(t.Context(), source.RuntimeID)
	if err != nil || !found || launch.Authority != executor || launch.Run.ConversationID != d.ConversationID {
		t.Fatal("issuer control replaced execution authority", launch, err)
	}
}

func TestDelegationSubjectsFollowOriginAcrossExecutionOwners(t *testing.T) {
	repo, source, executor, input := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), input, source)
	if err != nil {
		t.Fatal(err)
	}
	next, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "next-reviewer", Name: "Another reviewer", Instructions: "Review", Enabled: true, ModelKey: "default", MaxConcurrent: 1}, executor)
	if err != nil {
		t.Fatal(err)
	}
	forward := persistence.ConversationDelegationAdmission{
		Request:     sdk.ConversationDelegationCreate{ClientID: "forward-work", ConversationID: d.ConversationID, AgentID: next.ID, Purpose: "Independent check", Brief: d.Brief, Budget: d.Budget},
		FromAgentID: d.ToAgentID, SourceAgent: input.Agent, Agent: sdk.ConversationAgentSnapshot{ID: next.ID, Revision: next.Revision},
		Task: sdk.ConversationTask{Goal: d.Brief.Goal, SourceConversationID: d.ConversationID, Budget: d.Budget},
	}
	forwarded, err := repo.CreateConversationDelegation(t.Context(), forward, executor)
	if err != nil || forwarded.RootConversationID != d.RootConversationID || len(forwarded.Dependencies) != 1 || forwarded.Dependencies[0].DelegationID != d.ID {
		t.Fatal("cross-owner source traversal lost original goal", forwarded, err)
	}
	if _, err := repo.Get(t.Context(), d.SourceConversationID, executor); err == nil {
		t.Fatal("internal origin traversal opened issuer conversation")
	}
}

func TestDelegationSubjectsKeepDifferentRolesForTheSameUser(t *testing.T) {
	repo, source, _, input := delegationSubjectFixture(t)
	executor := source
	executor.RoleKey = "professional-role"
	agent, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "own-professional", Name: "Professional", Instructions: "Review", ModelKey: "default", Enabled: true, MaxConcurrent: 1}, executor)
	if err != nil {
		t.Fatal(err)
	}
	input.ExecutionAuthority = &executor
	input.Request.AgentID = agent.ID
	input.Agent = sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision, OwnerUserID: executor.UserID, ExecutionSubject: &sdk.ConversationExecutionSubject{RuntimeID: executor.RuntimeID, WorkspaceID: executor.WorkspaceID, UserID: executor.UserID}}
	d, err := repo.CreateConversationDelegation(t.Context(), input, source)
	if err != nil {
		t.Fatal(err)
	}
	launch, found, err := repo.LaunchConversationTask(t.Context(), source.RuntimeID)
	if err != nil || !found || launch.Authority != executor {
		t.Fatal("same user lost selected execution role", launch, err)
	}
	claim, found, err := repo.Claim(t.Context(), source.RuntimeID, "worker", time.Minute)
	if err != nil || !found || claim.Authority != executor {
		t.Fatal("execution role", claim, err)
	}
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Reviewed"}, ""); err != nil {
		t.Fatal(err)
	}
	run, found, err := repo.LaunchConversationPeerMessage(t.Context(), source.RuntimeID)
	if err != nil || !found || run.ConversationID != d.SourceConversationID {
		t.Fatal("completion callback", run, err)
	}
	callback, found, err := repo.Claim(t.Context(), source.RuntimeID, "issuer", time.Minute)
	if err != nil || !found || callback.Authority != source {
		t.Fatal("execution role replaced issuer role for the same user", callback, err)
	}
}
