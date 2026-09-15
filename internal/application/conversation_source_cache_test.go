package application

import (
	"context"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestSharedSourceTraversalIsMemoizedOnlyWithinOneRead(t *testing.T) {
	s, r, _, reader, _ := contractPublicationServiceFixture()
	root := r.original.Requirements.Sources[0]
	ctx := context.WithValue(t.Context(), conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "contract", delegationID: r.d.ID, roots: []sdk.ConversationRunReference{root}})
	audit := s.sourceAudit(reader)
	for i := 0; i < 3; i++ {
		if _, err := audit.run(ctx, root); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.reads[root.RunID]) != 1 {
		t.Fatal("shared proof traversed repeatedly in one read", r.reads)
	}
	r.sourceErr = conversationFailure("forbidden", "current_field_denied")
	if _, err := s.sourceAudit(reader).run(ctx, root); !collaborationDenied(err) {
		t.Fatal("later read reused an earlier data decision", err)
	}
}

func TestSourceMemoCannotAuthorizeRawCollaborationAfterDeliveryProjection(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
	def, _ := collaborationTool("delegation_get")
	body := conversationJSONText(sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "released"}, Messages: []sdk.ConversationAgentMessage{{ID: "private", Content: "private working message"}}})
	repo := &historyReceiptRepository{personalReceiptRepository: &personalReceiptRepository{a: a}}
	repo.snapshot.Calls = []persistence.ConversationToolExecution{{State: "completed", Definition: def, Call: sdk.ConversationToolCall{ID: "source-call", Name: def.Key, Arguments: `{"id":"released"}`}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: "released", Content: []byte(body)}}}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read", "execution_read"}}}
	audit := s.sourceAudit(a)
	ref := sdk.ConversationRunReference{ConversationID: "source", RunID: "source-run"}
	ctx := deliverySourceContext(t.Context(), "released")
	if _, err := audit.run(ctx, ref); err != nil {
		t.Fatal("delivery provenance denied", err)
	}
	rawCtx, raw := audit.rawExecutionSourceAudit(ctx)
	if _, err := raw.run(rawCtx, ref); !collaborationDenied(err) {
		t.Fatal("completed delivery proof authorized raw private messages", err)
	}
}

func TestSourceMemoRejectsUnserializableReadBoundary(t *testing.T) {
	s, r, _, reader, _ := contractPublicationServiceFixture()
	root := r.original.Requirements.Sources[0]
	ctx := context.WithValue(t.Context(), conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "contract", delegationID: r.d.ID, roots: []sdk.ConversationRunReference{root}})
	ctx = context.WithValue(ctx, conversationPeerRequestKey{}, conversationPeerRequest{ToolRequest: &sdk.ConversationToolRequest{Definition: sdk.ConversationToolDefinition{InputSchema: []byte("{")}}})
	if _, err := s.sourceAudit(reader).run(ctx, root); err == nil || len(r.reads[root.RunID]) != 0 {
		t.Fatal("invalid read boundary reused a common fallback digest or read data", err, r.reads)
	}
}
