package application

import (
	"context"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type privateDisagreementSources struct {
	privatePeerSources
	persistence.ConversationDisagreementRepository
}

func TestPeerDisagreementContextKeepsOverflowIssuesDiscoverableWithoutLosingProvenance(t *testing.T) {
	in := sdk.ConversationStepRequest{}
	for i := range 64 {
		in.ContextSources = append(in.ContextSources, sdk.ConversationRunReference{ConversationID: "source", RunID: fmt.Sprintf("run-%d", i)})
	}
	d := sdk.ConversationDelegation{Brief: sdk.ConversationTaskBrief{Version: 1}, AgreementRevision: 1, Disagreements: []sdk.ConversationDisagreementSummary{{ID: "deferred-issue", Title: "A conclusion with another source", Sources: []sdk.ConversationRunReference{{ConversationID: "other", RunID: "other-run"}}}, {ID: "already-authorized", Title: "Known source", Sources: in.ContextSources[:1]}}}
	out, err := appendDisagreementContext(in, d)
	if err != nil || len(out.ContextSources) != 64 || len(out.Messages) != 1 || !strings.Contains(out.Messages[0].Content, `"read_on_demand":["deferred-issue"]`) || strings.Contains(out.Messages[0].Content, "A conclusion with another source") || !strings.Contains(out.Messages[0].Content, "Known source") {
		t.Fatalf("bounded context lost pending work or provenance: %+v %v", out, err)
	}
	d.Disagreements = []sdk.ConversationDisagreementSummary{{ID: "large-issue", NextAction: strings.Repeat("large context", 1200)}}
	out, err = appendDisagreementContext(sdk.ConversationStepRequest{}, d)
	if err != nil || len(out.Messages[0].Content) > 16384 || !strings.Contains(out.Messages[0].Content, `"read_on_demand":["large-issue"]`) {
		t.Fatal("oversized issue blocks model context", err)
	}
}

func TestPeerDisagreementResolutionClearsOnlyCurrentBlockerAndPreservesReviewHistory(t *testing.T) {
	d := sdk.ConversationDelegation{Brief: sdk.ConversationTaskBrief{Version: 1, CompletionConditions: []string{"Verify period"}}, AgreementRevision: 1, Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Verified"}}
	digest := conversationDigest(d.Delivery)
	original := sdk.ConversationDeliveryVerification{DeliveryDigest: digest, BriefVersion: 1, AgreementRevision: 1, Blockers: []string{"disagreements_pending"}, Checks: []sdk.ConversationCompletionCheck{{Condition: 0, Method: "agent", Verdict: "met", Basis: "Compared original period"}}}
	d.Verification = &original
	d.Disagreements = []sdk.ConversationDisagreementSummary{{Status: "resolved", BriefVersion: 1, AgreementRevision: 1, DeliveryDigest: digest}}
	projectDisagreementVerification(&d)
	if !d.Verification.Ready || len(d.Verification.Blockers) != 0 || original.Ready || len(original.Blockers) != 1 {
		t.Fatal("resolution changed original review or retained obsolete blocker")
	}
	d.Verification = &original
	d.Verification.Checks = append([]sdk.ConversationCompletionCheck{}, original.Checks...)
	d.Verification.Checks[0].Verdict = "unknown"
	projectDisagreementVerification(&d)
	if d.Verification.Ready {
		t.Fatal("resolving a dispute satisfied an unverified condition")
	}
	d.Disagreements[0].Status = "open"
	projectDisagreementVerification(&d)
	projectDisagreementVerification(&d)
	if d.Verification.Ready || len(d.Verification.Blockers) != 1 {
		t.Fatal("current issue blocker was duplicated or lost")
	}
}

func (privateDisagreementSources) ConversationDelegation(context.Context, string, sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	return sdk.ConversationDelegation{Brief: sdk.ConversationTaskBrief{Version: 1}, AgreementRevision: 1, Disagreements: []sdk.ConversationDisagreementSummary{{ID: "issue", Title: "private-derived-conclusion", Status: "open", Sources: []sdk.ConversationRunReference{{ConversationID: "private-source", RunID: "source-run"}}}}}, nil
}
func (p privateDisagreementSources) ConversationSourceSnapshot(ctx context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	if ref.ConversationID == "receiver" {
		return persistence.ConversationSourceSnapshot{Run: sdk.ConversationRun{ID: ref.RunID, ConversationID: ref.ConversationID}, StepSources: []persistence.ConversationStepSources{{Step: 1, Sources: []sdk.ConversationRunReference{{ConversationID: "private-source", RunID: "source-run"}}}}}, nil
	}
	return p.privatePeerSources.ConversationSourceSnapshot(ctx, ref, a)
}
func (p privateDisagreementSources) ConversationDisagreementHistory(ctx context.Context, id, _ string, _ int64, a sdk.ConversationAuthority) (sdk.ConversationDisagreementHistory, error) {
	d, _ := p.ConversationDelegation(ctx, id, a)
	return sdk.ConversationDisagreementHistory{Items: []sdk.ConversationDisagreement{{ConversationDisagreementSummary: d.Disagreements[0]}}}, nil
}

func TestPeerDisagreementContextAndHistoryRetainSourceBoundaries(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	s := &ConversationService{repo: privateDisagreementSources{}, runtimeID: "runtime", options: ConversationOptions{CollaborationAuthorizer: allowCollaborationTestPolicy{}}}
	claim := persistence.ConversationClaim{Authority: a, Run: sdk.ConversationRun{ID: "receiver-run", ConversationID: "receiver", BackgroundTask: &sdk.ConversationTaskExecution{DelegationID: "d"}}}
	input, err := s.appendConversationDisagreements(t.Context(), claim, sdk.ConversationStepRequest{})
	if err != nil || len(input.Messages) != 1 || strings.Contains(input.Messages[0].Content, "private-derived-conclusion") || !strings.Contains(input.Messages[0].Content, `"omitted":true`) {
		t.Fatalf("private issue reached model %+v %v", input, err)
	}
	if err = s.checkRunSources(t.Context(), sdk.ConversationRunReference{ConversationID: "receiver", RunID: "receiver-run", BeforeStep: 1}, a); err != nil {
		t.Fatal("later context tainted earlier boundary", err)
	}
	if err = s.checkRunSources(t.Context(), sdk.ConversationRunReference{ConversationID: "receiver", RunID: "receiver-run", BeforeStep: 2}, a); err == nil || !strings.Contains(err.Error(), "attachment_conversation_mismatch") {
		t.Fatal("consumed context lost source access", err)
	}
	ctx := context.WithValue(t.Context(), conversationPeerRequestKey{}, conversationPeerRequest{ConversationID: "receiver"})
	if page, err := s.ConversationDisagreementHistory(ctx, "d", "issue", 0, a); err == nil || len(page.Items) != 0 {
		t.Fatal("private issue history reached recipient", err)
	}
}
