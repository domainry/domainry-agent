package remote

import (
	"context"
	"net/http/httptest"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/server"
)

type executionSharingRemoteFixture struct {
	releasedResultService
	share  sdk.ConversationExecutionShare
	ref    sdk.ConversationRunReference
	result sdk.ConversationResultRead
}

func (s *executionSharingRemoteFixture) check(id string, a sdk.ConversationAuthority) error {
	s.id, s.authority = id, a
	s.calls++
	if s.denied {
		return &sdk.Error{Class: "forbidden", Code: "agent.conversation.execution_not_shared"}
	}
	return nil
}
func (s *executionSharingRemoteFixture) PublishConversationDelegationExecution(_ context.Context, id string, in sdk.ConversationExecutionShare, a sdk.ConversationAuthority) (sdk.ConversationExecutionPublication, error) {
	s.share = in
	err := s.check(id, a)
	if err != nil {
		return sdk.ConversationExecutionPublication{}, err
	}
	return sdk.ConversationExecutionPublication{Reference: in.Reference, Publisher: a, Revision: in.ExpectedRevision + 1, Withdrawn: in.Withdraw}, nil
}
func (s *executionSharingRemoteFixture) ConversationDelegationExecutions(_ context.Context, id string, a sdk.ConversationAuthority) ([]sdk.ConversationExecutionPublication, error) {
	err := s.check(id, a)
	if err != nil {
		return nil, err
	}
	return []sdk.ConversationExecutionPublication{{Reference: s.ref, Publisher: a}}, nil
}

func (s *executionSharingRemoteFixture) ConversationDelegationExecutionPublications(_ context.Context, id string, a sdk.ConversationAuthority) ([]sdk.ConversationExecutionPublication, error) {
	if err := s.check(id, a); err != nil {
		return nil, err
	}
	return []sdk.ConversationExecutionPublication{{Reference: s.ref, Publisher: a}}, nil
}
func (s *executionSharingRemoteFixture) ReadConversationDelegationExecution(_ context.Context, id string, in sdk.ConversationRunReference, a sdk.ConversationAuthority) (sdk.ConversationRun, error) {
	s.ref = in
	err := s.check(id, a)
	if err != nil {
		return sdk.ConversationRun{}, err
	}
	return sdk.ConversationRun{ID: in.RunID, ConversationID: in.ConversationID, Status: "running"}, nil
}
func (s *executionSharingRemoteFixture) ReadConversationDelegationExecutionResult(_ context.Context, id string, in sdk.ConversationResultRead, a sdk.ConversationAuthority) (sdk.ConversationResultSlice, error) {
	s.result = in
	err := s.check(id, a)
	if err != nil {
		return sdk.ConversationResultSlice{}, err
	}
	return sdk.ConversationResultSlice{Reference: in.Reference, JSONText: "exact", Offset: in.Offset, NextOffset: in.Offset + 5, TotalBytes: in.Offset + 5, Complete: true}, nil
}

func TestSaaSExecutionSharingPreservesActualAuthorityAndScopedRequestsAndCurrentDenial(t *testing.T) {
	source := &executionSharingRemoteFixture{}
	svc, err := server.New(server.Config{APIKey: "execution-sharing-fixture-key", Conversations: source, ConversationRuntimeID: "runtime", ConversationWorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(svc.Handler())
	defer httpServer.Close()
	opened, err := NewFactory(Options{BaseURL: httpServer.URL, APIKey: "execution-sharing-fixture-key", Client: httpServer.Client()}).OpenSaaS(t.Context(), sdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close(context.Background())
	sharing, ok := opened.(sdk.ConversationBinding).Conversations().(sdk.ConversationExecutionSharingService)
	if !ok {
		t.Fatal("SaaS omitted execution sharing")
	}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "actual-reader", RoleKey: "reading-role"}
	ref := sdk.ConversationRunReference{ConversationID: "actual-execution", RunID: "original-run"}
	in := sdk.ConversationExecutionShare{Reference: ref, ExpectedRevision: 7, ClientID: "publish-exact", Reason: "明确共享", Withdraw: true}
	pub, err := sharing.PublishConversationDelegationExecution(t.Context(), "delegation", in, a)
	if err != nil || source.share != in || source.authority != a || source.id != "delegation" || pub.Publisher != a || !pub.Withdrawn {
		t.Fatal("sharing request changed", pub, err)
	}
	run, err := sharing.ReadConversationDelegationExecution(t.Context(), "delegation", ref, a)
	if err != nil || run.ID != ref.RunID || source.ref != ref || source.authority != a {
		t.Fatal("run scope changed", run, err)
	}
	items, err := sharing.ConversationDelegationExecutions(t.Context(), "delegation", a)
	if err != nil || len(items) != 1 || items[0].Reference != ref {
		t.Fatal("publication list changed", items, err)
	}
	owner, ok := sharing.(sdk.ConversationExecutionPublicationOwnerService)
	if !ok {
		t.Fatal("SaaS omitted publication owner controls")
	}
	owned, err := owner.ConversationDelegationExecutionPublications(t.Context(), "delegation", a)
	if err != nil || len(owned) != 1 || owned[0].Publisher != a || source.authority != a {
		t.Fatal("owner metadata authority changed", owned, err)
	}
	result := sdk.ConversationResultRead{Reference: sdk.ConversationResultReference{ConversationID: ref.ConversationID, RunID: ref.RunID, Step: 3, CallID: "exact-call", SHA256: "original-digest"}, Offset: 256, MaxBytes: 512}
	page, err := sharing.ReadConversationDelegationExecutionResult(t.Context(), "delegation", result, a)
	if err != nil || page.Reference != result.Reference || source.result != result || source.authority != a {
		t.Fatal("exact original page changed", page, err)
	}
	source.denied = true
	if owned, err := owner.ConversationDelegationExecutionPublications(t.Context(), "delegation", a); err == nil || len(owned) != 0 {
		t.Fatal("current owner metadata denial hidden", owned, err)
	}
	if run, err := sharing.ReadConversationDelegationExecution(t.Context(), "delegation", ref, a); err == nil || run.ID != "" {
		t.Fatal("current source denial hidden", run, err)
	}
	if page, err := sharing.ReadConversationDelegationExecutionResult(t.Context(), "delegation", result, a); err == nil || page.JSONText != "" {
		t.Fatal("current result denial hidden", page, err)
	}
	calls := source.calls
	a.WorkspaceID = "foreign"
	if _, err := sharing.ConversationDelegationExecutions(t.Context(), "delegation", a); err == nil || source.calls != calls {
		t.Fatal("foreign workspace reached source", err)
	}
}
