package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type publicationTestRepository struct {
	sameUserRoleSourceGraph
	persistence.ConversationCollaborationRepository
	persistence.ConversationDelegationExecutionRepository
	persistence.ConversationDeliveryVerificationRepository
	d                sdk.ConversationDelegation
	issuer, executor sdk.ConversationAuthority
	records          map[int64]sdk.ConversationDeliveryRecord
	sourceErr        error
	mutations        int
}

type publicationWithoutProvenance struct {
	persistence.ConversationRepository
	persistence.ConversationCollaborationRepository
	persistence.ConversationDelegationExecutionRepository
	persistence.ConversationDeliveryPublicationRepository
	persistence.ConversationDeliveryVerificationRepository
}

func (r *publicationTestRepository) ConversationDelegation(context.Context, string, sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	return r.d, nil
}
func (r *publicationTestRepository) ConversationDelegationAuthorities(context.Context, string, sdk.ConversationAuthority) (persistence.ConversationDelegationAuthorities, error) {
	return persistence.ConversationDelegationAuthorities{Issuer: r.issuer, Executor: r.executor}, nil
}
func (r *publicationTestRepository) ConversationSourceSnapshot(ctx context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	if r.sourceErr != nil {
		return persistence.ConversationSourceSnapshot{}, r.sourceErr
	}
	return r.sameUserRoleSourceGraph.ConversationSourceSnapshot(ctx, ref, a)
}
func (r *publicationTestRepository) ConversationDeliveryPublicationRecord(_ context.Context, _ string, revision int64, _ sdk.ConversationAuthority) (sdk.ConversationDeliveryRecord, error) {
	if record, ok := r.records[revision]; ok {
		return record, nil
	}
	return sdk.ConversationDeliveryRecord{}, conversationFailure("not_found", "delivery_record_not_found")
}
func (r *publicationTestRepository) ConversationDeliveryHistory(context.Context, string, int64, sdk.ConversationAuthority) (sdk.ConversationDeliveryHistory, error) {
	return sdk.ConversationDeliveryHistory{Items: []sdk.ConversationDeliveryRecord{r.records[7]}, Complete: true}, nil
}
func (r *publicationTestRepository) UpdateConversationDelegation(_ context.Context, _ string, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	if in.ExpectedRevision != r.d.Revision {
		return sdk.ConversationDelegation{}, conversationFailure("conflict", "revision_conflict")
	}
	r.mutations++
	r.d.Revision++
	entry := r.records[in.Publication.DeliveryRevision]
	entry.Revision = r.d.Revision
	entry.Publication = &sdk.ConversationDeliveryPublicationReceipt{ConversationDeliveryPublication: *in.Publication, Publisher: a, RecipientUserID: r.issuer.UserID}
	r.records[r.d.Revision] = entry
	return r.d, nil
}

func TestDeliveryPublicationPreviewAndCommitRecheckCanonicalHistoryAndCurrentSources(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "executor", RoleKey: "current-publisher"}
	original, issuer := a, a
	original.RoleKey = "old-professional"
	issuer.UserID = "issuer"
	root := sdk.ConversationRunReference{ConversationID: "original", RunID: "original", BeforeStep: 1}
	record := sdk.ConversationDeliveryRecord{Revision: 7, Kind: "accept_delivery", Reason: "Protected original basis", Delivery: sdk.ConversationDelegationDelivery{Summary: "Protected original findings", BriefVersion: 1, Evidence: []sdk.ConversationRunReference{root}}, Verification: sdk.ConversationDeliveryVerification{ActorID: issuer.UserID, Source: &sdk.ConversationRunReference{ConversationID: "private-issuer", RunID: "private-assessment"}}}
	r := &publicationTestRepository{sameUserRoleSourceGraph: sameUserRoleSourceGraph{snapshots: map[string]persistence.ConversationSourceSnapshot{root.RunID: {Authority: original}}, reads: map[string][]sdk.ConversationAuthority{}}, issuer: issuer, executor: a, records: map[int64]sdk.ConversationDeliveryRecord{7: record}, d: sdk.ConversationDelegation{ID: "delegation", OwnerUserID: issuer.UserID, ExecutionSubject: executionSubject(a), Status: "accepted_delivery", Revision: 9}}
	policy := &executionBindingTestPolicy{deniedRole: "revoked"}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: r, options: ConversationOptions{CollaborationAuthorizer: policy}}
	without := *s
	without.repo = publicationWithoutProvenance{ConversationRepository: r, ConversationCollaborationRepository: r, ConversationDelegationExecutionRepository: r, ConversationDeliveryPublicationRepository: r, ConversationDeliveryVerificationRepository: r}
	if out, err := without.PreviewConversationDeliveryPublication(t.Context(), r.d.ID, sdk.ConversationDeliveryPublicationRequest{DeliveryRevision: 7}, a); err == nil || out.Record.Delivery.Summary != "" {
		t.Fatal("missing private provenance ports exposed an unverified original", out, err)
	}
	index, err := s.ConversationDeliveryPublicationCandidates(t.Context(), r.d.ID, 0, a)
	encoded, _ := json.Marshal(index)
	if err != nil || len(index.Items) != 1 || index.CurrentAvailable || strings.Contains(string(encoded), "Protected") || strings.Contains(string(encoded), "private-assessment") {
		t.Fatal("index exposed protected record contents", index, err)
	}
	preview, err := s.PreviewConversationDeliveryPublication(t.Context(), r.d.ID, sdk.ConversationDeliveryPublicationRequest{DeliveryRevision: 7}, a)
	if err != nil || preview.Record.Delivery.Summary != record.Delivery.Summary || preview.RecordDigest != conversationDigest(record) || preview.Record.Verification.Source != nil || preview.Publisher != a || preview.RecipientUserID != issuer.UserID || r.mutations != 0 {
		t.Fatal("preview changed or published the original", preview, err)
	}
	in := sdk.ConversationDelegationUpdate{ClientID: "share", ExpectedRevision: preview.ExpectedRevision, Action: "republish_delivery", Reason: "Explicitly share original", Publication: &sdk.ConversationDeliveryPublication{DeliveryRevision: 7, RecordDigest: preview.RecordDigest}}
	r.sourceErr = conversationFailure("forbidden", "current_data_access_denied")
	if out, err := s.UpdateConversationDelegation(t.Context(), r.d.ID, in, a); !collaborationDenied(err) || out.Publication != nil || r.mutations != 0 {
		t.Fatal("commit ignored source revocation after preview", out, err)
	}
	if out, err := s.PreviewConversationDeliveryPublication(t.Context(), r.d.ID, sdk.ConversationDeliveryPublicationRequest{DeliveryRevision: 7}, a); !collaborationDenied(err) || out.Record.Delivery.Summary != "" {
		t.Fatal("denied preview leaked original text", out, err)
	}
	r.sourceErr = nil
	policy.deniedRole = original.RoleKey
	if _, err := s.UpdateConversationDelegation(t.Context(), r.d.ID, in, a); !collaborationDenied(err) || r.mutations != 0 {
		t.Fatal("revoked original professional role still shared", err)
	}
	policy.deniedRole = "revoked"
	bad := in
	copy := *in.Publication
	copy.RecordDigest = strings.Repeat("0", 64)
	bad.Publication = &copy
	if _, err := s.UpdateConversationDelegation(t.Context(), r.d.ID, bad, a); err == nil || r.mutations != 0 {
		t.Fatal("forged canonical digest committed", err)
	}
	if _, err := s.UpdateConversationDelegation(t.Context(), r.d.ID, in, issuer); !collaborationDenied(err) || r.mutations != 0 {
		t.Fatal("issuer republished receiver's private roots", err)
	}
	out, err := s.UpdateConversationDelegation(t.Context(), r.d.ID, in, a)
	if err != nil || out.Publication == nil || out.Publication.Publisher != a || out.Delivery != nil || out.Task != nil || len(out.Messages) > 0 || out.Status != "accepted_delivery" || r.mutations != 1 {
		t.Fatal("publication did not return a narrow receipt", out, err)
	}
	if conversationDigest(r.records[7]) != conversationDigest(record) {
		t.Fatal("original accepted record changed")
	}
}
