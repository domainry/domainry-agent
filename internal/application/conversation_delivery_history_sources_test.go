package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type historyReceiptRepository struct {
	*personalReceiptRepository
	persistence.ConversationHistoryRepository
	original                                  persistence.ConversationToolExecution
	message                                   sdk.ConversationMessage
	snapshot                                  persistence.ConversationSourceSnapshot
	missingSource, missingRun, missingMessage bool
	sameRun                                   bool
}

func (r *historyReceiptRepository) Get(_ context.Context, id string, a sdk.ConversationAuthority) (sdk.Conversation, error) {
	if a != r.a || id != "producer" && id != "source" || id == "source" && r.missingSource {
		return sdk.Conversation{}, invalidPersonalReceipt()
	}
	return sdk.Conversation{ID: id, DelegationID: "released"}, nil
}
func (r *historyReceiptRepository) Run(_ context.Context, id, run string, a sdk.ConversationAuthority) (sdk.ConversationRun, error) {
	conversation, runID := "source", "source-run"
	if r.sameRun {
		conversation, runID = "producer", "run"
	}
	if a != r.a || id != conversation || run != runID || r.missingRun {
		return sdk.ConversationRun{}, invalidPersonalReceipt()
	}
	return sdk.ConversationRun{ID: run, ConversationID: id}, nil
}
func (r *historyReceiptRepository) ReadExecutionCall(ctx context.Context, id, run string, step int, call string, a sdk.ConversationAuthority) (persistence.ConversationToolExecution, error) {
	if id == "producer" && call == r.record.Call.ID {
		return r.personalReceiptRepository.ReadExecutionCall(ctx, id, run, step, call, a)
	}
	conversation, runID := "source", "source-run"
	if r.sameRun {
		conversation, runID = "producer", "run"
	}
	if a != r.a || id != conversation || run != runID || step != r.original.Step || call != r.original.Call.ID {
		return persistence.ConversationToolExecution{}, invalidPersonalReceipt()
	}
	return r.original, nil
}
func (r *historyReceiptRepository) HistoryMessage(_ context.Context, id, message string, a sdk.ConversationAuthority) (sdk.ConversationMessage, error) {
	if a != r.a || id != "source" || message != r.message.ID || r.missingMessage {
		return sdk.ConversationMessage{}, invalidPersonalReceipt()
	}
	return r.message, nil
}
func (r *historyReceiptRepository) ConversationSourceSnapshot(_ context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	conversation, runID := "source", "source-run"
	if r.sameRun {
		conversation, runID = "producer", "run"
	}
	if a != r.a || ref.ConversationID != conversation || ref.RunID != runID || r.missingRun {
		return persistence.ConversationSourceSnapshot{}, invalidPersonalReceipt()
	}
	return r.snapshot, nil
}

func TestDeliveredHistoryAndExecutionReadOriginalSourcesWithoutReaderTools(t *testing.T) {
	for _, key := range []string{"history_search", "history_read", "execution_read", "tool_result_read"} {
		t.Run(key, func(t *testing.T) {
			a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
			now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
			originalDef, _ := personalResultReadDefinition("ask_user")
			answer := strings.Repeat("原始答复", 120)
			answerJSON, _ := json.Marshal(map[string]string{"answer": answer, "interaction_id": "answered"})
			original := persistence.ConversationToolExecution{State: "completed", Step: 0, Definition: originalDef, Call: sdk.ConversationToolCall{ID: "source-call", Name: "ask_user", Arguments: `{"question":"Which original answer?"}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: answerJSON}, IdempotencyKey: "original-source"}
			message := sdk.ConversationMessage{ID: "message", ConversationID: "source", RunID: "source-run", Role: "assistant", Seq: 1, Content: answer, CreatedAt: now}
			repo := &historyReceiptRepository{personalReceiptRepository: &personalReceiptRepository{a: a}, original: original, message: message, snapshot: persistence.ConversationSourceSnapshot{Run: sdk.ConversationRun{ID: "source-run", ConversationID: "source"}, Calls: []persistence.ConversationToolExecution{original}}}
			policy := &deliveryArtifactPolicy{denied: map[string]bool{}, disabled: map[string]bool{}}
			host := &deliveryReadTestHost{}
			s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{ToolHost: &profileToolHost{base: host, allowed: map[string]bool{}}, ToolAvailability: policy, PersonalAuthorizer: policy, CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read", "execution_read"}}}
			def, _ := historyResultReadDefinition(key)
			var args, value any
			switch key {
			case "history_search":
				args = sdk.ConversationHistorySearch{Query: "原始"}
				value = sdk.ConversationHistorySearchResult{Items: []sdk.ConversationHistoryHit{{ConversationID: message.ConversationID, MessageID: message.ID, RunID: message.RunID, Seq: message.Seq, Role: message.Role, Excerpt: truncateUTF8(answer, 256), CreatedAt: now}}, Complete: true}
			case "history_read":
				args = map[string]any{"conversation_id": "source", "message_id": "message", "offset": 3, "max_bytes": 256}
				text := truncateUTF8(answer[3:], 256)
				value = deliveredHistorySlice{ConversationID: "source", MessageID: "message", RunID: "source-run", Seq: 1, Role: "assistant", Content: text, Offset: 3, NextOffset: 3 + len(text), CreatedAt: now}
			case "execution_read":
				args = sdk.ConversationExecutionRead{ConversationID: "source", RunID: "source-run"}
				value = sdk.ConversationExecutionReadResult{ConversationID: "source", RunID: "source-run", RunStatus: "completed", Items: []sdk.ConversationExecutionEntry{conversationExecutionEntry("source", "source-run", original)}, Complete: true}
			case "tool_result_read":
				ref := sdk.ConversationResultReference{ConversationID: "source", RunID: "source-run", Step: 0, CallID: original.Call.ID, SHA256: conversationDigest(original.Result)}
				args = sdk.ConversationResultRead{Reference: ref, MaxBytes: 256}
				raw, _ := json.Marshal(original.Result)
				text := truncateUTF8(string(raw), 256)
				value = sdk.ConversationResultSlice{Reference: ref, JSONText: text, NextOffset: len(text), TotalBytes: len(raw)}
			}
			content, _ := json.Marshal(value)
			result := sdk.ConversationToolResult{Status: "completed", Content: content}
			record := persistence.ConversationToolExecution{Step: 2, State: "completed", Definition: def, Call: sdk.ConversationToolCall{ID: "outer", Name: key, Arguments: conversationJSONText(args)}, Result: &result, IdempotencyKey: "original-wrapper"}
			repo.record = record
			owner := sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}
			base := context.WithValue(t.Context(), conversationAgentContextKey{}, &sdk.ConversationAgentSnapshot{})
			ctx := deliverySourceContext(base, "released")
			read := func() error { _, err := s.sourceAudit(a).record(ctx, owner, record); return err }
			if _, err := s.sourceAudit(a).record(base, owner, record); err == nil {
				t.Fatal("raw replay used delivery permission")
			}
			checks := host.executionChecks
			if err := read(); err != nil || host.executionChecks != checks {
				t.Fatal("independent source read required producer/reader tool grant", err)
			}
			if len(policy.requests) != 0 {
				t.Fatal("history reader invoked a personal execution authorizer")
			}
			s.options.ContextBytes = 256
			if err := read(); err != nil {
				t.Fatal("new context budget changed historical page", err)
			}
			for _, disabled := range []string{key, "ask_user"} {
				policy.disabled[disabled] = true
				if err := read(); err == nil {
					t.Fatal("disabled source read", disabled)
				}
				delete(policy.disabled, disabled)
			}
			s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "delivery_read"}
			if err := read(); !collaborationDenied(err) {
				t.Fatal("same-delegation raw history bypassed execution_read", err)
			}
			s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "delivery_read", "execution_read"}
			for _, flag := range []*bool{&repo.missingSource, &repo.missingRun} {
				*flag = true
				if err := read(); err == nil {
					t.Fatal("missing source accepted")
				}
				*flag = false
			}
			other := a
			other.UserID = "other"
			if _, err := s.sourceAudit(other).record(ctx, owner, record); err == nil {
				t.Fatal("foreign source accepted")
			}
			bad := record
			bad.Call.Arguments = `{}`
			if _, err := s.sourceAudit(a).record(ctx, owner, bad); err == nil {
				t.Fatal("forged original call accepted")
			}
			// Change the saved authoritative wrapper too: these checks must
			// validate its typed source fields, not merely its outer digest.
			var changed map[string]any
			_ = json.Unmarshal(content, &changed)
			switch key {
			case "history_search":
				changed["items"].([]any)[0].(map[string]any)["excerpt"] = "forged"
			case "history_read":
				changed["next_offset"] = 4
			case "execution_read":
				changed["items"].([]any)[0].(map[string]any)["resource_id"] = "forged"
			case "tool_result_read":
				changed["json_text"] = "forged"
			}
			result.Content, _ = json.Marshal(changed)
			if err := read(); err == nil {
				t.Fatal("unverified inner data accepted")
			}
			result.Content = content
			if key == "history_read" || key == "history_search" {
				repo.message.Content = "different original message"
				if err := read(); err == nil {
					t.Fatal("changed original message accepted")
				}
				repo.message = message
				repo.missingMessage = true
				if err := read(); err == nil {
					t.Fatal("deleted original message accepted")
				}
				repo.missingMessage = false
			}
			attachment := sdk.AttachmentConversationTools()[0]
			repo.snapshot.Calls = []persistence.ConversationToolExecution{{Step: 0, Definition: attachment, Call: sdk.ConversationToolCall{ID: "private", Name: attachment.Key, Arguments: `{}`}, State: "completed", Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"text":"private attachment"}`)}}}
			if err := read(); err == nil {
				t.Fatal("wrapped source shared private attachment")
			}
			repo.snapshot.Calls = []persistence.ConversationToolExecution{original}
			if err := read(); err != nil {
				t.Fatal("restored source unreadable", err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := s.sourceAudit(a).record(cancelled, owner, record); err == nil {
				t.Fatal("cancelled read accepted")
			}
		})
	}
}
