package remote

import (
	"context"
	"net/http/httptest"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/server"
)

type remoteContractPublicationService struct {
	releasedResultService
	publication sdk.ConversationContractPublicationRequest
	before      int64
	actor       sdk.ConversationAuthority
	id          string
	denied      bool
}

func (s *remoteContractPublicationService) PreviewConversationContractPublication(_ context.Context, id string, in sdk.ConversationContractPublicationRequest, a sdk.ConversationAuthority) (sdk.ConversationContractPublicationPreview, error) {
	s.publication, s.actor, s.id = in, a, id
	if s.denied {
		return sdk.ConversationContractPublicationPreview{}, &sdk.Error{Class: "forbidden", Code: "agent.conversation.source_access_denied"}
	}
	return sdk.ConversationContractPublicationPreview{Agreement: sdk.ConversationAgreementRevision{Revision: in.AgreementRevision}, Requirements: sdk.ConversationAgentRequirements{Sources: []sdk.ConversationRunReference{{ConversationID: "original", RunID: "original-run", BeforeStep: 3}}}, ExpectedRevision: 8, RecordDigest: "original-contract", Publisher: a, RecipientUserID: "executor"}, nil
}

func (s *remoteContractPublicationService) ConversationContractPublicationCandidates(_ context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationContractPublicationCandidates, error) {
	s.before, s.actor, s.id = before, a, id
	return sdk.ConversationContractPublicationCandidates{Items: []sdk.ConversationContractPublicationCandidate{{Revision: 7, BriefVersion: 2}}, Complete: false, NextBefore: 7, CurrentAgreementRevision: 9}, nil
}

func (s *remoteContractPublicationService) ConversationContractPublicationHistory(_ context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationContractPublicationHistory, error) {
	s.before, s.actor, s.id = before, a, id
	return sdk.ConversationContractPublicationHistory{Items: []sdk.ConversationContractPublicationReceipt{{ConversationContractPublication: sdk.ConversationContractPublication{AgreementRevision: 7, RecordDigest: "original-contract"}, Revision: 8, Publisher: a, RecipientUserID: "executor", Reason: "Share the original contract"}}, Complete: true}, nil
}

func TestSaaSContractPublicationPreservesOriginalSnapshotAndActualAuthority(t *testing.T) {
	source := &remoteContractPublicationService{}
	svc, err := server.New(server.Config{APIKey: "contract-runtime-secret", Conversations: source, ConversationRuntimeID: "runtime", ConversationWorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(svc.Handler())
	defer httpServer.Close()
	opened, err := NewFactory(Options{BaseURL: httpServer.URL, APIKey: "contract-runtime-secret", Client: httpServer.Client()}).OpenSaaS(t.Context(), sdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close(context.Background())
	reader, ok := opened.(sdk.ConversationBinding).Conversations().(sdk.ConversationContractPublicationReader)
	if !ok {
		t.Fatal("SaaS binding omitted contract publication preparation")
	}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "issuer", RoleKey: "actual-publisher"}
	in := sdk.ConversationContractPublicationRequest{AgreementRevision: 7}
	preview, err := reader.PreviewConversationContractPublication(t.Context(), "delegation", in, a)
	if err != nil || preview.Agreement.Revision != 7 || preview.Agreement.Requirements != nil || len(preview.Requirements.Sources) != 1 || preview.Requirements.Sources[0].BeforeStep != 3 || preview.Publisher != a || preview.RecipientUserID != "executor" || source.publication != in || source.actor != a || source.id != "delegation" {
		t.Fatal("preview lost legacy snapshot, recovered original roots, or actual caller", preview, err)
	}
	index, err := reader.ConversationContractPublicationCandidates(t.Context(), "delegation", 9, a)
	if err != nil || len(index.Items) != 1 || index.Items[0].Revision != 7 || index.NextBefore != 7 || index.Complete || source.before != 9 || source.actor != a {
		t.Fatal("contract version index forwarding", index, err)
	}
	history, err := reader.ConversationContractPublicationHistory(t.Context(), "delegation", 10, a)
	if err != nil || len(history.Items) != 1 || history.Items[0].Publisher != a || history.Items[0].Reason != "Share the original contract" || source.before != 10 || source.actor != a || source.id != "delegation" {
		t.Fatal("publication audit forwarding", history, err)
	}
	source.denied = true
	if out, err := reader.PreviewConversationContractPublication(t.Context(), "delegation", in, a); err == nil || out.RecordDigest != "" || len(out.Requirements.Sources) != 0 {
		t.Fatal("remote source denial returned private contract data", out, err)
	}
}
