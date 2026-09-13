package application

import (
	"encoding/json"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestDeliveredScheduleCannotReuseProvenanceForPrivateOrigin(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
	originDef, _ := collaborationTool("delegation_get")
	originBody, _ := json.Marshal(sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "released"}, Messages: []sdk.ConversationAgentMessage{{ID: "private", Content: "private plan input"}}})
	original := persistence.ConversationToolExecution{State: "completed", Definition: originDef, Call: sdk.ConversationToolCall{ID: "source-call", Name: originDef.Key, Arguments: `{"id":"released"}`}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: "released", Content: originBody}}
	def, _ := scheduleResultReadDefinition("schedule_get")
	record := persistence.ConversationToolExecution{State: "completed", Step: 1, Definition: def, Call: sdk.ConversationToolCall{ID: "outer", Name: def.Key, Arguments: `{"plan_id":"plan"}`}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: "plan", Content: json.RawMessage(`{"operation":"get","plan":{"id":"plan","conversation_id":"source","run_id":"source-run"}}`)}}
	repo := &historyReceiptRepository{personalReceiptRepository: &personalReceiptRepository{a: a, record: record}, original: original, snapshot: persistence.ConversationSourceSnapshot{Calls: []persistence.ConversationToolExecution{original}}}
	// This host represents a successful owner read; actual closed plan validation
	// is covered by Tools and the real HTTP composition acceptance.
	host := &deliveryReadTestHost{}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{ToolDefinitions: []sdk.ConversationToolDefinition{def}, ToolHost: &profileToolHost{base: host, allowed: map[string]bool{}}, CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read", "execution_read"}}}
	ctx := deliverySourceContext(t.Context(), "released")
	audit := s.sourceAudit(a)
	for _, prefix := range []int{0, 2} {
		if _, err := audit.run(ctx, sdk.ConversationRunReference{ConversationID: "source", RunID: "source-run", BeforeStep: prefix}); err != nil {
			t.Fatal("provenance access denied", err)
		}
	}
	owner := sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}
	if _, err := audit.record(ctx, owner, record); !collaborationDenied(err) {
		t.Fatal("plan reused provenance-only access for raw communication", err)
	}
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "delivery_read", "execution_read", "communicate"}
	if _, err := s.sourceAudit(a).record(ctx, owner, record); err != nil {
		t.Fatal("restored original communication access denied", err)
	}
	// Attest the actual saved execution before accepting a model-shaped receipt.
	forged := record
	forged.IdempotencyKey = "invented"
	if _, err := s.sourceAudit(a).record(ctx, owner, forged); err == nil {
		t.Fatal("forged original operation identity accepted")
	}
	repo.missingSource = true
	if _, err := s.sourceAudit(a).record(ctx, owner, record); err == nil {
		t.Fatal("missing origin still readable")
	}
	if host.executionChecks != 0 {
		t.Fatal("receipt read used producer execution permission", host.executionChecks)
	}
}
