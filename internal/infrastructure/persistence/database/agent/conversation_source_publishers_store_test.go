package agent

import (
	"database/sql"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func sourcePublisherFixture(t *testing.T) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationAuthority, sdk.ConversationDelegation, sdk.ConversationRunReference, sdk.ConversationRunReference) {
	t.Helper()
	repo, issuer, executor, in := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), in, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if _, launched, err := repo.LaunchConversationTask(t.Context(), executor.RuntimeID); err != nil || !launched {
		t.Fatal("launch", launched, err)
	}
	claim, found, err := repo.Claim(t.Context(), executor.RuntimeID, "publisher-test", time.Minute)
	if err != nil || !found {
		t.Fatal("claim", found, err)
	}
	input := executionStoreInput()
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "Reviewed"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Reviewed"}, ""); err != nil {
		t.Fatal(err)
	}
	d, err = repo.ConversationDelegation(t.Context(), d.ID, executor)
	if err != nil {
		t.Fatal(err)
	}
	original := executor
	original.RoleKey = "old-proof-role"
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "old-proof"}, original)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "old-proof", Message: "Old receipt"}, original)
	if err != nil {
		t.Fatal(err)
	}
	return repo, issuer, executor, d, sdk.ConversationRunReference{ConversationID: c.ID, RunID: run.ID, BeforeStep: 1}, sdk.ConversationRunReference{ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, BeforeStep: 1}
}

func insertLegacyPublication(t *testing.T, repo *ConversationStore, release persistence.ConversationSourceRelease, reader sdk.ConversationAuthority) {
	t.Helper()
	q, args, err := query.NewInsertBuilder(repo.store.Renderer(), conversationSourceReleaseTable).Columns("owner_key", "release_id", "producer_key", "delegation_id", "conversation_id", "run_id", "before_step", "payload_json").Values(conversationOwner(reader), conversationHash(release), conversationOwner(release.Producer), release.DelegationID, release.Reference.ConversationID, release.Reference.RunID, release.Reference.BeforeStep, conversationJSON(release)).Build()
	if err := conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
		t.Fatal(err)
	}
}

func TestSourcePublisherManualDeliveryUsesActualRoleAndReviewDoesNotRepublish(t *testing.T) {
	repo, issuer, executor, d, root, _ := sourcePublisherFixture(t)
	publisher := executor
	publisher.RoleKey = "actual-manual-publishing-role"
	delivery := sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Reviewed", Evidence: []sdk.ConversationRunReference{root}, Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Compared totals"}}}
	var err error
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "manual-delivery", ExpectedRevision: d.Revision, Action: "deliver", Reason: "Share original receipt", Delivery: &delivery}, publisher)
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		items, err := NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), root, issuer)
		if err != nil || len(items) != 1 || items[0].Publisher == nil || *items[0].Publisher != publisher || items[0].Producer.RoleKey != "old-proof-role" {
			t.Fatal("actual publisher replaced by admission/reviewer role", items, err)
		}
	}
	check()
	reviewer := issuer
	reviewer.RoleKey = "actual-reviewing-role"
	for _, action := range []string{"review_delivery", "accept_delivery"} {
		d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: action, ExpectedRevision: d.Revision, Action: action, Reason: "Assess submitted result", Review: peerAcceptanceReview(d)}, reviewer)
		if err != nil {
			t.Fatal(action, err)
		}
		check()
	}
}

func TestLegacySourcePublisherUsesHistoricalDeliveryRunAndRejectsUnprovenActors(t *testing.T) {
	for _, kind := range []string{"deliver", "review_delivery", "manual", "unrelated-root", "wrong-delegation"} {
		t.Run(kind, func(t *testing.T) {
			repo, issuer, executor, d, root, actorSource := sourcePublisherFixture(t)
			original := executor
			original.RoleKey = "old-proof-role"
			release := persistence.ConversationSourceRelease{DelegationID: d.ID, Purpose: "delivery", Reference: root, Producer: original}
			insertLegacyPublication(t, repo, release, issuer)
			entry := sdk.ConversationDeliveryRecord{Revision: 99, Kind: "deliver", Delivery: sdk.ConversationDelegationDelivery{Evidence: []sdk.ConversationRunReference{root}}, Verification: sdk.ConversationDeliveryVerification{ActorID: executor.UserID, AgentID: d.ToAgentID, Source: &actorSource}}
			switch kind {
			case "review_delivery":
				entry.Kind = kind
			case "manual":
				entry.Verification.Source = nil
				entry.Verification.AgentID = ""
			case "unrelated-root":
				entry.Delivery.Evidence = nil
			case "wrong-delegation":
				entry.Verification.Source = &root // Not this delegation's execution.
			}
			q, args, err := query.NewInsertBuilder(repo.store.Renderer(), conversationDeliveryRecordTable).Columns("owner_key", "delegation_id", "revision", "payload_json").Values(conversationOwner(issuer), d.ID, entry.Revision, conversationJSON(entry)).Build()
			if err := conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
				t.Fatal(err)
			}
			// Simulate a later binding: historical proof must retain its old role.
			current := executor
			current.RoleKey = "later-execution-role"
			q, args, err = query.NewUpdateBuilder(repo.store.Renderer(), conversationDelegationSubjectTable).Set("execution_authority_json", conversationJSON(current)).Where(query.Equal("delegation_id", d.ID)).Build()
			if err := conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
				t.Fatal(err)
			}
			items, err := NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), root, issuer)
			if err != nil || len(items) != 1 {
				t.Fatal(items, err)
			}
			if kind == "deliver" {
				if items[0].Publisher == nil || *items[0].Publisher != executor || items[0].Producer != original {
					t.Fatal("current binding replaced historical publisher", items)
				}
			} else if items[0].Publisher != nil {
				t.Fatal("unproven event granted publication authority", kind, items)
			}
			// Recovery must not rewrite the legacy row or fabricate history.
			q, args, err = query.NewSelectBuilder(repo.store.Renderer(), conversationSourceReleaseTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(issuer)), query.Equal("release_id", conversationHash(release)))).Build()
			if err != nil {
				t.Fatal(err)
			}
			var raw []byte
			if err := repo.store.Database().QueryRowContext(t.Context(), q, args...).Scan(&raw); err != nil || string(raw) != string(conversationJSON(release)) {
				t.Fatal("immutable legacy publication was rewritten", err)
			}
		})
	}
}

func TestLegacyManualSourcePublicationNeedsExplicitResubmission(t *testing.T) {
	repo, issuer, executor, d, root, _ := sourcePublisherFixture(t)
	publisher := executor
	publisher.RoleKey = "current-manual-publisher"
	delivery := sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Reviewed", Evidence: []sdk.ConversationRunReference{root}, Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Compared totals"}}}
	var err error
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "old-manual", ExpectedRevision: d.Revision, Action: "deliver", Reason: "Original manual submission", Delivery: &delivery}, publisher)
	if err != nil {
		t.Fatal(err)
	}
	items, err := repo.ConversationSourceReleases(t.Context(), root, issuer)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	legacy := items[0]
	legacy.Publisher = nil
	q, args, err := query.NewUpdateBuilder(repo.store.Renderer(), conversationSourceReleaseTable).Set("release_id", conversationHash(legacy)).Set("payload_json", conversationJSON(legacy)).Where(query.And(query.Equal("owner_key", conversationOwner(issuer)), query.Equal("release_id", conversationHash(items[0])))).Build()
	if err := conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
		t.Fatal(err)
	}
	items, err = NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), root, issuer)
	if err != nil || len(items) != 1 || items[0].Publisher != nil {
		t.Fatal("manual history inferred an unrecorded selected role", items, err)
	}
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "explicit-resubmission", ExpectedRevision: d.Revision, Action: "deliver", Reason: "Explicitly republish under current role", Delivery: &delivery}, publisher)
	if err != nil {
		t.Fatal(err)
	}
	items, err = NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), root, issuer)
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	known := 0
	for _, item := range items {
		if item.Publisher != nil {
			known++
			if *item.Publisher != publisher || item.Producer != legacy.Producer {
				t.Fatal("resubmission changed immutable proof identity", items)
			}
		}
	}
	if known != 1 {
		t.Fatal("explicit publication did not preserve unknown legacy evidence", items)
	}
}

func TestLegacySourcePublisherContractUsesActualChangeRunAndPreservesOldInputProducer(t *testing.T) {
	repo, issuer, reader, in := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), in, issuer)
	if err != nil {
		t.Fatal(err)
	}
	publisher := issuer
	publisher.RoleKey = "actual-contract-publishing-role"
	actorRun, err := repo.Enqueue(t.Context(), d.SourceConversationID, sdk.ConversationSend{ClientMessageID: "contract-publisher", Message: "Share old input"}, publisher)
	if err != nil {
		t.Fatal(err)
	}
	actorRef := sdk.ConversationRunReference{ConversationID: d.SourceConversationID, RunID: actorRun.ID, BeforeStep: 1}
	original := issuer
	original.RoleKey = "old-input-proof-role"
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "old-input"}, original)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "old-input", Message: "Input"}, original)
	if err != nil {
		t.Fatal(err)
	}
	root := sdk.ConversationRunReference{ConversationID: c.ID, RunID: run.ID, BeforeStep: 1}
	release := persistence.ConversationSourceRelease{DelegationID: d.ID, Purpose: "contract", Reference: root, Producer: original}
	insertLegacyPublication(t, repo, release, reader)
	entry := sdk.ConversationAgreementRevision{Revision: 2, InputSource: &root, ChangeSource: &actorRef, FromAgentID: "default"}
	q, args, err := query.NewInsertBuilder(repo.store.Renderer(), conversationAgreementTable).Columns("owner_key", "delegation_id", "revision", "payload_json").Values(conversationOwner(issuer), d.ID, entry.Revision, conversationJSON(entry)).Build()
	if err := conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
		t.Fatal(err)
	}
	items, err := NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), root, reader)
	if err != nil || len(items) != 1 || items[0].Publisher == nil || *items[0].Publisher != publisher || items[0].Producer != original {
		t.Fatal("contract publisher inferred from old input/admission instead of change", items, err)
	}
	// New contract publication also uses the submitting role, after owner checks.
	d.InputSource = &root
	if err := repo.transaction(t.Context(), func(tx *sql.Tx) error { return repo.saveDelegationContractReleases(t.Context(), tx, d, publisher) }); err != nil {
		t.Fatal(err)
	}
	items, err = repo.ConversationSourceReleases(t.Context(), root, reader)
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	for _, item := range items {
		if item.Publisher == nil || *item.Publisher != publisher || item.Producer != original {
			t.Fatal("new and recovered publication identities differ", items)
		}
	}
}

func TestLegacyMessageSourcePublisherUsesImmutableSenderRun(t *testing.T) {
	for _, sender := range []string{"recorded", "legacy", "wrong-role"} {
		t.Run(sender, func(t *testing.T) {
			repo, issuer, executor, d, _, source := sourcePublisherFixture(t)
			release := persistence.ConversationSourceRelease{DelegationID: d.ID, Purpose: "message", Reference: source, Producer: executor}
			insertLegacyPublication(t, repo, release, issuer)
			message := sdk.ConversationAgentMessage{ID: "legacy-message", DelegationID: d.ID, FromAgentID: d.ToAgentID, SenderUserID: executor.UserID, SenderRoleKey: executor.RoleKey, Source: &source}
			if sender == "legacy" {
				message.SenderUserID, message.SenderRoleKey = "", ""
			} else if sender == "wrong-role" {
				message.SenderRoleKey = "unrelated-role"
			}
			q, args, err := query.NewInsertBuilder(repo.store.Renderer(), conversationAgentMessageTable).Columns("owner_key", "delegation_id", "message_id", "conversation_id", "consumed_run_id", "created_at", "payload_json", "runtime_id", "authority_json", "agent_json").Values(conversationOwner(issuer), d.ID, message.ID, d.SourceConversationID, "", time.Now().UnixMilli(), conversationJSON(message), issuer.RuntimeID, conversationJSON(issuer), conversationJSON(d.SourceAgent)).Build()
			if err := conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
				t.Fatal(err)
			}
			items, err := NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), source, issuer)
			if err != nil || len(items) != 1 {
				t.Fatal(items, err)
			}
			if sender == "wrong-role" {
				if items[0].Publisher != nil {
					t.Fatal("sender role disagreement granted publication", items)
				}
			} else if items[0].Publisher == nil || *items[0].Publisher != executor {
				t.Fatal("historical sender run was not preserved", items)
			}
		})
	}
}

func TestLegacyRequiredSourcePublisherUsesExactOriginalAdmissionCall(t *testing.T) {
	for _, variant := range []string{"actual", "current-metadata-changed", "undeclared", "other-delegation", "incomplete", "wrong-step", "other-operation", "manual-history"} {
		t.Run(variant, func(t *testing.T) {
			repo, issuer, reader, in := delegationSubjectFixture(t)
			d, err := repo.CreateConversationDelegation(t.Context(), in, issuer)
			if err != nil {
				t.Fatal(err)
			}
			publisher := issuer
			publisher.RoleKey = "original-admission-publisher"
			actor, err := repo.Enqueue(t.Context(), d.SourceConversationID, sdk.ConversationSend{ClientMessageID: "admission", Message: "Delegate sources"}, publisher)
			if err != nil {
				t.Fatal(err)
			}
			original := issuer
			original.RoleKey = "old-source-proof"
			c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "required-old-source"}, original)
			if err != nil {
				t.Fatal(err)
			}
			run, err := repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "required-old-source", Message: "Source"}, original)
			if err != nil {
				t.Fatal(err)
			}
			root := sdk.ConversationRunReference{ConversationID: c.ID, RunID: run.ID, BeforeStep: 2}
			actorRef := sdk.ConversationRunReference{ConversationID: d.SourceConversationID, RunID: actor.ID, BeforeStep: 1}
			release := persistence.ConversationSourceRelease{DelegationID: d.ID, Purpose: "contract", Reference: root, Producer: original}
			insertLegacyPublication(t, repo, release, reader)
			entry := sdk.ConversationAgreementRevision{Revision: 1, FromAgentID: "default", ChangeSource: &actorRef}
			if variant == "manual-history" {
				entry.FromAgentID, entry.FromUserID, entry.ChangeSource = "", issuer.UserID, nil
			}
			q, args, err := query.NewUpdateBuilder(repo.store.Renderer(), conversationAgreementTable).Set("payload_json", conversationJSON(entry)).Where(query.And(query.Equal("owner_key", conversationOwner(issuer)), query.Equal("delegation_id", d.ID), query.Equal("revision", 1))).Build()
			if err := conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
				t.Fatal(err)
			}
			request := sdk.ConversationDelegationCreate{Requirements: sdk.ConversationAgentRequirements{Sources: []sdk.ConversationRunReference{root}}}
			if variant == "undeclared" {
				request.Requirements.Sources[0].BeforeStep++
			}
			step := 0
			var definition sdk.ConversationToolDefinition
			for _, candidate := range sdk.ConversationCollaborationTools() {
				if candidate.Key == "agent_delegate" {
					definition = candidate
				}
			}
			record := persistence.ConversationToolExecution{Step: step, State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "admitted", Name: "agent_delegate", Arguments: string(conversationJSON(request))}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: d.ID}}
			switch variant {
			case "other-delegation":
				record.Result.ResourceID = "another-delegation"
			case "incomplete":
				record.State = "running"
			case "wrong-step":
				step, record.Step = 1, 1
			case "other-operation":
				record.Call.Name = "delegation_update"
			}
			claim := persistence.ConversationClaim{Authority: publisher, Run: sdk.ConversationRun{ConversationID: actorRef.ConversationID, ID: actorRef.RunID}}
			if err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
				return repo.executionWrite(t.Context(), tx, "_agent_conversation_tool_calls", claim, step, record.Call.ID, record, true)
			}); err != nil {
				t.Fatal(err)
			}
			// Current metadata is neither required nor sufficient to prove an
			// old declaration. The original completed call is the authority.
			d.Requirements.Sources = []sdk.ConversationRunReference{root}
			if variant == "current-metadata-changed" {
				d.Requirements.Sources = nil
			}
			q, args, err = query.NewUpdateBuilder(repo.store.Renderer(), conversationDelegationTable).Set("payload_json", conversationJSON(d)).Where(query.And(query.Equal("owner_key", conversationOwner(issuer)), query.Equal("delegation_id", d.ID))).Build()
			if err := conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
				t.Fatal(err)
			}
			items, err := NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), root, reader)
			if err != nil || len(items) != 1 {
				t.Fatal(items, err)
			}
			if variant == "actual" || variant == "current-metadata-changed" {
				if items[0].Publisher == nil || *items[0].Publisher != publisher || items[0].Producer != original {
					t.Fatal("immutable admission identity was not recovered", items)
				}
			} else if items[0].Publisher != nil {
				t.Fatal("unproven required root acquired a publisher", variant, items)
			}
		})
	}
}
