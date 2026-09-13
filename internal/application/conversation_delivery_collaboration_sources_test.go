package application

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestDeliveryCollaborationProvenanceDoesNotReleaseRawWorkingContext(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
	definition, _ := collaborationTool("delegation_get")
	d := sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "released"}, Task: &sdk.ConversationTaskDetail{}, Messages: []sdk.ConversationAgentMessage{{ID: "message", Content: "private working context"}}}
	raw, _ := json.Marshal(d)
	result := sdk.ConversationToolResult{Status: "completed", ResourceID: d.ID, Content: raw}
	record := persistence.ConversationToolExecution{State: "completed", Step: 1, Definition: definition, Call: sdk.ConversationToolCall{ID: "agreement", Name: definition.Key, Arguments: `{"id":"released"}`}, Result: &result}
	host := &deliveryReadTestHost{}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: privatePeerSources{}, options: ConversationOptions{ToolHost: host, CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read"}}}
	ctx := context.WithValue(t.Context(), conversationAgentContextKey{}, &sdk.ConversationAgentSnapshot{})
	owner := sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}
	if _, err := s.sourceAudit(a).record(deliverySourceContext(ctx, d.ID), owner, record); err != nil || host.executionChecks != 0 {
		t.Fatal("provenance required collaboration tool execution", err)
	}
	if _, err := s.sourceAudit(a).record(ctx, owner, record); err == nil {
		t.Fatal("raw context acquired provenance exception")
	}
	// Even explicit inclusion in a delivery does not release raw task/messages
	// under the permission intended only to read its business result.
	ref := sdk.ConversationResultReference{ConversationID: owner.ConversationID, RunID: owner.RunID, Step: record.Step, CallID: record.Call.ID, SHA256: conversationDigest(result)}
	repo := &releasedResultRepository{current: sdk.ConversationDelegation{ID: d.ID, Delivery: &sdk.ConversationDelegationDelivery{Conditions: []sdk.ConversationConditionAssessment{{Receipts: []sdk.ConversationResultReference{ref}}}}}, record: record}
	s.repo = repo
	in := sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: ref, MaxBytes: 4096}}
	if page, err := s.ReadConversationDeliveryResult(t.Context(), d.ID, in, a); err == nil || page.JSONText != "" {
		t.Fatal("raw released collaboration record disclosed private working context", err)
	}
	// Same-delegation provenance continues to traverse and reject private file
	// sources. Its exception cannot be used for another delegation either.
	s.repo = privatePeerSources{}
	d.BriefSource = &sdk.ConversationRunReference{ConversationID: "private-source", RunID: "source-run"}
	result.Content, _ = json.Marshal(d)
	if _, err := s.sourceAudit(a).record(deliverySourceContext(ctx, d.ID), owner, record); err == nil {
		t.Fatal("private provenance escaped source traversal")
	}
	result.Content = raw
	if _, err := s.sourceAudit(a).record(deliverySourceContext(ctx, "another"), owner, record); err == nil {
		t.Fatal("another delegation inherited read scope")
	}
	record.Definition.Version = "changed"
	if _, err := s.sourceAudit(a).record(deliverySourceContext(ctx, d.ID), owner, record); err == nil {
		t.Fatal("changed collaboration definition accepted")
	}
}
