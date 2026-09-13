package application

import (
	"context"
	"encoding/json"
	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"strings"
	"testing"
)

type privatePeerSources struct {
	persistence.ConversationRepository
	persistence.ConversationCollaborationRepository
	persistence.ConversationSourceRepository
}

func (privatePeerSources) ConversationDelegation(_ context.Context, id string, _ sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	return sdk.ConversationDelegation{ID: id}, nil
}

func (privatePeerSources) Get(_ context.Context, id string, _ sdk.ConversationAuthority) (sdk.Conversation, error) {
	return sdk.Conversation{ID: id, AgentID: "fixture-peer"}, nil
}

func (privatePeerSources) ConversationPeerInbox(context.Context, string, sdk.ConversationAuthority) ([]sdk.ConversationAgentMessage, error) {
	return []sdk.ConversationAgentMessage{{ID: "message", Content: "private contents", Source: &sdk.ConversationRunReference{ConversationID: "private-source", RunID: "source-run"}}}, nil
}
func (privatePeerSources) ConversationSourceSnapshot(context.Context, sdk.ConversationRunReference, sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	d := sdk.AttachmentConversationTools()[0]
	return persistence.ConversationSourceSnapshot{Run: sdk.ConversationRun{ID: "source-run", ConversationID: "private-source"}, Calls: []persistence.ConversationToolExecution{{Step: 0, Call: sdk.ConversationToolCall{ID: "private-read", Name: d.Key, Arguments: `{}`}, Definition: d, State: "completed", Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"text":"private contents"}`)}}}}, nil
}
func TestPeerMessagesAndDeliveriesDoNotSharePrivateAttachmentsImplicitly(t *testing.T) {
	s := &ConversationService{runtimeID: "runtime", repo: privatePeerSources{}, options: ConversationOptions{CollaborationAuthorizer: allowCollaborationTestPolicy{}, ContextBytes: 65536}}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	claim := persistence.ConversationClaim{Authority: a, Run: sdk.ConversationRun{ID: "receiver-run", ConversationID: "receiver"}}
	input, err := s.appendConversationPeerInbox(t.Context(), claim, sdk.ConversationStepRequest{})
	if err == nil || !strings.Contains(err.Error(), "attachment_conversation_mismatch") || len(input.Messages) != 0 || len(input.InboxMessageIDs) != 0 {
		t.Fatalf("private message reached recipient: %+v %v", input, err)
	}
	d := sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "delegation", ConversationID: "private-source"}, Task: &sdk.ConversationTaskDetail{ConversationTaskSummary: sdk.ConversationTaskSummary{ExecutionRunID: "source-run", Result: &sdk.ConversationTaskResult{Preview: "private contents"}}}}
	raw, _ := json.Marshal(d)
	var definition sdk.ConversationToolDefinition
	for _, tool := range sdk.ConversationCollaborationTools() {
		if tool.Key == "delegation_get" {
			definition = tool
		}
	}
	host := collaborationToolHost{service: s}
	err = host.AuthorizeConversationToolResult(t.Context(), sdk.ConversationToolRequest{Authority: a, ConversationID: "receiver", RunID: "receiver-run", Call: sdk.ConversationToolCall{ID: "get", Name: "delegation_get", Arguments: `{"id":"delegation"}`}, Definition: definition}, sdk.ConversationToolResult{Status: "completed", ResourceID: "delegation", Content: raw})
	if err == nil || !strings.Contains(err.Error(), "attachment_conversation_mismatch") {
		t.Fatalf("private delivery escaped into recipient: %v", err)
	}
}

type privateDependencySources struct{ privatePeerSources }

func (privateDependencySources) ConversationDelegation(context.Context, string, sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	return sdk.ConversationDelegation{ID: "upstream", BriefSource: &sdk.ConversationRunReference{ConversationID: "private-source", RunID: "source-run"}}, nil
}
func (p privateDependencySources) ConversationSourceSnapshot(ctx context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	if ref.ConversationID == "receiver" {
		return persistence.ConversationSourceSnapshot{Run: sdk.ConversationRun{ID: "receiver-run", ConversationID: "receiver", BackgroundTask: &sdk.ConversationTaskExecution{Dependencies: []sdk.ConversationTaskDependency{{Source: &sdk.ConversationRunReference{ConversationID: "private-source", RunID: "source-run"}}}}}}, nil
	}
	return p.privatePeerSources.ConversationSourceSnapshot(ctx, ref, a)
}
func TestPeerDependencySnapshotsRecheckSourceBeforeModelAndSharing(t *testing.T) {
	s := &ConversationService{runtimeID: "runtime", repo: privateDependencySources{}, options: ConversationOptions{CollaborationAuthorizer: allowCollaborationTestPolicy{}}}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	for _, err := range []error{
		s.validateDependencySources(t.Context(), []sdk.ConversationDependencyInput{{DelegationID: "upstream", BriefVersion: 1}}, "receiver", a),
		s.checkRunSources(t.Context(), sdk.ConversationRunReference{ConversationID: "receiver", RunID: "receiver-run"}, a),
	} {
		if err == nil || !strings.Contains(err.Error(), "attachment_conversation_mismatch") {
			t.Fatalf("private dependency reached new execution: %v", err)
		}
	}
	d := sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "delegation", Dependencies: []sdk.ConversationTaskDependency{{Values: json.RawMessage(`{"constraints":["private"]}`), Source: &sdk.ConversationRunReference{ConversationID: "private-source", RunID: "source-run"}}}}}
	raw, _ := json.Marshal(d)
	var definition sdk.ConversationToolDefinition
	for _, tool := range sdk.ConversationCollaborationTools() {
		if tool.Key == "delegation_get" {
			definition = tool
		}
	}
	err := (&collaborationToolHost{service: s}).AuthorizeConversationToolResult(t.Context(), sdk.ConversationToolRequest{Authority: a, ConversationID: "receiver", RunID: "receiver-run", Call: sdk.ConversationToolCall{ID: "get", Name: "delegation_get", Arguments: `{"id":"delegation"}`}, Definition: definition}, sdk.ConversationToolResult{Status: "completed", ResourceID: "delegation", Content: raw})
	if err == nil || !strings.Contains(err.Error(), "attachment_conversation_mismatch") {
		t.Fatalf("private dependency escaped via tool result: %v", err)
	}
}

type privateStructuredSources struct{ privateDependencySources }

func (privateStructuredSources) ConversationDelegation(context.Context, string, sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	return sdk.ConversationDelegation{ID: "upstream", InputSource: &sdk.ConversationRunReference{ConversationID: "private-source", RunID: "source-run"}}, nil
}
func (p privateStructuredSources) ConversationSourceSnapshot(ctx context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	if ref.ConversationID == "receiver" {
		return persistence.ConversationSourceSnapshot{Run: sdk.ConversationRun{ID: "receiver-run", ConversationID: "receiver", BackgroundTask: &sdk.ConversationTaskExecution{InputSource: &sdk.ConversationRunReference{ConversationID: "private-source", RunID: "source-run"}}}}, nil
	}
	return p.privateDependencySources.ConversationSourceSnapshot(ctx, ref, a)
}
func TestPeerStructuredInputSourceCannotBeSharedImplicitly(t *testing.T) {
	s := &ConversationService{runtimeID: "runtime", repo: privateStructuredSources{}, options: ConversationOptions{CollaborationAuthorizer: allowCollaborationTestPolicy{}}}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	for _, err := range []error{
		s.validateDependencySources(t.Context(), []sdk.ConversationDependencyInput{{DelegationID: "upstream", BriefVersion: 1, Fields: []string{"input"}}}, "receiver", a),
		s.checkRunSources(t.Context(), sdk.ConversationRunReference{ConversationID: "receiver", RunID: "receiver-run"}, a),
	} {
		if err == nil || !strings.Contains(err.Error(), "attachment_conversation_mismatch") {
			t.Fatalf("private structured input source escaped: %v", err)
		}
	}
	var definition sdk.ConversationToolDefinition
	for _, tool := range sdk.ConversationCollaborationTools() {
		if tool.Key == "delegation_get" {
			definition = tool
		}
	}
	source := &sdk.ConversationRunReference{ConversationID: "private-source", RunID: "source-run"}
	for _, d := range []sdk.ConversationDelegation{
		{ID: "delegation", StructuredInput: &sdk.ConversationStructuredInput{Data: json.RawMessage(`{"private":true}`)}, InputSource: source},
		{ID: "delegation", Dependencies: []sdk.ConversationTaskDependency{{Values: json.RawMessage(`{"input":{"data":{"private":true}}}`), InputSource: source}}},
	} {
		raw, _ := json.Marshal(sdk.ConversationDelegationDetail{ConversationDelegation: d})
		err := (&collaborationToolHost{service: s}).AuthorizeConversationToolResult(t.Context(), sdk.ConversationToolRequest{Authority: a, ConversationID: "receiver", RunID: "receiver-run", Call: sdk.ConversationToolCall{ID: "get", Name: "delegation_get", Arguments: `{"id":"delegation"}`}, Definition: definition}, sdk.ConversationToolResult{Status: "completed", ResourceID: "delegation", Content: raw})
		if err == nil || !strings.Contains(err.Error(), "attachment_conversation_mismatch") {
			t.Fatalf("private typed data escaped through tool result: %v", err)
		}
	}
}

type privateTransferSources struct {
	privatePeerSources
	handoff *sdk.ConversationDelegationHandoff
}

func (p privateTransferSources) ConversationSourceSnapshot(ctx context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	if ref.ConversationID == "receiver" {
		return persistence.ConversationSourceSnapshot{Run: sdk.ConversationRun{ID: "receiver-run", ConversationID: "receiver", BackgroundTask: &sdk.ConversationTaskExecution{Handoff: p.handoff}}}, nil
	}
	return p.privatePeerSources.ConversationSourceSnapshot(ctx, ref, a)
}

func TestPeerTransferRechecksOriginalEffectsAndDecisionSources(t *testing.T) {
	source := sdk.ConversationRunReference{ConversationID: "private-source", RunID: "source-run"}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	for _, handoff := range []*sdk.ConversationDelegationHandoff{{Runs: []sdk.ConversationRunReference{source}}, {Source: &source}} {
		s := &ConversationService{runtimeID: "runtime", repo: privateTransferSources{handoff: handoff}, options: ConversationOptions{CollaborationAuthorizer: allowCollaborationTestPolicy{}}}
		for _, err := range []error{
			s.checkHandoffSources(t.Context(), handoff, a, "receiver"),
			s.checkRunSources(t.Context(), sdk.ConversationRunReference{ConversationID: "receiver", RunID: "receiver-run"}, a),
		} {
			if err == nil || !strings.Contains(err.Error(), "attachment_conversation_mismatch") {
				t.Fatalf("transfer exposed private source to a new recipient: %v", err)
			}
		}
		var definition sdk.ConversationToolDefinition
		for _, tool := range sdk.ConversationCollaborationTools() {
			if tool.Key == "delegation_get" {
				definition = tool
			}
		}
		for _, detail := range []sdk.ConversationDelegationDetail{
			{ConversationDelegation: sdk.ConversationDelegation{ID: "delegation", Handoff: handoff}},
			{ConversationDelegation: sdk.ConversationDelegation{ID: "delegation"}, Assignments: []sdk.ConversationDelegationAssignment{{Source: &source}}},
			{ConversationDelegation: sdk.ConversationDelegation{ID: "delegation"}, Assignments: []sdk.ConversationDelegationAssignment{{PreviousDelivery: &sdk.ConversationDelegationDelivery{Evidence: []sdk.ConversationRunReference{source}}}}},
		} {
			raw, _ := json.Marshal(detail)
			err := (&collaborationToolHost{service: s}).AuthorizeConversationToolResult(t.Context(), sdk.ConversationToolRequest{Authority: a, ConversationID: "receiver", RunID: "receiver-run", Definition: definition, Call: sdk.ConversationToolCall{ID: "get", Name: "delegation_get", Arguments: `{"id":"delegation"}`}}, sdk.ConversationToolResult{Status: "completed", ResourceID: "delegation", Content: raw})
			if err == nil || !strings.Contains(err.Error(), "attachment_conversation_mismatch") {
				t.Fatalf("transfer history escaped through tool result: %v", err)
			}
		}
	}
}

type revokedTransferHistorySources struct{ privatePeerSources }

func (revokedTransferHistorySources) ConversationAgreementHistory(context.Context, string, int64, sdk.ConversationAuthority) (sdk.ConversationAgreementHistory, error) {
	return sdk.ConversationAgreementHistory{Items: []sdk.ConversationAgreementRevision{{Revision: 2, Reason: "A decision derived from revoked data", Source: &sdk.ConversationRunReference{ConversationID: "brief", RunID: "brief-run"}, ChangeSource: &sdk.ConversationRunReference{ConversationID: "revoked", RunID: "decision-run"}}}}, nil
}
func (revokedTransferHistorySources) ConversationSourceSnapshot(_ context.Context, ref sdk.ConversationRunReference, _ sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	if ref.ConversationID == "revoked" {
		return persistence.ConversationSourceSnapshot{}, conversationFailure("forbidden", "knowledge_access_denied")
	}
	return persistence.ConversationSourceSnapshot{Run: sdk.ConversationRun{ID: ref.RunID, ConversationID: ref.ConversationID}}, nil
}
func TestPeerTransferDecisionHistoryRechecksCurrentAccess(t *testing.T) {
	s := &ConversationService{runtimeID: "runtime", repo: revokedTransferHistorySources{}, options: ConversationOptions{CollaborationAuthorizer: allowCollaborationTestPolicy{}}}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	out, err := s.ConversationAgreementHistory(t.Context(), "delegation", 0, a)
	if err == nil || !strings.Contains(err.Error(), "knowledge_access_denied") || len(out.Items) != 0 {
		t.Fatalf("old brief permission exposed revoked decision: %+v %v", out, err)
	}
}

type verificationPeerSources struct {
	privatePeerSources
	persistence.ConversationExecutionReadRepository
	persistence.ConversationDeliveryVerificationRepository
	history sdk.ConversationDeliveryHistory
}

func (p verificationPeerSources) ConversationDeliveryHistory(context.Context, string, int64, sdk.ConversationAuthority) (sdk.ConversationDeliveryHistory, error) {
	return p.history, nil
}

func (verificationPeerSources) ConversationSourceSnapshot(_ context.Context, ref sdk.ConversationRunReference, _ sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	return persistence.ConversationSourceSnapshot{}, conversationFailure("forbidden", "knowledge_access_denied")
}

func TestPeerVerificationHistoryRechecksReviewAndEveryReceiptSource(t *testing.T) {
	ref := sdk.ConversationResultReference{ConversationID: "revoked", RunID: "original", CallID: "read", SHA256: strings.Repeat("a", 64)}
	source := &sdk.ConversationRunReference{ConversationID: "revoked", RunID: "review"}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	for _, record := range []sdk.ConversationDeliveryRecord{
		{Verification: sdk.ConversationDeliveryVerification{Source: source}},
		{Verification: sdk.ConversationDeliveryVerification{Checks: []sdk.ConversationCompletionCheck{{Condition: 0, Receipts: []sdk.ConversationResultReference{ref}}}}},
		{Delivery: sdk.ConversationDelegationDelivery{Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Receipts: []sdk.ConversationResultReference{ref}}}}},
	} {
		s := &ConversationService{runtimeID: "runtime", repo: verificationPeerSources{history: sdk.ConversationDeliveryHistory{Items: []sdk.ConversationDeliveryRecord{record}}}, options: ConversationOptions{CollaborationAuthorizer: allowCollaborationTestPolicy{}}}
		out, err := s.ConversationDeliveryHistory(t.Context(), "delegation", 0, a)
		if err == nil || !strings.Contains(err.Error(), "knowledge_access_denied") || len(out.Items) != 0 {
			t.Fatalf("revoked receipt/review disclosed %+v %v", out, err)
		}
	}
	s := &ConversationService{runtimeID: "runtime", repo: verificationPeerSources{}, options: ConversationOptions{CollaborationAuthorizer: allowCollaborationTestPolicy{}}}
	for _, report := range []*sdk.ConversationDeliveryVerification{{Source: source}, {Checks: []sdk.ConversationCompletionCheck{{Condition: 0, Receipts: []sdk.ConversationResultReference{ref}}}}} {
		if err := s.checkVerificationSources(t.Context(), report, a, "receiver"); err == nil {
			t.Fatal("recipient received revoked evidence")
		}
	}
}
