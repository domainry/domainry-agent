package application

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestDeliveredWrappersCannotReuseProvenanceAccessForRawCollaboration(t *testing.T) {
	for _, key := range []string{"history_search", "history_read", "execution_read", "tool_result_read"} {
		t.Run(key, func(t *testing.T) {
			a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
			def, _ := collaborationTool("delegation_get")
			body, _ := json.Marshal(sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "released"}, Messages: []sdk.ConversationAgentMessage{{ID: "message", Content: "private working message"}}})
			original := persistence.ConversationToolExecution{State: "completed", Definition: def, Call: sdk.ConversationToolCall{ID: "source-call", Name: def.Key, Arguments: `{"id":"released"}`}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: "released", Content: body}}
			message := sdk.ConversationMessage{ID: "message", ConversationID: "source", RunID: "source-run", Seq: 1, Role: "assistant", Content: "private working message"}
			repo := &historyReceiptRepository{personalReceiptRepository: &personalReceiptRepository{a: a}, original: original, message: message, snapshot: persistence.ConversationSourceSnapshot{Calls: []persistence.ConversationToolExecution{original}}}
			s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read", "execution_read"}}}
			var args, value any
			switch key {
			case "history_search":
				args = sdk.ConversationHistorySearch{Query: "private"}
				value = sdk.ConversationHistorySearchResult{Items: []sdk.ConversationHistoryHit{{ConversationID: "source", MessageID: "message", RunID: "source-run", Seq: 1, Role: "assistant", Excerpt: message.Content}}, Complete: true}
			case "history_read":
				args = map[string]string{"conversation_id": "source", "message_id": "message"}
				value = deliveredHistorySlice{ConversationID: "source", MessageID: "message", RunID: "source-run", Seq: 1, Role: "assistant", Content: message.Content, NextOffset: len(message.Content), Complete: true}
			case "execution_read":
				args = sdk.ConversationExecutionRead{ConversationID: "source", RunID: "source-run"}
				value = sdk.ConversationExecutionReadResult{ConversationID: "source", RunID: "source-run", Items: []sdk.ConversationExecutionEntry{conversationExecutionEntry("source", "source-run", original)}, Complete: true}
			case "tool_result_read":
				ref := sdk.ConversationResultReference{ConversationID: "source", RunID: "source-run", CallID: original.Call.ID, SHA256: conversationDigest(original.Result)}
				args = sdk.ConversationResultRead{Reference: ref}
				raw, _ := json.Marshal(original.Result)
				value = sdk.ConversationResultSlice{Reference: ref, JSONText: string(raw), NextOffset: len(raw), TotalBytes: len(raw), Complete: true}
			}
			wrapperDef, _ := historyResultReadDefinition(key)
			data, _ := json.Marshal(value)
			record := persistence.ConversationToolExecution{Step: 2, State: "completed", Definition: wrapperDef, Call: sdk.ConversationToolCall{ID: "outer", Name: key, Arguments: conversationJSONText(args)}, Result: &sdk.ConversationToolResult{Status: "completed", Content: data}}
			repo.record = record
			ctx := deliverySourceContext(t.Context(), "released")
			audit := s.sourceAudit(a)
			for _, prefix := range []int{0, 2} {
				if _, err := audit.run(ctx, sdk.ConversationRunReference{ConversationID: "source", RunID: "source-run", BeforeStep: prefix}); err != nil {
					t.Fatal("same-delegation provenance unexpectedly denied", err)
				}
			}
			if _, err := audit.record(ctx, sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}, record); !collaborationDenied(err) {
				t.Fatal("cached provenance disclosed raw collaboration through wrapper", err)
			}
			s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "delivery_read", "execution_read", "communicate"}
			if _, err := s.sourceAudit(a).record(ctx, sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}, record); err != nil {
				t.Fatal("restored raw collaboration data permission did not allow read", err)
			}
		})
	}
}

func TestDeliveredEmptyPagesStillRequireSourceOwnershipAndExecutionRead(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
	for _, key := range []string{"history_search", "execution_read"} {
		t.Run(key, func(t *testing.T) {
			var args, value any
			if key == "history_search" {
				args = sdk.ConversationHistorySearch{Query: "missing", ConversationID: "source"}
				value = sdk.ConversationHistorySearchResult{Items: []sdk.ConversationHistoryHit{}, Complete: true}
			} else {
				args = sdk.ConversationExecutionRead{ConversationID: "source", RunID: "source-run"}
				value = sdk.ConversationExecutionReadResult{ConversationID: "source", RunID: "source-run", Items: []sdk.ConversationExecutionEntry{}, Complete: true}
			}
			def, _ := historyResultReadDefinition(key)
			body, _ := json.Marshal(value)
			record := persistence.ConversationToolExecution{State: "completed", Step: 1, Definition: def, Call: sdk.ConversationToolCall{ID: "outer", Name: key, Arguments: conversationJSONText(args)}, Result: &sdk.ConversationToolResult{Status: "completed", Content: body}}
			repo := &historyReceiptRepository{personalReceiptRepository: &personalReceiptRepository{a: a, record: record}}
			s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read", "execution_read"}}}
			ctx := deliverySourceContext(context.Background(), "released")
			read := func() error {
				_, err := s.sourceAudit(a).record(ctx, sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}, record)
				return err
			}
			if err := read(); err != nil {
				t.Fatal("owned empty page denied", err)
			}
			repo.missingSource = true
			if err := read(); err == nil {
				t.Fatal("empty page exposed deleted source")
			}
			repo.missingSource = false
			s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "delivery_read"}
			if err := read(); !collaborationDenied(err) {
				t.Fatal("empty page exposed private source without execution_read", err)
			}
		})
	}
}

func TestDeliveredResultWrapperAllowsOwnEarlierCallButRejectsForwardReferences(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
	def, _ := personalResultReadDefinition("calculate")
	value, _ := calculateConversation(calculationInput{Operation: "expression", Expression: "1+2"})
	body, _ := json.Marshal(value)
	original := persistence.ConversationToolExecution{Step: 0, State: "completed", Definition: def, Call: sdk.ConversationToolCall{ID: "source-call", Name: def.Key, Arguments: `{"operation":"expression","expression":"1+2"}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: body}}
	wrapperDef, _ := historyResultReadDefinition("tool_result_read")
	record := persistence.ConversationToolExecution{Step: 1, State: "completed", Definition: wrapperDef, Call: sdk.ConversationToolCall{ID: "outer", Name: wrapperDef.Key}, Result: &sdk.ConversationToolResult{Status: "completed"}}
	repo := &historyReceiptRepository{sameRun: true, personalReceiptRepository: &personalReceiptRepository{a: a}, original: original, snapshot: persistence.ConversationSourceSnapshot{Calls: []persistence.ConversationToolExecution{original}}}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read", "execution_read"}}}
	ctx := deliverySourceContext(t.Context(), "released")
	for _, step := range []int{0, 1, 2} {
		ref := sdk.ConversationResultReference{ConversationID: "producer", RunID: "run", Step: step, CallID: original.Call.ID, SHA256: conversationDigest(original.Result)}
		raw, _ := json.Marshal(original.Result)
		record.Call.Arguments = conversationJSONText(sdk.ConversationResultRead{Reference: ref})
		record.Result.Content, _ = json.Marshal(sdk.ConversationResultSlice{Reference: ref, JSONText: string(raw), TotalBytes: len(raw), NextOffset: len(raw), Complete: true})
		repo.record = record
		repo.original.Step = step
		_, err := s.sourceAudit(a).record(ctx, sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}, record)
		if step == 0 && err != nil {
			t.Fatal("own earlier immutable call unreadable", err)
		}
		if step > 0 && err == nil {
			t.Fatal("current/future call reference accepted", step)
		}
	}
}

func TestDeliveredExecutionIndexDoesNotReleaseUnfinishedOrErrorPayloads(t *testing.T) {
	for _, state := range []string{"started", "failed-result"} {
		t.Run(state, func(t *testing.T) {
			a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
			definition, _ := personalResultReadDefinition("calculate")
			original := persistence.ConversationToolExecution{State: "started", Definition: definition, Call: sdk.ConversationToolCall{ID: "source-call", Name: definition.Key, Arguments: `{"operation":"expression","expression":"1+2"}`}}
			if state == "failed-result" {
				original.State = "completed"
				original.Result = &sdk.ConversationToolResult{Status: "failed", ErrorCode: "private_failure", Content: json.RawMessage(`{"private":"failure details"}`)}
			}
			def, _ := historyResultReadDefinition("execution_read")
			page := sdk.ConversationExecutionReadResult{ConversationID: "source", RunID: "source-run", Items: []sdk.ConversationExecutionEntry{conversationExecutionEntry("source", "source-run", original)}, Complete: true}
			body, _ := json.Marshal(page)
			record := persistence.ConversationToolExecution{Step: 1, State: "completed", Definition: def, Call: sdk.ConversationToolCall{ID: "outer", Name: def.Key, Arguments: `{"conversation_id":"source","run_id":"source-run"}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: body}}
			repo := &historyReceiptRepository{personalReceiptRepository: &personalReceiptRepository{a: a, record: record}, original: original, snapshot: persistence.ConversationSourceSnapshot{Calls: []persistence.ConversationToolExecution{original}}}
			host := &deliveryReadTestHost{ConversationToolHost: catalogOnlyHost{definitions: []sdk.ConversationToolDefinition{definition}}}
			s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{ToolHost: &profileToolHost{base: host, allowed: map[string]bool{}}, CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read", "execution_read"}}}
			ctx := deliverySourceContext(t.Context(), "released")
			if _, err := s.sourceAudit(a).record(ctx, sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}, record); err == nil {
				t.Fatal("ignored unsuccessful output bypassed original execution authorization")
			}
		})
	}
}
