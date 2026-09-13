package remote

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/server"
)

type participantGrantService struct {
	releasedResultService
	update sdk.ConversationDelegationUpdate
}

func (s *participantGrantService) UpdateConversationDelegation(_ context.Context, id string, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegationDetail, error) {
	s.calls++
	s.id, s.authority, s.update = id, a, in
	if s.denied {
		return sdk.ConversationDelegationDetail{}, &sdk.Error{Class: "forbidden", Code: "agent.conversation.delegation_owner_required"}
	}
	out := sdk.ConversationDelegationDetail{ParticipantsOnly: true, ConversationDelegation: sdk.ConversationDelegation{ID: id, OwnerUserID: a.UserID, Revision: in.ExpectedRevision + 1, ParticipantsRevision: 3, ExecutionSubject: &sdk.ConversationExecutionSubject{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: a.UserID}}}
	if in.Participants != nil {
		for _, p := range *in.Participants {
			out.Participants = append(out.Participants, sdk.ConversationDelegationParticipant{UserID: p.UserID, Operations: p.Operations, Revision: 3})
		}
	}
	return out, nil
}

func TestSaaSDelegationParticipantsPreserveActorGrantsAndCurrentDenial(t *testing.T) {
	source := &participantGrantService{}
	svc, err := server.New(server.Config{APIKey: "participant-runtime-secret", Conversations: source, ConversationRuntimeID: "runtime", ConversationWorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(svc.Handler())
	defer h.Close()
	opened, err := NewFactory(Options{BaseURL: h.URL, APIKey: "participant-runtime-secret", Client: h.Client()}).OpenSaaS(t.Context(), sdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close(context.Background())
	client := opened.(sdk.ConversationBinding).Conversations().(sdk.ConversationCollaborationService)
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "issuer"}
	members := []sdk.ConversationDelegationParticipantInput{{UserID: "participant", Operations: []string{"view", "communicate"}}}
	empty := []sdk.ConversationDelegationParticipantInput{}
	for _, participants := range []*[]sdk.ConversationDelegationParticipantInput{&members, nil, &empty} {
		in := sdk.ConversationDelegationUpdate{ClientID: "membership-change", Action: "set_participants", ExpectedRevision: 7, Reason: "Review this task", Participants: participants}
		out, err := client.UpdateConversationDelegation(t.Context(), "delegation", in, a)
		if err != nil || !out.ParticipantsOnly || source.authority != a || source.id != "delegation" || !reflect.DeepEqual(source.update, in) || out.OwnerUserID != a.UserID || out.ExecutionSubject == nil || out.ExecutionSubject.UserID != a.UserID || out.ParticipantsRevision != 3 {
			t.Fatal("SaaS changed actual actor or participation", out, err)
		}
		if participants == &members && (len(out.Participants) != 1 || out.Participants[0].Revision != 3 || out.Participants[0].UserID != "participant") {
			t.Fatal("SaaS lost server-owned grant revision", out)
		}
	}
	source.denied = true
	out, err := client.UpdateConversationDelegation(t.Context(), "delegation", sdk.ConversationDelegationUpdate{}, a)
	var coded *sdk.Error
	if !errors.As(err, &coded) || coded.Code != "agent.conversation.delegation_owner_required" || out.ID != "" {
		t.Fatal("current owner denial lost", out, err)
	}
	before := source.calls
	a.WorkspaceID = "foreign"
	if _, err := client.UpdateConversationDelegation(t.Context(), "delegation", sdk.ConversationDelegationUpdate{}, a); err == nil || before != source.calls {
		t.Fatal("foreign workspace reached membership write", err)
	}
}
