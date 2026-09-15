package agent

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

func TestLegacyContractRunEvidenceRequiresCapturedRootsAndOriginalAuthority(t *testing.T) {
	for _, kind := range []string{"original", "missing-snapshot", "wrong-authority"} {
		t.Run(kind, func(t *testing.T) {
			repo, issuer, executor, d, root := contractPublicationFixture(t)
			launched, found, err := repo.LaunchConversationTask(t.Context(), issuer.RuntimeID)
			if err != nil || !found {
				t.Fatal("launch", found, err)
			}
			legacyContractSnapshot(t, repo, d, issuer)
			q, args, err := query.NewDeleteBuilder(repo.store.Renderer(), conversationCollaborationMutationTable).Where(query.Equal("owner_key", conversationOwner(issuer))).Build()
			if err = conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
				t.Fatal(err)
			}
			d.Requirements = sdk.ConversationAgentRequirements{TaskType: "today"}
			q, args, err = query.NewUpdateBuilder(repo.store.Renderer(), conversationDelegationTable).Set("payload_json", conversationJSON(d)).Where(query.And(query.Equal("owner_key", conversationOwner(issuer)), query.Equal("delegation_id", d.ID))).Build()
			if err = conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
				t.Fatal(err)
			}
			if kind == "missing-snapshot" {
				var envelope map[string]json.RawMessage
				var background map[string]json.RawMessage
				if err = json.Unmarshal(conversationJSON(launched.Run), &envelope); err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(envelope["background_task"], &background); err != nil {
					t.Fatal(err)
				}
				delete(background, "requirements")
				envelope["background_task"], _ = json.Marshal(background)
				raw, _ := json.Marshal(envelope)
				q, args, err = query.NewUpdateBuilder(repo.store.Renderer(), "_agent_conversation_runs").Set("payload_json", raw).Where(query.And(conversationScope(executor, launched.Run.ConversationID), query.Equal("run_id", launched.Run.ID))).Build()
				if err = conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
					t.Fatal(err)
				}
			} else if kind == "wrong-authority" {
				actor := executor
				actor.UserID = "unrelated"
				q, args, err = query.NewUpdateBuilder(repo.store.Renderer(), "_agent_conversation_runs").Set("authority_json", conversationJSON(actor)).Where(query.And(conversationScope(executor, launched.Run.ConversationID), query.Equal("run_id", launched.Run.ID))).Build()
				if err = conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
					t.Fatal(err)
				}
			}
			record, err := repo.ConversationContractPublicationRecord(t.Context(), d.ID, 1, issuer)
			if kind == "original" {
				if err != nil || len(record.Requirements.Sources) != 1 || record.Requirements.Sources[0] != root {
					t.Fatal("exact frozen execution roots not restored", record, err)
				}
			} else if err == nil {
				t.Fatal("unknown/corrupt execution metadata manufactured original roots", kind, record)
			}
		})
	}
}

func contractPublicationFixture(t *testing.T) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationAuthority, sdk.ConversationDelegation, sdk.ConversationRunReference) {
	t.Helper()
	repo, issuer, executor, in := delegationSubjectFixture(t)
	original := issuer
	original.RoleKey = "old-contract-proof"
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "contract-proof"}, original)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "contract-proof", Message: "Original input"}, original)
	if err != nil {
		t.Fatal(err)
	}
	root := sdk.ConversationRunReference{ConversationID: c.ID, RunID: run.ID, BeforeStep: 1}
	in.Request.Requirements = sdk.ConversationAgentRequirements{TaskType: "original-review", Sources: []sdk.ConversationRunReference{root}}
	in.Task.Requirements = in.Request.Requirements
	d, err := repo.CreateConversationDelegation(t.Context(), in, issuer)
	if err != nil {
		t.Fatal(err)
	}
	return repo, issuer, executor, d, root
}

func legacyContractSnapshot(t *testing.T, repo *ConversationStore, d sdk.ConversationDelegation, a sdk.ConversationAuthority) sdk.ConversationAgreementRevision {
	t.Helper()
	history, err := repo.ConversationAgreementHistory(t.Context(), d.ID, 0, a)
	if err != nil || len(history.Items) != 1 {
		t.Fatal(history, err)
	}
	entry := history.Items[0]
	entry.Requirements = nil
	q, args, err := query.NewUpdateBuilder(repo.store.Renderer(), conversationAgreementTable).Set("payload_json", conversationJSON(entry)).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", d.ID), query.Equal("revision", entry.Revision))).Build()
	if err = conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestContractPublicationPreservesOriginalAndBindsActualPublisherWithExactRetry(t *testing.T) {
	repo, issuer, executor, d, root := contractPublicationFixture(t)
	original, err := repo.ConversationContractPublicationRecord(t.Context(), d.ID, 0, issuer)
	if err != nil || original.Agreement.Requirements == nil || len(original.Requirements.Sources) != 1 || original.Requirements.Sources[0] != root {
		t.Fatal("missing original requirements snapshot", original, err)
	}
	task, err := repo.ConversationTask(t.Context(), d.TaskID, executor)
	if err != nil {
		t.Fatal(err)
	}
	// Closed relationships may republish without altering the original result.
	d.Status, d.Decision = "accepted_delivery", "Original accepted decision"
	if err = repo.transaction(t.Context(), func(tx *sql.Tx) error { return repo.saveConversationDelegation(t.Context(), tx, d, d.Revision, issuer) }); err != nil {
		t.Fatal(err)
	}
	publisher := issuer
	publisher.RoleKey = "actual-contract-publisher"
	in := sdk.ConversationDelegationUpdate{ClientID: "explicit-original-contract", ExpectedRevision: d.Revision, Action: "republish_contract", Reason: "Share exact original", ContractPublication: &sdk.ConversationContractPublication{AgreementRevision: 1, RecordDigest: conversationHash(original)}}
	for _, kind := range []string{"digest", "revision", "actor", "mixed"} {
		bad := in
		selection := *in.ContractPublication
		bad.ContractPublication = &selection
		actor := publisher
		switch kind {
		case "digest":
			selection.RecordDigest = strings.Repeat("0", 64)
		case "revision":
			bad.ExpectedRevision++
		case "actor":
			actor = executor
		case "mixed":
			bad.Publication = &sdk.ConversationDeliveryPublication{}
		}
		if _, e := repo.UpdateConversationDelegation(t.Context(), d.ID, bad, actor); e == nil {
			t.Fatal("invalid publication committed", kind)
		}
	}
	out, err := repo.UpdateConversationDelegation(t.Context(), d.ID, in, publisher)
	if err != nil || out.Revision != d.Revision+1 {
		t.Fatal(out, err)
	}
	before, after := d, out
	before.Revision = after.Revision
	before.UpdatedAt = after.UpdatedAt
	if conversationHash(before) != conversationHash(after) {
		t.Fatal("publication changed original relationship", before, after)
	}
	currentTask, err := repo.ConversationTask(t.Context(), d.TaskID, executor)
	if err != nil || conversationHash(task) != conversationHash(currentTask) {
		t.Fatal("publication changed execution task", err)
	}
	stored, err := repo.ConversationContractPublicationRecord(t.Context(), d.ID, 1, publisher)
	if err != nil || conversationHash(stored) != conversationHash(original) {
		t.Fatal("publication rewrote immutable agreement", err)
	}
	replay, err := repo.UpdateConversationDelegation(t.Context(), d.ID, in, publisher)
	if err != nil || conversationHash(replay) != conversationHash(out) {
		t.Fatal("exact retry lost committed receipt", err)
	}
	history, err := NewConversationStore(repo.store).ConversationContractPublicationHistory(t.Context(), d.ID, 0, executor)
	if err != nil || len(history.Items) != 1 || history.Items[0].Publisher != publisher || history.Items[0].RecipientUserID != executor.UserID || history.Items[0].Reason != in.Reason {
		t.Fatal("incorrect publication audit", history, err)
	}
	releases, err := repo.ConversationSourceReleases(t.Context(), root, executor)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, release := range releases {
		found = found || release.Publisher != nil && *release.Publisher == publisher && release.Producer.RoleKey == "old-contract-proof" && release.Purpose == "contract"
	}
	if !found {
		t.Fatal("original proof replaced by current publication role", releases)
	}
}

func TestLegacyQueuedContractRecoversImmutableAdmissionNotCurrentMetadata(t *testing.T) {
	repo, issuer, _, d, root := contractPublicationFixture(t)
	legacy := legacyContractSnapshot(t, repo, d, issuer)
	d.Requirements = sdk.ConversationAgentRequirements{TaskType: "today-different-task"}
	q, args, err := query.NewUpdateBuilder(repo.store.Renderer(), conversationDelegationTable).Set("payload_json", conversationJSON(d)).Where(query.And(query.Equal("owner_key", conversationOwner(issuer)), query.Equal("delegation_id", d.ID))).Build()
	if err = conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
		t.Fatal(err)
	}
	record, err := NewConversationStore(repo.store).ConversationContractPublicationRecord(t.Context(), d.ID, 1, issuer)
	if err != nil || record.Agreement.Requirements != nil || record.Requirements.TaskType != "original-review" || len(record.Requirements.Sources) != 1 || record.Requirements.Sources[0] != root {
		t.Fatal("legacy declaration substituted today metadata", record, err)
	}
	history, err := repo.ConversationAgreementHistory(t.Context(), d.ID, 0, issuer)
	if err != nil || conversationHash(history.Items[0]) != conversationHash(legacy) {
		t.Fatal("virtual recovery changed legacy JSON", err)
	}
	// Removing the original receipt makes a queued legacy declaration unknown.
	q, args, err = query.NewDeleteBuilder(repo.store.Renderer(), conversationCollaborationMutationTable).Where(query.Equal("owner_key", conversationOwner(issuer))).Build()
	if err = conversationExec(t.Context(), repo.store.Database(), q, args, err); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ConversationContractPublicationRecord(t.Context(), d.ID, 1, issuer); err == nil {
		t.Fatal("unproven task/current declaration manufactured original requirements")
	}
}

func TestLegacyAssignmentFallbackReadsIssuerHistoryAcrossExecutionOwners(t *testing.T) {
	repo, issuer, executor, d, root := contractPublicationFixture(t)
	if err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
		q, args, err := query.NewDeleteBuilder(repo.store.Renderer(), conversationAssignmentTable).Where(query.And(query.Equal("owner_key", conversationOwner(issuer)), query.Equal("delegation_id", d.ID))).Build()
		if err = conversationExec(t.Context(), tx, q, args, err); err != nil {
			return err
		}
		entry := sdk.ConversationAgreementRevision{Revision: 1, Brief: d.Brief, FromUserID: issuer.UserID, Source: &root}
		q, args, err = query.NewUpdateBuilder(repo.store.Renderer(), conversationAgreementTable).Set("payload_json", conversationJSON(entry)).Where(query.And(query.Equal("owner_key", conversationOwner(issuer)), query.Equal("delegation_id", d.ID), query.Equal("revision", 1))).Build()
		return conversationExec(t.Context(), tx, q, args, err)
	}); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []sdk.ConversationAuthority{issuer, executor} {
		assignments, err := repo.ConversationDelegationAssignments(t.Context(), d.ID, reader)
		if err != nil || len(assignments) != 1 || assignments[0].ActorID != issuer.UserID || assignments[0].Source == nil || *assignments[0].Source != root {
			t.Fatal("reader owner replaced original issuer history or could not resolve execution-owned task", reader, assignments, err)
		}
	}
}
