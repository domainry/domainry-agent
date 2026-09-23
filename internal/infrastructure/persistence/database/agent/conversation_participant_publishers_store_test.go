package agent

import (
	"database/sql"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

func TestParticipantGrantKeepsActualPublisherAndRepublicationEpoch(t *testing.T) {
	repo, owner, d := peerFixture(t)
	reader := owner
	reader.UserID, reader.RoleKey = "participant", "reading-role"
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: reader.UserID, Operations: []string{"view", "communicate"}}}
	in := sdk.ConversationDelegationUpdate{ClientID: "original-invite", ExpectedRevision: d.Revision, Action: "set_participants", Reason: "Explicit grant", Participants: &grants}
	first, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, in, owner)
	if err != nil || len(first.Participants) != 1 || first.Participants[0].Publisher == nil || *first.Participants[0].Publisher != owner {
		t.Fatal("grant omitted its actual publishing role", first.Participants, err)
	}
	original := first.Participants[0].Revision
	repo = NewConversationStore(repo.store)
	replay, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, in, owner)
	if err != nil || replay.Revision != first.Revision || *replay.Participants[0].Publisher != owner {
		t.Fatal("restart or retry replaced the original publisher", replay, err)
	}
	in.ClientID, in.ExpectedRevision = "same-publisher", first.Revision
	same, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, in, owner)
	if err != nil || same.Participants[0].Revision != original {
		t.Fatal("unchanged publisher invalidated an unchanged grant", same, err)
	}
	message, err := repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: "original-grant-message", ToAgentID: d.ToAgentID, BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, Content: "Original authorization epoch"}, "", reader)
	if err != nil {
		t.Fatal(err)
	}
	next := owner
	next.RoleKey = "new-granting-role"
	in.ClientID, in.ExpectedRevision = "new-publisher", same.Revision
	changed, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, in, next)
	if err != nil || changed.Participants[0].Revision == original || changed.Participants[0].Publisher == nil || *changed.Participants[0].Publisher != next {
		t.Fatal("explicit republication reused the old publishing epoch", changed, err)
	}
	read, err := NewConversationStore(repo.store).ConversationDelegation(t.Context(), d.ID, reader)
	if err != nil || *read.Participants[0].Publisher != next || first.Participants[0].Publisher == nil || *first.Participants[0].Publisher != owner {
		t.Fatal("current reader changed the grant publisher or immutable original receipt", read, first, err)
	}
	messages, err := repo.ConversationAgentMessages(t.Context(), d.ID, owner)
	if err != nil || len(messages) != 1 || messages[0].ID != message.ID || !messages[0].Superseded || messages[0].Content != message.Content || messages[0].ConsumedByRunID != "" {
		t.Fatal("a new actual publisher revived old pending input", messages, err)
	}
}

func TestParticipantPublicationUsesExactContractRootsAndAllowsRevocationWithoutOldSources(t *testing.T) {
	repo, owner, _, d, root := contractPublicationFixture(t)
	reader := owner
	reader.UserID, reader.RoleKey = "participant", "participant-role"
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: reader.UserID, Operations: []string{"view", "communicate"}}}
	in := sdk.ConversationDelegationUpdate{ClientID: "share-original-root", ExpectedRevision: d.Revision, Action: "set_participants", Reason: "Share exact contract", Participants: &grants}
	first, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, in, owner)
	if err != nil {
		t.Fatal(err)
	}
	repo = NewConversationStore(repo.store)
	releases, err := repo.ConversationSourceReleases(t.Context(), root, reader)
	if err != nil || len(releases) != 1 || releases[0].Reference != root || releases[0].Publisher == nil || *releases[0].Publisher != owner || releases[0].Producer.RoleKey != "old-contract-proof" || releases[0].Purpose != "contract" || releases[0].DelegationID != d.ID {
		t.Fatal("participant lost bounded original source publication", releases, err)
	}
	beyond := root
	beyond.BeforeStep++
	if releases, err := repo.ConversationSourceReleases(t.Context(), beyond, reader); err != nil || len(releases) != 0 {
		t.Fatal("participant acquired the publisher's raw future execution", releases, err)
	}
	if _, err := repo.Run(t.Context(), root.ConversationID, root.RunID, reader); err == nil {
		t.Fatal("shared agreement opened a raw private run")
	}
	// The exact original agreement may be unavailable. Reducing a grant must
	// still work; adding a new user must neither guess roots nor half-commit.
	if err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
		q, args, err := query.NewDeleteBuilder(repo.store.Renderer(), conversationItemTable).Where(delegationHistoryPredicate(conversationOwner(owner), conversationItemDelegationAgreement, d.ID, "")).Build()
		return conversationExec(t.Context(), tx, q, args, err)
	}); err != nil {
		t.Fatal(err)
	}
	grants[0].Operations = []string{"view"}
	in.ClientID, in.ExpectedRevision = "reduce-unreadable-scope", first.Revision
	revokingRole := owner
	revokingRole.RoleKey = "different-revoking-role"
	reduced, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, in, revokingRole)
	if err != nil || len(reduced.Participants) != 1 || len(reduced.Participants[0].Operations) != 1 || reduced.Participants[0].Publisher == nil || *reduced.Participants[0].Publisher != owner {
		t.Fatal("revocation depended on retired original data", reduced, err)
	}
	grants = append(grants, sdk.ConversationDelegationParticipantInput{UserID: "new-recipient", Operations: []string{"view"}})
	in.ClientID, in.ExpectedRevision, in.Participants = "new-unverified-recipient", reduced.Revision, &grants
	if _, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, in, owner); err == nil {
		t.Fatal("missing exact original sources silently authorized a new user")
	}
	current, err := repo.ConversationDelegation(t.Context(), d.ID, owner)
	if err != nil || current.Revision != reduced.Revision || len(current.Participants) != 1 {
		t.Fatal("failed sharing committed a partial participant grant", current, err)
	}
}

func TestLegacyParticipantGrantRequiresExplicitNewPublication(t *testing.T) {
	repo, owner, d := peerFixture(t)
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: "participant", Operations: []string{"view"}}}
	in := sdk.ConversationDelegationUpdate{ClientID: "known-publication", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}
	first, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, in, owner)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := repo.ConversationDelegation(t.Context(), d.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Participants[0].Publisher = nil
	if err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
		q, args, err := query.NewUpdateBuilder(repo.store.Renderer(), conversationPeerLinkTable).Set("payload_json", conversationJSON(legacy)).Where(query.And(query.Equal("link_kind", conversationPeerLinkKindDelegation), query.Equal("owner_key", conversationOwner(owner)), query.Equal("link_id", d.ID))).Build()
		return conversationExec(t.Context(), tx, q, args, err)
	}); err != nil {
		t.Fatal(err)
	}
	repo = NewConversationStore(repo.store)
	read, err := repo.ConversationDelegation(t.Context(), d.ID, owner)
	if err != nil || read.Participants[0].Publisher != nil {
		t.Fatal("legacy reading guessed today's granting role", read, err)
	}
	in.ClientID, in.ExpectedRevision = "explicit-legacy-publication", first.Revision
	next := owner
	next.RoleKey = "explicit-granting-role"
	shared, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, in, next)
	if err != nil || shared.Participants[0].Publisher == nil || *shared.Participants[0].Publisher != next || shared.Participants[0].Revision == first.Participants[0].Revision || first.Participants[0].Publisher == nil || *first.Participants[0].Publisher != owner {
		t.Fatal("explicit legacy sharing lost its new publisher or original receipt", shared, first, err)
	}
}
