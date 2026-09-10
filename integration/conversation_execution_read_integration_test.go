package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
)

type historyResultHost struct {
	*resultHost
	personal *application.PersonalConversationHost
}

func (h *historyResultHost) ConversationTools(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	definitions, err := h.resultHost.ConversationTools(ctx, a)
	for _, definition := range agentsdk.PersonalConversationTools() {
		if definition.Key == "history_search" || definition.Key == "history_read" {
			definitions = append(definitions, definition)
		}
	}
	return definitions, err
}
func (h *historyResultHost) InvokeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	if strings.HasPrefix(in.Call.Name, "history_") {
		return h.personal.InvokeConversationTool(ctx, in)
	}
	return h.resultHost.InvokeConversationTool(ctx, in)
}

type historyResultModel struct {
	*executionModel
	summaries atomic.Int32
}

func (m *historyResultModel) GenerateConversation(_ context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	if in.Purpose != "summary" {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("unexpected text call")
	}
	m.summaries.Add(1)
	// Deliberately omit all IDs. Original execution evidence must survive
	// repeated lossy summaries independently of model-written text.
	return agentsdk.ConversationModelResult{Content: `{"goal":"跟进最早的对账工作","constraints":[],"facts":[],"decisions":[],"open_items":["核对费用"]}`}, nil
}
func executionAnswer(text string) agentsdk.ConversationStepResult {
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: text}, FinishReason: "stop"}
}

func TestExecutionReferencesSurviveTurnsSummariesAndServiceRestart(t *testing.T) {
	repo := conversationRepository(t)
	host := &historyResultHost{resultHost: &resultHost{allowed: true, reader: true, index: true, payload: "原始费用证据"}}
	var err error
	host.personal, err = application.NewPersonalConversationHost(repo, host, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &historyResultModel{executionModel: &executionModel{}}
	var sourceRun, sourceConversation string
	var reads atomic.Int32
	model.step = func(_ int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "user" {
			switch last.Content {
			case "启动对账任务":
				return resultToolCall("large_operation", "original-operation", map[string]string{"title": "对账"}), nil
			case "核对刚才实际返回的资源":
				for _, message := range in.Messages {
					if !strings.HasPrefix(message.Content, "Recent execution links") {
						continue
					}
					var links []agentsdk.ConversationExecutionRead
					if json.Unmarshal([]byte(strings.Split(message.Content, "\n")[1]), &links) != nil || len(links) != 1 {
						return agentsdk.ConversationStepResult{}, fmt.Errorf("automatic run link missing")
					}
					return resultToolCall("execution_read", "inspect", links[0]), nil
				}
				return agentsdk.ConversationStepResult{}, fmt.Errorf("no execution index")
			case "查找最早对账任务的原始费用证据":
				for _, message := range in.Messages {
					if strings.Contains(message.Content, sourceRun) || strings.Contains(message.Content, "work-item-awaiting-review") {
						t.Error("test did not evict the original reference")
					}
				}
				return resultToolCall("history_search", "find-source", map[string]any{"query": "启动对账任务"}), nil
			default:
				return executionAnswer(strings.Repeat("讨论后续安排，费用仍待核对。", 180)), nil
			}
		}
		var result agentsdk.ConversationToolResult
		if json.Unmarshal([]byte(last.Content), &result) != nil || result.Status != "completed" {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("tool did not complete: %s", last.Content)
		}
		switch last.ToolCallID {
		case "original-operation":
			return executionAnswer("已提交，费用仍待核对。"), nil
		case "find-source":
			var hits agentsdk.ConversationHistorySearchResult
			if json.Unmarshal(result.Content, &hits) != nil || len(hits.Items) != 1 || hits.Items[0].RunID != sourceRun {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("history search lost run ID")
			}
			return resultToolCall("history_read", "original-message", map[string]string{"conversation_id": hits.Items[0].ConversationID, "message_id": hits.Items[0].MessageID}), nil
		case "original-message":
			var message struct {
				RunID          string `json:"run_id"`
				ConversationID string `json:"conversation_id"`
			}
			if json.Unmarshal(result.Content, &message) != nil || message.RunID != sourceRun {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("history reader lost run ID")
			}
			return resultToolCall("execution_read", "inspect", agentsdk.ConversationExecutionRead{ConversationID: message.ConversationID, RunID: message.RunID}), nil
		case "inspect":
			var page agentsdk.ConversationExecutionReadResult
			if json.Unmarshal(result.Content, &page) != nil || len(page.Items) != 1 || !page.Complete || page.Omitted || page.Items[0].Reference == nil || page.Items[0].ResourceID != "work-item-awaiting-review" {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("missing actual execution reference: %s", result.Content)
			}
			return resultToolCall("tool_result_read", "full-result", agentsdk.ConversationResultRead{Reference: *page.Items[0].Reference}), nil
		case "full-result":
			var page agentsdk.ConversationResultSlice
			if json.Unmarshal(result.Content, &page) != nil || !page.Complete || !strings.Contains(page.JSONText, "原始费用证据") {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("original evidence missing")
			}
			reads.Add(1)
			return executionAnswer("原始记录仍注明费用待核对，尚未核查业务系统的最新状态。"), nil
		}
		return agentsdk.ConversationStepResult{}, fmt.Errorf("unexpected tool")
	}
	options := application.ConversationOptions{ToolHost: host, ContextBytes: 20000, SummaryBytes: 512, Poll: 5 * time.Millisecond}
	service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { service.Close() }()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "history-evidence"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	sourceConversation = c.ID
	send := func(id, message string) agentsdk.ConversationRun {
		t.Helper()
		run, err := service.Send(t.Context(), sourceConversation, agentsdk.ConversationSend{ClientMessageID: id, Message: message}, conversationAuthority())
		if err != nil {
			t.Fatal(err)
		}
		final := waitConversation(t, service, sourceConversation, run.ID)
		if final.Status != "completed" {
			t.Fatalf("%s failed: %s", id, final.ErrorCode)
		}
		return final
	}
	seed := send("seed", "启动对账任务")
	sourceRun = seed.ID
	if seed.Steps[0].Calls[0].ResultReference == nil {
		t.Fatal("run snapshot lost immutable result reference")
	}
	send("follow-up", "核对刚才实际返回的资源")
	for i := 0; i < 9; i++ {
		send(fmt.Sprintf("filler-%d", i), "讨论后续安排")
	}
	if model.summaries.Load() < 2 {
		t.Fatal("test did not replace the summary repeatedly")
	}
	service.Close()
	service, err = application.NewConversationService(repo, model, conversationAuthority().RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	send("recover", "查找最早对账任务的原始费用证据")
	if reads.Load() != 2 {
		t.Fatalf("full reads = %d", reads.Load())
	}
	host.mu.Lock()
	invokes := host.invokes
	host.mu.Unlock()
	if invokes != 1 {
		t.Fatal("historical inspection repeated the original effect")
	}
}

func TestExecutionReadPaginationIsolationAndRevocation(t *testing.T) {
	for _, mode := range []string{"pages", "denied", "changed", "revoked_after_read", "revoked_after_freeze", "nested"} {
		t.Run(mode, func(t *testing.T) {
			repo := conversationRepository(t)
			host := &resultHost{allowed: true, reader: true, index: true, payload: "private-result-content"}
			model := &executionModel{}
			model.step = func(_ int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				if in.Messages[len(in.Messages)-1].Role == "user" {
					out := resultToolCall("large_operation", "call-a", map[string]string{"title": "a"})
					for _, name := range []string{"b", "c"} {
						out.Message.ToolCalls = append(out.Message.ToolCalls, resultToolCall("large_operation", "call-"+name, map[string]string{"title": name}).Message.ToolCalls[0])
					}
					return out, nil
				}
				return executionAnswer("已提交。"), nil
			}
			options := application.ConversationOptions{ToolHost: host, Poll: 5 * time.Millisecond}
			service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { service.Close() }()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "source"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "source", Message: "创建三个事项"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			if final := waitConversation(t, service, c.ID, run.ID); final.Status != "completed" {
				t.Fatal(final.ErrorCode)
			}
			service.Close()
			args := agentsdk.ConversationExecutionRead{ConversationID: c.ID, RunID: run.ID, Limit: 2}
			first, err := repo.ReadExecutionCalls(t.Context(), args, conversationAuthority())
			if err != nil || len(first.Items) != 2 || first.Complete || first.NextCursor == "" {
				t.Fatal("ledger page missing", err)
			}
			for _, scope := range []string{"user", "workspace", "runtime"} {
				other := conversationAuthority()
				switch scope {
				case "user":
					other.UserID = "other"
				case "workspace":
					other.WorkspaceID = "other"
				case "runtime":
					other.RuntimeID = "other"
				}
				if _, err := repo.ReadExecutionCalls(t.Context(), args, other); err == nil {
					t.Fatal("cross-owner run readable")
				}
				if _, err := repo.ReadExecutionCall(t.Context(), c.ID, run.ID, 0, "call-a", other); err == nil {
					t.Fatal("cross-owner call readable")
				}
			}
			bad := args
			bad.Cursor = first.NextCursor
			bad.Limit = 1
			if _, err := repo.ReadExecutionCalls(t.Context(), bad, conversationAuthority()); err == nil {
				t.Fatal("cursor accepted changed query")
			}
			host.mu.Lock()
			switch mode {
			case "denied":
				host.allowed = false
			case "changed":
				host.version = "2"
			case "revoked_after_read":
				host.revokeOnSourceRead = true
			}
			host.mu.Unlock()
			model = &executionModel{}
			seen := map[string]bool{}
			model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				if number == 1 {
					return resultToolCall("execution_read", "inspect-0", args), nil
				}
				if mode == "revoked_after_read" {
					t.Error("source data reached model after permission revoked")
				}
				if mode == "revoked_after_freeze" {
					return agentsdk.ConversationStepResult{}, fmt.Errorf("disconnect after authorized frozen index")
				}
				var output agentsdk.ConversationToolResult
				last := in.Messages[len(in.Messages)-1]
				if json.Unmarshal([]byte(last.Content), &output) != nil || output.Status != "completed" {
					return agentsdk.ConversationStepResult{}, fmt.Errorf("index failed: %s", last.Content)
				}
				var page agentsdk.ConversationExecutionReadResult
				if json.Unmarshal(output.Content, &page) != nil {
					return agentsdk.ConversationStepResult{}, fmt.Errorf("invalid page")
				}
				if mode == "denied" || mode == "changed" {
					if len(page.Items) != 0 || !page.Omitted || strings.Contains(last.Content, "work-item-awaiting-review") || strings.Contains(last.Content, "large_operation") {
						t.Error("unauthorized metadata disclosed")
					}
					return executionAnswer("原结果当前不可读取。"), nil
				}
				for _, entry := range page.Items {
					if seen[entry.CallID] {
						t.Error("duplicate call across pages")
					}
					seen[entry.CallID] = true
					if entry.Reference == nil {
						t.Error("missing result reference")
					}
				}
				if mode != "nested" && !page.Complete {
					next := args
					next.Cursor = page.NextCursor
					return resultToolCall("execution_read", fmt.Sprintf("inspect-%d", number), next), nil
				}
				return executionAnswer("已查看记录。"), nil
			}
			service, err = application.NewConversationService(repo, model, conversationAuthority().RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			reader, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "reader"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			readRun, err := service.Send(t.Context(), reader.ID, agentsdk.ConversationSend{ClientMessageID: "inspect", Message: "查看之前的处理记录"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			final := waitConversation(t, service, reader.ID, readRun.ID)
			if mode == "revoked_after_freeze" {
				if final.Status != "failed" {
					t.Fatal("disconnect not observed")
				}
				service.Close()
				host.mu.Lock()
				host.allowed = false
				host.mu.Unlock()
				service, err = application.NewConversationService(repo, model, conversationAuthority().RuntimeID, options)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = service.Resume(t.Context(), reader.ID, readRun.ID, conversationAuthority()); err != nil {
					t.Fatal(err)
				}
				final = waitConversation(t, service, reader.ID, readRun.ID)
				model.mu.Lock()
				count := model.calls
				model.mu.Unlock()
				if count != 2 {
					t.Fatal("revoked frozen input reached model on resume")
				}
			}
			if strings.HasPrefix(mode, "revoked_") {
				if final.Status != "failed" || final.ErrorCode != "tool_access_denied" {
					t.Fatalf("revocation not enforced: %s/%s", final.Status, final.ErrorCode)
				}
			} else if final.Status != "completed" {
				t.Fatal(final.ErrorCode)
			}
			if mode == "pages" && len(seen) != 3 {
				t.Fatal("pagination did not cover all calls")
			}
			if mode == "nested" {
				service.Close()
				ref := final.Steps[0].Calls[0].ResultReference
				if ref == nil {
					t.Fatal("index result not stored")
				}
				host.mu.Lock()
				host.allowed = false
				host.mu.Unlock()
				model = &executionModel{}
				model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
					if number == 1 {
						return resultToolCall("tool_result_read", "nested", agentsdk.ConversationResultRead{Reference: *ref}), nil
					}
					last := in.Messages[len(in.Messages)-1].Content
					if !strings.Contains(last, "tool_access_denied") || strings.Contains(last, "work-item-awaiting-review") {
						t.Error("nested index bypassed source authorization")
					}
					return executionAnswer("原工具权限已撤销。"), nil
				}
				service, err = application.NewConversationService(repo, model, conversationAuthority().RuntimeID, options)
				if err != nil {
					t.Fatal(err)
				}
				nested, err := service.Send(t.Context(), reader.ID, agentsdk.ConversationSend{ClientMessageID: "nested", Message: "读取刚才的结果目录"}, conversationAuthority())
				if err != nil {
					t.Fatal(err)
				}
				if result := waitConversation(t, service, reader.ID, nested.ID); result.Status != "completed" {
					t.Fatal(result.ErrorCode)
				}
			}
		})
	}
}
