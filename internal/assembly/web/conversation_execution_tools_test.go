package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

type executionToolsCommand struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Nonce     string          `json:"nonce"`
}

func executionToolsModelStep(in sdk.ConversationStepRequest) (sdk.ConversationStepResult, bool) {
	text := ""
	for _, message := range in.Messages {
		if message.Role == "user" {
			text = message.Content
		}
	}
	const prefix = "Execution tools fixture:\n"
	if !strings.HasPrefix(text, prefix) {
		return sdk.ConversationStepResult{}, false
	}
	var command executionToolsCommand
	if json.Unmarshal([]byte(strings.TrimPrefix(text, prefix)), &command) != nil {
		return sdk.ConversationStepResult{}, false
	}
	id := "execution-tools-" + command.Nonce
	for _, message := range in.Messages {
		if message.Role == "tool" && message.ToolCallID == id {
			return sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "Scoped execution tool outcome received."}}, true
		}
	}
	return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: id, Name: command.Tool, Arguments: string(command.Arguments)}}}}, true
}

func exerciseExecutionToolsHTTP(t *testing.T, executor, issuer, reader *browser, d sdk.ConversationDelegationDetail, read func(*browser) sdk.ConversationDelegationDetail, setScope func(string, []string)) {
	t.Helper()
	ref := sdk.ConversationRunReference{ConversationID: d.ConversationID, RunID: d.Task.ExecutionRunID}
	invoke := func(b *browser, nonce, tool string, args any, confirmation bool) (sdk.ConversationToolResult, string) {
		t.Helper()
		conversation := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "execution-tool-" + nonce, Title: "Scoped execution tool"}), 200))
		command := executionToolsCommand{Tool: tool, Arguments: json.RawMessage(accountJSON(args)), Nonce: nonce}
		if tool == "delegation_execution_publish" {
			var body map[string]any
			_ = json.Unmarshal(command.Arguments, &body)
			delete(body["publication"].(map[string]any), "client_id")
			command.Arguments, _ = json.Marshal(body)
		}
		run := accountDecode[sdk.ConversationRun](t, b.call("POST", "/agent/conversations/"+conversation.ID+"/messages", accountJSON(sdk.ConversationSend{ClientMessageID: "tool-" + nonce, Message: "Execution tools fixture:\n" + accountJSON(command)}), 202))
		path := "/agent/conversations/" + conversation.ID + "/runs/" + run.ID
		deadline, approved := time.Now().Add(15*time.Second), false
		for time.Now().Before(deadline) {
			run = accountDecode[sdk.ConversationRun](t, b.call("GET", path, "", 200))
			if interaction := run.Interaction; interaction != nil && interaction.Status == "pending" {
				if !confirmation {
					t.Fatal("read tool unexpectedly asked for confirmation", tool)
				}
				b.call("POST", path+"/respond", accountJSON(sdk.ConversationInteractionResponse{InteractionID: interaction.ID, ClientID: "approve-" + interaction.ID, ExpectedRevision: interaction.Revision, Decision: "approve"}), 200)
				approved = true
			}
			if run.Terminal() {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if run.Status != "completed" || approved != confirmation {
			calls := []string{}
			for _, step := range run.Steps {
				for _, call := range step.Calls {
					calls = append(calls, call.Name+":"+call.Status+":"+call.ErrorCode)
				}
			}
			t.Fatal("tool run or confirmation boundary failed", tool, run.Status, run.ErrorCode, approved, calls)
		}
		for _, step := range run.Steps {
			for _, call := range step.Calls {
				if call.Name != tool {
					continue
				}
				if call.ResultReference == nil {
					t.Fatal("tool result lost original reference", tool, call)
				}
				text, offset := "", 0
				for {
					page := accountDecode[sdk.ConversationResultSlice](t, b.call("POST", path+"/result", accountJSON(sdk.ConversationResultRead{Reference: *call.ResultReference, Offset: offset, MaxBytes: 8192}), 200))
					text += page.JSONText
					if page.Complete {
						break
					}
					if page.NextOffset <= offset {
						t.Fatal("tool result pagination did not advance", tool)
					}
					offset = page.NextOffset
				}
				var result sdk.ConversationToolResult
				if json.Unmarshal([]byte(text), &result) != nil {
					t.Fatal("tool result slice invalid", tool)
				}
				return result, path
			}
		}
		t.Fatal("model did not execute scoped tool", tool)
		return sdk.ConversationToolResult{}, ""
	}

	publication := sdk.ConversationDelegationExecutionPublish{ID: d.ID, Publication: sdk.ConversationExecutionShare{Reference: ref, ExpectedRevision: read(issuer).Revision, Reason: "实际执行用户通过 Agent 工具共享原运行"}}
	result, _ := invoke(executor, "publish", "delegation_execution_publish", publication, true)
	var receipt sdk.ConversationExecutionPublication
	if result.Status != "completed" || json.Unmarshal(result.Content, &receipt) != nil || receipt.Reference != ref || receipt.Publisher.UserID != d.ExecutionSubject.UserID {
		t.Fatal("Agent publication changed actual ownership", result)
	}
	result, _ = invoke(reader, "index", "delegation_executions", map[string]string{"id": d.ID}, false)
	var index sdk.ConversationDelegationExecutionIndex
	if result.Status != "completed" || json.Unmarshal(result.Content, &index) != nil || index.DelegationID != d.ID || len(index.Publications) != 1 || index.Publications[0].Reference != ref {
		t.Fatal("third-user Agent missed exact shared run", result)
	}
	result, runPath := invoke(reader, "read", "delegation_execution_read", sdk.ConversationDelegationExecutionRead{ID: d.ID, Reference: ref}, false)
	var observed sdk.ConversationDelegationExecutionView
	if result.Status != "completed" || json.Unmarshal(result.Content, &observed) != nil || observed.Run.ID != ref.RunID || len(observed.Run.Steps) != 7 || observed.Run.Interaction != nil || observed.Run.WriteScope != nil {
		t.Fatal("third-user Agent inspection incomplete or carried controls", result)
	}
	input := sdk.ConversationResultRead{Reference: d.Delivery.Conditions[0].Receipts[0], MaxBytes: 256}
	result, _ = invoke(reader, "result", "delegation_execution_result_read", sdk.ConversationDelegationExecutionResultRead{ID: d.ID, Read: input}, false)
	var saved sdk.ConversationDelegationExecutionResult
	if result.Status != "completed" || json.Unmarshal(result.Content, &saved) != nil || saved.Result.Reference != input.Reference || saved.Result.NextOffset < 1 {
		t.Fatal("Agent result changed original receipt", result)
	}
	setScope("execution-tool-source-withdraw", []string{"view", "execution_read", "delivery_read"})
	// A previously saved read wrapper remains subject to its original raw
	// communication source rights; ordinary owner history cannot bypass it.
	withdrawn := accountDecode[sdk.ConversationRun](t, reader.call("GET", runPath, "", 200))
	if withdrawn.AccessError == "" || len(withdrawn.Steps) != 0 {
		t.Fatal("recorded wrapper retained withdrawn source content", withdrawn.AccessError)
	}
	setScope("execution-tool-source-restore", []string{"view", "execution_read", "communicate", "delivery_read"})
	wrong := ref
	wrong.RunID = "not-the-shared-run"
	result, _ = invoke(reader, "wrong-reference", "delegation_execution_read", sdk.ConversationDelegationExecutionRead{ID: d.ID, Reference: wrong}, false)
	if result.Status != "failed" || result.ErrorCode != "execution_not_shared" {
		t.Fatal("Agent could choose an unpublished run", result)
	}
	publication.Publication.ExpectedRevision = read(issuer).Revision
	publication.Publication.Withdraw = true
	result, _ = invoke(executor, "withdraw", "delegation_execution_publish", publication, true)
	if result.Status != "completed" || json.Unmarshal(result.Content, &receipt) != nil || !receipt.Withdrawn {
		t.Fatal("Agent withdrawal failed", result)
	}
	reader.call("POST", "/agent/delegations/"+d.ID+"/execution", accountJSON(ref), 403)
	publication.Publication.ExpectedRevision = read(issuer).Revision
	publication.Publication.Withdraw = false
	result, _ = invoke(executor, "republish", "delegation_execution_publish", publication, true)
	if result.Status != "completed" {
		t.Fatal("Agent could not explicitly republish original run", result)
	}
	t.Log("Actual third-user Agent execution index, seven-step read, exact result page, current source revocation, unpublished-run rejection and confirmed owner publication/withdrawal/republish verified")
}
