package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type contractPublicationTestRepository struct {
	*publicationTestRepository
	original             persistence.ConversationContractPublicationRecord
	audits               []sdk.ConversationContractPublicationReceipt
	publicationAuthority *sdk.ConversationAuthority
	publicationReads     int
}

func (r *contractPublicationTestRepository) ConversationSourceReleases(_ context.Context, ref sdk.ConversationRunReference, _ sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	r.publicationReads++
	original := r.snapshots[ref.RunID].Authority
	publisher := r.issuer
	if r.publicationAuthority != nil {
		publisher = *r.publicationAuthority
	}
	return []persistence.ConversationSourceRelease{{DelegationID: r.d.ID, Purpose: "contract", Reference: ref, Producer: original, Publisher: &publisher}}, nil
}

func TestExactCurrentRoleSourceReadSurvivesAnotherPublicationWithdrawal(t *testing.T) {
	s, r, _, a, _ := contractPublicationServiceFixture()
	root := r.original.Requirements.Sources[0]
	r.snapshots[root.RunID] = persistence.ConversationSourceSnapshot{Authority: a}
	publisher := a
	publisher.RoleKey = "revoked"
	r.publicationAuthority = &publisher
	ctx := context.WithValue(t.Context(), conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "contract", delegationID: r.d.ID, roots: []sdk.ConversationRunReference{root}})
	if _, err := s.sourceAudit(a).run(ctx, root); err != nil || r.publicationReads != 0 {
		t.Fatal("withdrawn foreign publication removed ordinary exact-role source access", err, r.publicationReads)
	}
	r.sourceErr = conversationFailure("forbidden", "current_data_denied")
	if _, err := s.sourceAudit(a).run(ctx, root); !collaborationDenied(err) {
		t.Fatal("ordinary source access skipped current data policy", err)
	}
}

func (r *contractPublicationTestRepository) ConversationContractPublicationRecord(_ context.Context, _ string, revision int64, _ sdk.ConversationAuthority) (persistence.ConversationContractPublicationRecord, error) {
	if revision != 0 && revision != r.original.Agreement.Revision {
		return persistence.ConversationContractPublicationRecord{}, conversationFailure("not_found", "agreement_not_found")
	}
	return r.original, nil
}
func (r *contractPublicationTestRepository) ConversationAgreementHistory(context.Context, string, int64, sdk.ConversationAuthority) (sdk.ConversationAgreementHistory, error) {
	return sdk.ConversationAgreementHistory{Items: []sdk.ConversationAgreementRevision{r.original.Agreement}, Complete: true}, nil
}
func (r *contractPublicationTestRepository) ConversationContractPublicationHistory(context.Context, string, int64, sdk.ConversationAuthority) (sdk.ConversationContractPublicationHistory, error) {
	return sdk.ConversationContractPublicationHistory{Items: r.audits, Complete: true}, nil
}
func (r *contractPublicationTestRepository) UpdateConversationDelegation(_ context.Context, _ string, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	if in.ExpectedRevision != r.d.Revision {
		return sdk.ConversationDelegation{}, conversationFailure("conflict", "revision_conflict")
	}
	r.mutations++
	r.d.Revision++
	r.audits = append(r.audits, sdk.ConversationContractPublicationReceipt{ConversationContractPublication: *in.ContractPublication, Revision: r.d.Revision, Publisher: a, RecipientUserID: r.executor.UserID, Reason: in.Reason})
	return r.d, nil
}

func contractPublicationServiceFixture() (*ConversationService, *contractPublicationTestRepository, *executionBindingTestPolicy, sdk.ConversationAuthority, sdk.ConversationAuthority) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "issuer", RoleKey: "current-publisher"}
	original, executor := a, a
	original.RoleKey = "old-professional"
	executor.UserID = "executor"
	root := sdk.ConversationRunReference{ConversationID: "original", RunID: "original", BeforeStep: 1}
	requirements := sdk.ConversationAgentRequirements{TaskType: "Protected task type", Sources: []sdk.ConversationRunReference{root}}
	record := persistence.ConversationContractPublicationRecord{Agreement: sdk.ConversationAgreementRevision{Revision: 1, Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "Protected original goal", Deliverable: "Protected original output"}, Requirements: &requirements}, Requirements: requirements}
	r := &contractPublicationTestRepository{publicationTestRepository: &publicationTestRepository{sameUserRoleSourceGraph: sameUserRoleSourceGraph{snapshots: map[string]persistence.ConversationSourceSnapshot{root.RunID: {Authority: original}}, reads: map[string][]sdk.ConversationAuthority{}}, issuer: a, executor: executor, d: sdk.ConversationDelegation{ID: "delegation", OwnerUserID: a.UserID, ExecutionSubject: executionSubject(executor), Status: "accepted_delivery", Revision: 7, AgreementRevision: 2, Brief: record.Agreement.Brief}}, original: record, audits: []sdk.ConversationContractPublicationReceipt{}}
	policy := &executionBindingTestPolicy{deniedRole: "revoked"}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: r, options: ConversationOptions{CollaborationAuthorizer: policy}}
	return s, r, policy, a, original
}

func TestContractPublicationIndexPreviewAndCommitUseOriginalAndRecheckCurrentPermission(t *testing.T) {
	s, r, policy, a, original := contractPublicationServiceFixture()
	without := *s
	without.repo = contractPublicationWithoutProvenance{ConversationRepository: r, ConversationCollaborationRepository: r, ConversationDelegationExecutionRepository: r, ConversationContractPublicationRepository: r}
	if out, err := without.PreviewConversationContractPublication(t.Context(), r.d.ID, sdk.ConversationContractPublicationRequest{AgreementRevision: 1}, a); err == nil || out.Agreement.Brief.Goal != "" {
		t.Fatal("missing original source ports exposed unverified content", out, err)
	}
	index, err := s.ConversationContractPublicationCandidates(t.Context(), r.d.ID, 0, a)
	raw, _ := json.Marshal(index)
	if err != nil || len(index.Items) != 1 || strings.Contains(string(raw), "Protected") || strings.Contains(string(raw), "original") {
		t.Fatal("version index exposed protected original", index, err)
	}
	preview, err := s.PreviewConversationContractPublication(t.Context(), r.d.ID, sdk.ConversationContractPublicationRequest{AgreementRevision: 1}, a)
	if err != nil || preview.RecordDigest != conversationDigest(r.original) || preview.Requirements.TaskType != "Protected task type" || preview.Publisher != a || preview.RecipientUserID != r.executor.UserID || r.mutations != 0 {
		t.Fatal("preview substituted current requirements or published sources", preview, err)
	}
	in := sdk.ConversationDelegationUpdate{ClientID: "explicit-contract", Action: "republish_contract", ExpectedRevision: preview.ExpectedRevision, Reason: "Share this original", ContractPublication: &sdk.ConversationContractPublication{AgreementRevision: 1, RecordDigest: preview.RecordDigest}}
	r.sourceErr = conversationFailure("forbidden", "current_field_denied")
	if _, err = s.UpdateConversationDelegation(t.Context(), r.d.ID, in, a); !collaborationDenied(err) || r.mutations != 0 {
		t.Fatal("commit ignored current data withdrawal", err)
	}
	if out, e := s.PreviewConversationContractPublication(t.Context(), r.d.ID, sdk.ConversationContractPublicationRequest{AgreementRevision: 1}, a); !collaborationDenied(e) || out.Agreement.Brief.Goal != "" {
		t.Fatal("denied preview leaked original", out, e)
	}
	r.sourceErr = nil
	policy.deniedRole = original.RoleKey
	if _, err = s.UpdateConversationDelegation(t.Context(), r.d.ID, in, a); !collaborationDenied(err) || r.mutations != 0 {
		t.Fatal("original role withdrawal still published", err)
	}
	policy.deniedRole = "revoked"
	if _, err = s.UpdateConversationDelegation(t.Context(), r.d.ID, in, r.executor); !collaborationDenied(err) || r.mutations != 0 {
		t.Fatal("receiver republished issuer sources", err)
	}
	bad := in
	selection := *in.ContractPublication
	selection.RecordDigest = strings.Repeat("0", 64)
	bad.ContractPublication = &selection
	if _, err = s.UpdateConversationDelegation(t.Context(), r.d.ID, bad, a); err == nil || r.mutations != 0 {
		t.Fatal("forged original digest committed")
	}
	ctx := context.WithValue(t.Context(), conversationPeerRequestKey{}, conversationPeerRequest{ConversationID: "model"})
	if _, err = s.UpdateConversationDelegation(ctx, r.d.ID, in, a); err == nil || r.mutations != 0 {
		t.Fatal("model initiated explicit manual publication")
	}
	out, err := s.UpdateConversationDelegation(t.Context(), r.d.ID, in, a)
	if err != nil || out.ContractPublication == nil || out.ContractPublication.Publisher != a || out.Task != nil || out.Delivery != nil || len(out.Messages) > 0 || out.Brief.Goal != "" || out.Status != "accepted_delivery" || r.mutations != 1 {
		t.Fatal("publication leaked content or lost narrow receipt", out, err)
	}
	if _, err = s.ConversationContractPublicationHistory(t.Context(), r.d.ID, 0, r.executor); err != nil {
		t.Fatal("authorized receiver cannot view actual publication audit", err)
	}
}

type contractPublicationWithoutProvenance struct {
	persistence.ConversationRepository
	persistence.ConversationCollaborationRepository
	persistence.ConversationDelegationExecutionRepository
	persistence.ConversationContractPublicationRepository
}

func TestContractSourceDenialKeepsOnlyAuthorizedRecoveryMetadata(t *testing.T) {
	s, r, policy, a, _ := contractPublicationServiceFixture()
	r.d.AgreementRevision = 1
	root := r.original.Requirements.Sources[0]
	r.d.BriefSource = &root
	r.d.Requirements = r.original.Requirements
	r.sourceErr = conversationFailure("forbidden", "current_data_denied")
	out, err := s.projectConversationDelegation(t.Context(), r.d, a)
	if err != nil || !out.ContractOmitted || out.ID != r.d.ID || out.Revision != r.d.Revision || out.Access == nil || !out.Access.View || out.Brief.Goal != "" || out.Task != nil || out.Delivery != nil || len(out.Messages) > 0 || len(out.Requirements.Sources) > 0 || out.BriefSource != nil {
		t.Fatal("recovery metadata leaked denied original", out, err)
	}
	r.sourceErr = conversationFailure("unavailable", "source_transport_unavailable")
	if out, err = s.projectConversationDelegation(t.Context(), r.d, a); err == nil || out.ContractOmitted {
		t.Fatal("transient failure became successful recovery projection", out, err)
	}
	policy.deniedRole = a.RoleKey
	if out, err = s.projectConversationDelegation(t.Context(), r.d, a); !collaborationDenied(err) || out.ID != "" {
		t.Fatal("recovery bypassed viewer withdrawal", out, err)
	}
}
