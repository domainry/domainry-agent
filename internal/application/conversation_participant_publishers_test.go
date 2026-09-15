package application

import (
	"context"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type participantPublicationTestRepository struct {
	*contractPublicationTestRepository
	writes int
}

func (r *participantPublicationTestRepository) SetConversationDelegationParticipants(_ context.Context, _ string, _ sdk.ConversationDelegationUpdate, _ sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	r.writes++
	return r.d, nil
}

func (r *participantPublicationTestRepository) SupersedeConversationParticipantMessage(context.Context, string, sdk.ConversationAuthority) error {
	return nil
}

var _ persistence.ConversationDelegationParticipantRepository = (*participantPublicationTestRepository)(nil)

func TestParticipantFirstPublicationChecksOriginalSourcesWithActualNewPublisher(t *testing.T) {
	s, original, policy, publisher, _ := contractPublicationServiceFixture()
	original.d.AgreementRevision = original.original.Agreement.Revision
	oldPublisher := publisher
	oldPublisher.RoleKey = "retired-publishing-role"
	original.publicationAuthority = &oldPublisher
	policy.deniedRole = oldPublisher.RoleKey
	repo := &participantPublicationTestRepository{contractPublicationTestRepository: original}
	s.repo = repo
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: "participant", Operations: []string{"view"}}}
	in := sdk.ConversationDelegationUpdate{ClientID: "explicit-new-sharing", ExpectedRevision: repo.d.Revision, Action: "set_participants", Participants: &grants}
	if _, err := s.updateDelegationParticipants(t.Context(), repo.d, in, publisher); err != nil || repo.writes != 1 || len(repo.reads) == 0 {
		t.Fatal("new sharing reused a retired publisher rather than checking exact original sources", err, repo.writes, repo.reads)
	}
	policy.deniedRole = "old-professional"
	if _, err := s.updateDelegationParticipants(t.Context(), repo.d, in, publisher); !collaborationDenied(err) || repo.writes != 1 {
		t.Fatal("new sharing borrowed the original source's withdrawn permission", err, repo.writes)
	}
}

func TestParticipantGrantRechecksActualPublisherWithoutBorrowingReaderRole(t *testing.T) {
	reader := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "participant", RoleKey: "reading-role"}
	publisher := reader
	publisher.UserID, publisher.RoleKey = "owner", "granting-role"
	d := sdk.ConversationDelegation{ID: "work", OwnerUserID: publisher.UserID, Participants: []sdk.ConversationDelegationParticipant{{UserID: reader.UserID, Revision: 3, Operations: []string{"view", "communicate"}, Publisher: &publisher}}}
	policy := &executionBindingTestPolicy{deniedRole: "revoked"}
	s := &ConversationService{runtimeID: reader.RuntimeID, options: ConversationOptions{CollaborationAuthorizer: policy}}
	if err := s.authorizeCollaboration(t.Context(), "view", &d, reader); err != nil {
		t.Fatal("valid explicit publisher did not release the agreement", err)
	}
	policy.deniedRole = publisher.RoleKey
	if err := s.authorizeCollaboration(t.Context(), "view", &d, reader); !collaborationDenied(err) {
		t.Fatal("withdrawn actual grant publisher still released the agreement", err)
	}
	policy.deniedRole = reader.RoleKey
	if err := s.authorizeCollaboration(t.Context(), "communicate", &d, reader); !collaborationDenied(err) {
		t.Fatal("publisher sharing substituted the recipient's withdrawn role", err)
	}
	policy.deniedRole = "revoked"
	for _, mutation := range []string{"unknown", "role", "user", "workspace", "runtime"} {
		t.Run(mutation, func(t *testing.T) {
			invalid := publisher
			switch mutation {
			case "unknown":
				invalid.Known = false
			case "role":
				invalid.RoleKey = ""
			case "user":
				invalid.UserID = "unrelated"
			case "workspace":
				invalid.WorkspaceID = "foreign"
			case "runtime":
				invalid.RuntimeID = "foreign"
			}
			d.Participants[0].Publisher = &invalid
			if err := s.authorizeCollaboration(t.Context(), "view", &d, reader); !collaborationDenied(err) {
				t.Fatal("invalid publisher was replaced by the owner or reader", invalid, err)
			}
		})
	}
	d.Participants[0].Publisher = nil
	if err := s.authorizeCollaboration(t.Context(), "view", &d, reader); !collaborationDenied(err) {
		t.Fatal("an unknown legacy grant publisher was guessed", err)
	}
}

func TestParticipantContractChecksFrozenRequiredSourcesAndKeepsPrivateIDsHidden(t *testing.T) {
	s, repo, _, owner, _ := contractPublicationServiceFixture()
	reader := owner
	reader.UserID, reader.RoleKey = "participant", "participant-role"
	repo.d.Participants = []sdk.ConversationDelegationParticipant{{UserID: reader.UserID, Revision: 2, Operations: []string{"view"}, Publisher: &owner}}
	repo.d.ConversationID, repo.d.SourceConversationID = "private-execution", "private-source"
	repo.d.AgreementRevision = repo.original.Agreement.Revision
	// Today's row has no roots. The immutable agreement still has the exact
	// protected root, which must be audited rather than silently discarded.
	repo.d.Requirements.Sources = nil
	visible, err := s.projectConversationDelegation(t.Context(), repo.d, reader)
	if err != nil || visible.ContractOmitted || visible.Brief.Goal != repo.original.Agreement.Brief.Goal || visible.Task != nil || visible.Delivery != nil || visible.ConversationID != "" || visible.SourceConversationID != "" {
		t.Fatal("exact shared agreement or private execution separation lost", visible, err)
	}
	if len(repo.reads) == 0 {
		t.Fatal("participant projection skipped the frozen required root")
	}
	repo.sourceErr = conversationFailure("forbidden", "current_field_access_denied")
	hidden, err := s.projectConversationDelegation(t.Context(), repo.d, reader)
	if err != nil || !hidden.ContractOmitted || hidden.Brief.Goal != "" || hidden.Task != nil || hidden.Delivery != nil || hidden.ConversationID != "" || hidden.SourceConversationID != "" || len(hidden.Messages) != 0 {
		t.Fatal("withdrawn required source exposed derived agreement or private IDs", hidden, err)
	}
}
