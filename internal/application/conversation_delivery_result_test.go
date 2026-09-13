package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	tools "github.com/domainry/domainry-tools-sdk"
)

type releasedResultRepository struct {
	privatePeerSources
	persistence.ConversationResultRepository
	persistence.ConversationDeliveryVerificationRepository
	current sdk.ConversationDelegation
	history sdk.ConversationDeliveryHistory
	record  persistence.ConversationToolExecution
	reads   int
}

type scopedReleasedResultRepository struct {
	*releasedResultRepository
	original sdk.ConversationAuthority
	releases []persistence.ConversationSourceRelease
}

func (r *scopedReleasedResultRepository) ConversationSourceAuthority(context.Context, sdk.ConversationRunReference, sdk.ConversationAuthority) (sdk.ConversationAuthority, error) {
	return r.original, nil
}

func (r *scopedReleasedResultRepository) ConversationSourceReleases(context.Context, sdk.ConversationRunReference, sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	return r.releases, nil
}

func (r *scopedReleasedResultRepository) ConversationSourceSnapshot(context.Context, sdk.ConversationRunReference, sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	return persistence.ConversationSourceSnapshot{Authority: r.original}, nil
}

func TestDeliveryResultEndpointRequiresExactDeliveryPublication(t *testing.T) {
	reader := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", RoleKey: "reader"}
	original, publisher := reader, reader
	original.RoleKey, publisher.RoleKey = "old-proof", "actual-publisher"
	definition := sdk.ConversationToolDefinition{Key: "specialist_read", Version: "1", TimeoutMillis: 1000}
	result := sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"total":70}`)}
	ref := sdk.ConversationResultReference{ConversationID: "producer", RunID: "run", Step: 2, CallID: "original", SHA256: conversationDigest(result)}
	root := sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2}
	delivery := sdk.ConversationDelegationDelivery{Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Receipts: []sdk.ConversationResultReference{ref}}}}
	base := &releasedResultRepository{current: sdk.ConversationDelegation{ID: "released", OwnerUserID: reader.UserID, Delivery: &delivery}, record: persistence.ConversationToolExecution{State: "completed", Step: ref.Step, Definition: definition, Call: sdk.ConversationToolCall{ID: ref.CallID, Name: definition.Key}, Result: &result}}
	repo := &scopedReleasedResultRepository{releasedResultRepository: base, original: original}
	host := &deliveryReadTestHost{}
	s := &ConversationService{runtimeID: reader.RuntimeID, repo: repo, options: ConversationOptions{ContextBytes: 32768, ToolHost: host, ToolDefinitions: []sdk.ConversationToolDefinition{definition}, CollaborationAuthorizer: &executionBindingTestPolicy{deniedRole: "revoked"}}}
	in := sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: ref, MaxBytes: 8192}}
	for _, purpose := range []string{"contract", "message", "other-delegation", "unknown-publisher"} {
		release := persistence.ConversationSourceRelease{DelegationID: base.current.ID, Purpose: purpose, Reference: root, Producer: original, Publisher: &publisher}
		if purpose == "other-delegation" {
			release.Purpose, release.DelegationID = "delivery", "another"
		} else if purpose == "unknown-publisher" {
			release.Purpose, release.Publisher = "delivery", nil
		}
		repo.releases = []persistence.ConversationSourceRelease{release}
		before := repo.reads
		if page, err := s.ReadConversationDeliveryResult(t.Context(), base.current.ID, in, reader); !collaborationDenied(err) || page.JSONText != "" || repo.reads != before {
			t.Fatal("unrelated/unverified grant reached result storage", purpose, page, err)
		}
	}
	repo.releases = []persistence.ConversationSourceRelease{{DelegationID: base.current.ID, Purpose: "delivery", Reference: root, Producer: original, Publisher: &publisher}}
	if page, err := s.ReadConversationDeliveryResult(t.Context(), base.current.ID, in, reader); err != nil || !page.Complete || page.JSONText == "" || host.executionChecks != 0 {
		t.Fatal("exact delivery publication lost independent source reading", page, err)
	}
}

func (r *releasedResultRepository) ConversationDelegation(context.Context, string, sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	return r.current, nil
}
func (r *releasedResultRepository) ConversationDeliveryHistory(context.Context, string, int64, sdk.ConversationAuthority) (sdk.ConversationDeliveryHistory, error) {
	return r.history, nil
}
func (r *releasedResultRepository) ConversationResult(context.Context, sdk.ConversationResultReference, sdk.ConversationAuthority) (persistence.ConversationToolExecution, error) {
	r.reads++
	return r.record, nil
}
func (r *releasedResultRepository) ConversationSourceSnapshot(context.Context, sdk.ConversationRunReference, sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	// Intentionally omit calls: the reader must independently validate the
	// fetched exact result even if this snapshot doesn't contain its record.
	return persistence.ConversationSourceSnapshot{}, nil
}

func TestReleasedResultRestrictsMembershipAndRechecksEveryPage(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	definition := sdk.ConversationToolDefinition{Key: "specialist_read", Version: "1", TimeoutMillis: 1000}
	raw, _ := json.Marshal(map[string]string{"text": strings.Repeat("原数据", 2000)})
	result := sdk.ConversationToolResult{Status: "completed", Content: raw}
	ref := sdk.ConversationResultReference{ConversationID: "producer", RunID: "run", Step: 2, CallID: "original", SHA256: conversationDigest(result)}
	delivery := sdk.ConversationDelegationDelivery{Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Receipts: []sdk.ConversationResultReference{ref}}}}
	repo := &releasedResultRepository{current: sdk.ConversationDelegation{ID: "released", Delivery: &delivery}, history: sdk.ConversationDeliveryHistory{Items: []sdk.ConversationDeliveryRecord{{Revision: 3, Delivery: delivery}}}, record: persistence.ConversationToolExecution{State: "completed", Step: ref.Step, Definition: definition, Call: sdk.ConversationToolCall{ID: ref.CallID, Name: definition.Key}, Result: &result}}
	host := &deliveryReadTestHost{}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{ContextBytes: 32768, ToolHost: host, ToolDefinitions: []sdk.ConversationToolDefinition{definition}, CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read"}}}
	in := sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: ref, MaxBytes: 256}}
	first, err := s.ReadConversationDeliveryResult(t.Context(), "released", in, a)
	if err != nil || first.Complete || first.NextOffset < 1 || host.reads != 1 || host.executionChecks != 0 {
		t.Fatalf("released read used execution or skipped source policy: %+v %v", first, err)
	}
	in.Offset = first.NextOffset
	host.err = &tools.Error{Class: "forbidden", Code: "source.revoked"}
	if page, err := s.ReadConversationDeliveryResult(t.Context(), "released", in, a); err != host.err || page.JSONText != "" {
		t.Fatal("source revoked between pages still disclosed bytes", err)
	}
	host.err = nil
	in.DeliveryRevision = 3
	if _, err := s.ReadConversationDeliveryResult(t.Context(), "released", in, a); err != nil {
		t.Fatal("exact immutable delivery revision unreadable", err)
	}
	for _, revision := range []int64{-1, 2, 4} {
		in.DeliveryRevision = revision
		before := repo.reads
		if page, err := s.ReadConversationDeliveryResult(t.Context(), "released", in, a); err == nil || page.JSONText != "" || repo.reads != before {
			t.Fatal("invalid or nonexistent delivery revision reached result store", revision, err)
		}
	}
	in.DeliveryRevision = 0
	for _, change := range []func(*sdk.ConversationResultReference){
		func(r *sdk.ConversationResultReference) { r.CallID = "unreleased" },
		func(r *sdk.ConversationResultReference) { r.Step++ },
		func(r *sdk.ConversationResultReference) { r.SHA256 = strings.Repeat("0", 64) },
		func(r *sdk.ConversationResultReference) { r.ConversationID = "other" },
	} {
		in.Reference = ref
		change(&in.Reference)
		before := repo.reads
		if _, err := s.ReadConversationDeliveryResult(t.Context(), "released", in, a); err == nil || repo.reads != before {
			t.Fatal("unreleased reference reached result store", err)
		}
	}
	in.Reference = ref
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view"}
	before := repo.reads
	if _, err := s.ReadConversationDeliveryResult(t.Context(), "released", in, a); !collaborationDenied(err) || repo.reads != before {
		t.Fatal("revoked collaboration read reached result store", err)
	}
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "delivery_read"}
	result.Content = json.RawMessage(`{"modified":true}`)
	if page, err := s.ReadConversationDeliveryResult(t.Context(), "released", in, a); err == nil || page.JSONText != "" {
		t.Fatal("modified result disclosed")
	}
	result.Content = raw
	for _, state := range []string{"failed", "unknown"} {
		result.Status = state
		in.Reference.SHA256 = conversationDigest(result)
		delivery.Conditions[0].Receipts[0] = in.Reference
		if page, err := s.ReadConversationDeliveryResult(t.Context(), "released", in, a); err == nil || page.JSONText != "" {
			t.Fatal("unverified failure payload bypassed source read policy", state)
		}
	}
	result.Status = "completed"
	in.Reference = ref
	delivery.Conditions[0].Receipts[0] = ref
	host.err = &tools.Error{Class: "unavailable", Code: tools.ResultReadUnsupportedCode}
	if _, err := s.ReadConversationDeliveryResult(t.Context(), "released", in, a); err == nil {
		t.Fatal("unsupported independent read implicitly granted access")
	}
}
