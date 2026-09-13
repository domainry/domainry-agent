package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func TestSourceReleasesPersistOnlyExplicitReferencesAndBoundedPrefixes(t *testing.T) {
	repo, producer, reader, in := delegationSubjectFixture(t)
	run, err := repo.Enqueue(t.Context(), in.Request.ConversationID, sdk.ConversationSend{ClientMessageID: "source-evidence", Message: "Contract source"}, producer)
	if err != nil {
		t.Fatal(err)
	}
	d, err := repo.CreateConversationDelegation(t.Context(), in, producer)
	if err != nil {
		t.Fatal(err)
	}
	d.BriefSource = &sdk.ConversationRunReference{ConversationID: in.Request.ConversationID, RunID: run.ID, BeforeStep: 3}
	d.InputSource = d.BriefSource
	if err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
		return repo.saveConversationDelegation(t.Context(), tx, d, d.Revision, producer)
	}); err != nil {
		t.Fatal("source-bearing contract persistence", err)
	}
	// Reopen the repository and replay admission; grants stay immutable and unique.
	repo = NewConversationStore(repo.store)
	if again, err := repo.CreateConversationDelegation(t.Context(), in, producer); err != nil || again.ID != d.ID {
		t.Fatal("admission replay", again, err)
	}
	for _, before := range []int{1, 2, 3} {
		items, err := repo.ConversationSourceReleases(t.Context(), sdk.ConversationRunReference{ConversationID: in.Request.ConversationID, RunID: run.ID, BeforeStep: before}, reader)
		if err != nil || len(items) != 1 || items[0].Producer != producer || items[0].DelegationID != d.ID || items[0].Purpose != "contract" || items[0].Reference.BeforeStep != 3 {
			t.Fatal("lost exact publisher and prefix", before, items, err)
		}
	}
	for _, ref := range []sdk.ConversationRunReference{{ConversationID: in.Request.ConversationID, RunID: run.ID}, {ConversationID: in.Request.ConversationID, RunID: run.ID, BeforeStep: 4}, {ConversationID: in.Request.ConversationID, RunID: "unpublished", BeforeStep: 2}} {
		if items, err := repo.ConversationSourceReleases(t.Context(), ref, reader); err != nil || len(items) != 0 {
			t.Fatal("reference expanded beyond explicit source release", ref, items, err)
		}
	}
	other := reader
	other.UserID = "unrelated"
	if items, err := repo.ConversationSourceReleases(t.Context(), *d.BriefSource, other); err != nil || len(items) != 0 {
		t.Fatal("another user acquired the release", items, err)
	}
	if _, err := repo.Run(t.Context(), in.Request.ConversationID, run.ID, reader); err == nil {
		t.Fatal("evidence release exposed the producer's raw run")
	}
	// Removing the producer also removes releases held under another reader.
	lifecycle := NewSubjectLifecycle(repo.store, producer.RuntimeID)
	preview, err := lifecycle.PreviewSubject(t.Context(), producer.WorkspaceID, producer.UserID)
	var counts map[string]int64
	if err != nil || json.Unmarshal(preview, &counts) != nil || counts[conversationSourceReleaseTable] != 1 {
		t.Fatal("producer preview omitted issued source releases", string(preview), err)
	}
	if _, err := lifecycle.EraseSubjectForRequest(t.Context(), "erase-source-producer", producer.WorkspaceID, producer.UserID, nil); err != nil {
		t.Fatal(err)
	}
	if items, err := repo.ConversationSourceReleases(t.Context(), *d.BriefSource, reader); err != nil || len(items) != 0 {
		t.Fatal("producer erasure left a foreign-reader source release", items, err)
	}
}

func TestSourceReleaseTransactionRollsBackWhenOriginRunIsNotOwnedByPublisher(t *testing.T) {
	repo, producer, reader, in := delegationSubjectFixture(t)
	owned, err := repo.Enqueue(t.Context(), in.Request.ConversationID, sdk.ConversationSend{ClientMessageID: "owned-source", Message: "Source"}, producer)
	if err != nil {
		t.Fatal(err)
	}
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "reader-private"}, reader)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "reader-private", Message: "Private"}, reader)
	if err != nil {
		t.Fatal(err)
	}
	refs := []sdk.ConversationRunReference{{ConversationID: in.Request.ConversationID, RunID: owned.ID, BeforeStep: 1}, {ConversationID: c.ID, RunID: run.ID, BeforeStep: 1}}
	if err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
		return repo.saveSourceReleases(t.Context(), tx, "test-atomic-release", "contract", producer, reader, refs)
	}); err == nil {
		t.Fatal("publisher released another user's raw source run")
	}
	for _, ref := range refs {
		if items, err := repo.ConversationSourceReleases(t.Context(), ref, reader); err != nil || len(items) != 0 {
			t.Fatal("failed source release left a partial grant", ref, items, err)
		}
	}
}

func TestSourceAuthorityResolvesUnpublishedOldRoleUnderExactOwnerScope(t *testing.T) {
	repo, owner, current, in := delegationSubjectFixture(t, true)
	original := owner
	original.RoleKey = "old-professional-role"
	run, err := repo.Enqueue(t.Context(), in.Request.ConversationID, sdk.ConversationSend{ClientMessageID: "unpublished-old-role", Message: "Old-role source"}, original)
	if err != nil {
		t.Fatal(err)
	}
	ref := sdk.ConversationRunReference{ConversationID: in.Request.ConversationID, RunID: run.ID, BeforeStep: 2}
	repo = NewConversationStore(repo.store)
	got, err := repo.ConversationSourceAuthority(t.Context(), ref, current)
	if err != nil || got != original {
		t.Fatal("unpublished old role was replaced by the lookup role", got, err)
	}
	if releases, err := repo.ConversationSourceReleases(t.Context(), ref, current); err != nil || len(releases) != 0 {
		t.Fatal("metadata lookup created a publication", releases, err)
	}
	for _, change := range []string{"user", "workspace", "runtime", "unknown"} {
		other := current
		switch change {
		case "user":
			other.UserID = "unrelated"
		case "workspace":
			other.WorkspaceID = "unrelated"
		case "runtime":
			other.RuntimeID = "unrelated"
		case "unknown":
			other.Known = false
		}
		if _, err := repo.ConversationSourceAuthority(t.Context(), ref, other); err == nil {
			t.Fatal("private provenance lookup escaped the original owner", change)
		}
	}
	for _, invalid := range []sdk.ConversationRunReference{{RunID: run.ID}, {ConversationID: ref.ConversationID}, {ConversationID: ref.ConversationID, RunID: run.ID, BeforeStep: -1}, {ConversationID: ref.ConversationID, RunID: run.ID, BeforeStep: 258}, {ConversationID: ref.ConversationID, RunID: "absent", BeforeStep: 2}} {
		if _, err := repo.ConversationSourceAuthority(t.Context(), invalid, current); err == nil {
			t.Fatal("invalid source provenance reference was accepted", invalid)
		}
	}
}

func TestSourceReleasesSameIdentityDelegationWithoutSubjectRowPublishesOriginalOldRole(t *testing.T) {
	repo, actor, _, in := delegationSubjectFixture(t, true)
	in.ExecutionAuthority = &actor
	d, err := repo.CreateConversationDelegation(t.Context(), in, actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.delegationSubjects(t.Context(), repo.store.Database(), d.ID, actor); err != nil || found {
		t.Fatal("fixture did not exercise the same-identity subject fallback", found, err)
	}
	original := actor
	original.RoleKey = "old-source-role"
	run, err := repo.Enqueue(t.Context(), in.Request.ConversationID, sdk.ConversationSend{ClientMessageID: "old-subject-fallback", Message: "Old source"}, original)
	if err != nil {
		t.Fatal(err)
	}
	ref := sdk.ConversationRunReference{ConversationID: in.Request.ConversationID, RunID: run.ID, BeforeStep: 2}
	d.BriefSource = &ref
	d.Delivery = &sdk.ConversationDelegationDelivery{Evidence: []sdk.ConversationRunReference{ref}}
	if err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
		if err := repo.saveDelegationContractReleases(t.Context(), tx, d, actor); err != nil {
			return err
		}
		return repo.saveDelegationDeliveryReleases(t.Context(), tx, d, actor)
	}); err != nil {
		t.Fatal(err)
	}
	releases, err := NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), ref, actor)
	if err != nil || len(releases) != 2 {
		t.Fatal("same identity skipped the old-role publication ledger", releases, err)
	}
	for _, release := range releases {
		if release.Producer != original || release.DelegationID != d.ID || release.Reference != ref {
			t.Fatal("fallback publication replaced original proof provenance", release)
		}
	}
}

func TestSourceReleaseAdmissionPublishesRequiredMixedOldRootsAndRollsBackUnknownRoots(t *testing.T) {
	repo, issuer, _, in := delegationSubjectFixture(t, true)
	in.ExecutionAuthority = &issuer
	old := issuer
	for _, role := range []string{"old-report", "old-analysis"} {
		old.RoleKey = role
		conversation, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: role}, old)
		if err != nil {
			t.Fatal(err)
		}
		run, err := repo.Enqueue(t.Context(), conversation.ID, sdk.ConversationSend{ClientMessageID: role, Message: "Original source"}, old)
		if err != nil {
			t.Fatal(err)
		}
		in.Request.Requirements.Sources = append(in.Request.Requirements.Sources, sdk.ConversationRunReference{ConversationID: conversation.ID, RunID: run.ID, BeforeStep: 2})
	}
	in.Task.Requirements = in.Request.Requirements
	d, err := repo.CreateConversationDelegation(t.Context(), in, issuer)
	if err != nil {
		t.Fatal(err)
	}
	for i, ref := range in.Request.Requirements.Sources {
		items, err := NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), ref, issuer)
		if err != nil || len(items) != 1 || items[0].Producer.RoleKey != []string{"old-report", "old-analysis"}[i] || items[0].Reference != ref || items[0].DelegationID != d.ID || items[0].Purpose != "contract" {
			t.Fatal("required root lost its original publication", items, err)
		}
	}
	broken := in
	broken.Request.ClientID = "bad-required-source"
	broken.Request.Requirements.Sources = append(append([]sdk.ConversationRunReference{}, in.Request.Requirements.Sources...), sdk.ConversationRunReference{ConversationID: in.Request.ConversationID, RunID: "unknown"})
	broken.Task.Requirements = broken.Request.Requirements
	if _, err := repo.CreateConversationDelegation(t.Context(), broken, issuer); err == nil {
		t.Fatal("unknown required source created a delegation")
	} else {
		var failure *sdk.Error
		if !errors.As(err, &failure) || failure.Class != "not_found" {
			t.Fatal("admission failed before checking the unknown original source", err)
		}
	}
	for _, ref := range in.Request.Requirements.Sources {
		items, err := repo.ConversationSourceReleases(t.Context(), ref, issuer)
		if err != nil || len(items) != 1 {
			t.Fatal("failed admission left partial publication", items, err)
		}
	}
}

func TestSourceReleasesSameUserDifferentRolesPersistOriginalExecutionProvenance(t *testing.T) {
	repo, issuer, executor, in := delegationSubjectFixture(t, true)
	run, err := repo.Enqueue(t.Context(), in.Request.ConversationID, sdk.ConversationSend{ClientMessageID: "role-source", Message: "Original role source"}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	d, err := repo.CreateConversationDelegation(t.Context(), in, issuer)
	if err != nil {
		t.Fatal(err)
	}
	d.BriefSource = &sdk.ConversationRunReference{ConversationID: in.Request.ConversationID, RunID: run.ID, BeforeStep: 2}
	d.InputSource = d.BriefSource
	if err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
		return repo.saveConversationDelegation(t.Context(), tx, d, d.Revision, issuer)
	}); err != nil {
		t.Fatal(err)
	}
	repo = NewConversationStore(repo.store)
	subjects, err := repo.ConversationDelegationAuthorities(t.Context(), d.ID, executor)
	if err != nil || subjects.Issuer != issuer || subjects.Executor != executor {
		t.Fatal("same storage owner collapsed the two selected roles", subjects, err)
	}
	items, err := repo.ConversationSourceReleases(t.Context(), *d.BriefSource, executor)
	if err != nil || len(items) != 1 || items[0].Producer != issuer || items[0].Purpose != "contract" {
		t.Fatal("same-user release lost producer role after reopen", items, err)
	}
	snapshot, err := repo.ConversationSourceSnapshot(t.Context(), *d.BriefSource, executor)
	if err != nil || snapshot.Authority != issuer {
		t.Fatal("snapshot inherited the reader role instead of original execution", snapshot.Authority, err)
	}
	if raw, err := json.Marshal(snapshot); err != nil || strings.Contains(string(raw), issuer.RoleKey) {
		t.Fatal("internal routing authority leaked into snapshot JSON", string(raw), err)
	}
	launch, found, err := repo.LaunchConversationTask(t.Context(), issuer.RuntimeID)
	if err != nil || !found || launch.Authority != executor {
		t.Fatal("same-user task launch changed the selected execution role", launch, err)
	}
}

func TestSourceReleasesUseImmutableRunRoleWhenPublisherAndReaderUseAnotherRole(t *testing.T) {
	repo, original, reader, in := delegationSubjectFixture(t, true)
	run, err := repo.Enqueue(t.Context(), in.Request.ConversationID, sdk.ConversationSend{ClientMessageID: "original-role", Message: "Original role"}, original)
	if err != nil {
		t.Fatal(err)
	}
	ref := sdk.ConversationRunReference{ConversationID: in.Request.ConversationID, RunID: run.ID, BeforeStep: 2}
	if err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
		return repo.saveSourceReleases(t.Context(), tx, "original-role-publication", "message", reader, reader, []sdk.ConversationRunReference{ref})
	}); err != nil {
		t.Fatal(err)
	}
	items, err := NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), ref, reader)
	if err != nil || len(items) != 1 || items[0].Producer != original || items[0].Publisher == nil || *items[0].Publisher != reader {
		t.Fatal("publication rewrote original proof role to the current publisher", items, err)
	}
}
