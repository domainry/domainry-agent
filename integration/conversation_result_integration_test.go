package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
)

type resultHost struct {
	mu                 sync.Mutex
	allowed            bool
	reader             bool
	index              bool
	invokes            int
	payload            string
	version            string
	revokeOnSourceRead bool
}

func (h *resultHost) ConversationTools(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	version := h.version
	if version == "" {
		version = "1"
	}
	tools := []agentsdk.ConversationToolDefinition{{Key: "large_operation", Version: version, Description: "Produce a large stored result for an accepted work item", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object"}`), ActionKey: "work.start", Effect: "write", Idempotency: "key", MaxOutputBytes: 1024 * 1024, TimeoutMillis: 1000}}
	if h.index {
		tools = append(tools, agentsdk.ConversationExecutionReadDefinition())
	}
	if h.reader {
		tools = append(tools, agentsdk.ConversationToolResultReadDefinition())
	}
	return tools, nil
}
func (h *resultHost) AuthorizeConversationTool(_ context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	granted := h.allowed
	if in.Definition.Key == "execution_read" {
		granted = h.index
	}
	if in.Definition.Key == "tool_result_read" {
		granted = h.reader
	}
	if in.Definition.Key == "large_operation" && h.revokeOnSourceRead {
		h.allowed = false
		h.revokeOnSourceRead = false
	}
	return agentsdk.ConversationToolAuthorization{Granted: granted && in.Authority.UserID == conversationAuthority().UserID, Revision: "live-result-policy"}, nil
}
func (h *resultHost) InvokeConversationTool(_ context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if in.Definition.Key != "large_operation" {
		return agentsdk.ConversationToolResult{}, fmt.Errorf("built-in result reader must run in the executor")
	}
	h.invokes++
	content, _ := json.Marshal(map[string]any{"body": h.payload, "open_items": []string{"仍需核对费用"}, "tail_evidence": "END-VERIFIED-83"})
	return agentsdk.ConversationToolResult{Status: "completed", ResourceID: "work-item-awaiting-review", Content: content}, nil
}
func (h *resultHost) ReconcileConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return agentsdk.ConversationToolResult{}, fmt.Errorf("unexpected reconcile")
}

func resultToolCall(name, id string, input any) agentsdk.ConversationStepResult {
	raw, _ := json.Marshal(input)
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: id, Name: name, Arguments: string(raw)}}}, FinishReason: "tool_calls"}
}

func TestExecutionCompactsStoredResultsAndResumesExactInput(t *testing.T) {
	repo := conversationRepository(t)
	host := &resultHost{allowed: true, reader: true, payload: strings.Repeat("完整内容\n", 7000)}
	model := &executionModel{}
	var frozen []byte
	var ref agentsdk.ConversationResultReference
	var assembled strings.Builder
	pages := 0
	native := json.RawMessage(`[{"type":"thinking","thinking":"opaque","signature":"keep-exact"}]`)
	model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		raw, _ := json.Marshal(in)
		if len(raw) > 32768 {
			t.Error("oversized model request")
		}
		if number == 1 {
			out := resultToolCall("large_operation", "large", map[string]string{"title": "保留未完成工作"})
			out.Message.Content, out.Message.ProviderState = "工作仍需复核。", native
			return out, nil
		}
		if number == 2 || number == 3 {
			last := in.Messages[len(in.Messages)-1]
			var preview struct {
				Representation  string                               `json:"representation"`
				Status          string                               `json:"status"`
				ResourceID      string                               `json:"resource_id"`
				Reference       agentsdk.ConversationResultReference `json:"reference"`
				ContentComplete bool                                 `json:"content_complete"`
				TotalBytes      int                                  `json:"total_bytes"`
			}
			if err := json.Unmarshal([]byte(last.Content), &preview); err != nil {
				t.Error(err)
			}
			previous := in.Messages[len(in.Messages)-2]
			if in.Compaction == nil || in.Compaction.AfterBytes != len(raw) || in.Compaction.BeforeBytes <= in.Compaction.AfterBytes || preview.Representation != "stored_result_preview" || preview.ContentComplete || preview.Status != "completed" || preview.ResourceID != "work-item-awaiting-review" || last.ToolCallID != "large" || previous.ToolCalls[0].ID != "large" || string(previous.ProviderState) != string(native) || previous.Content != "工作仍需复核。" {
				t.Error("compaction lost pairing/status/references/native state")
			}
			ref = preview.Reference
			if number == 2 {
				frozen = raw
				return agentsdk.ConversationStepResult{}, fmt.Errorf("injected model disconnect after compact input persisted")
			}
			if string(raw) != string(frozen) {
				t.Error("restart rebuilt a different compact input")
			}
			// Read a tail segment where the required evidence was omitted from
			// the initial preview. Byte pages preserve raw JSON including UTF-8.
			offset := preview.TotalBytes - 3500
			stored, err := repo.ConversationResult(t.Context(), ref, conversationAuthority())
			if err != nil {
				t.Error(err)
				return agentsdk.ConversationStepResult{}, err
			}
			full, _ := json.Marshal(stored.Result)
			for !utf8.RuneStart(full[offset]) {
				offset++
			}
			return resultToolCall("tool_result_read", "read-0", agentsdk.ConversationResultRead{Reference: ref, Offset: offset, MaxBytes: 1024}), nil
		}
		last := in.Messages[len(in.Messages)-1]
		var output agentsdk.ConversationToolResult
		if err := json.Unmarshal([]byte(last.Content), &output); err != nil || output.Status != "completed" {
			t.Errorf("read output failed: %s", last.Content)
		}
		var slice agentsdk.ConversationResultSlice
		if err := json.Unmarshal(output.Content, &slice); err != nil {
			t.Error(err)
		}
		if !utf8.ValidString(slice.JSONText) || slice.NextOffset-slice.Offset != len(slice.JSONText) || slice.Reference != ref {
			t.Error("slice lost UTF-8 boundary or immutable reference")
		}
		assembled.WriteString(slice.JSONText)
		pages++
		if !slice.Complete {
			return resultToolCall("tool_result_read", fmt.Sprintf("read-%d", pages), agentsdk.ConversationResultRead{Reference: ref, Offset: slice.NextOffset, MaxBytes: 1024}), nil
		}
		if !strings.Contains(assembled.String(), "END-VERIFIED-83") || !strings.Contains(assembled.String(), "仍需核对费用") {
			t.Error("omitted evidence could not be retrieved")
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "已核对完整结果中的尾部证据，费用仍待复核。"}, FinishReason: "stop"}, nil
	}
	options := application.ConversationOptions{ToolHost: host, ContextBytes: 32768, Poll: 5 * time.Millisecond}
	service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { service.Close() }()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "compact"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "处理事项，但费用仍待复核，不要声称已完成全部工作。"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	if stopped := waitConversation(t, service, c.ID, run.ID); stopped.Status != "failed" {
		t.Fatal("injected disconnect not observed")
	}
	service.Close()
	service, err = application.NewConversationService(repo, model, conversationAuthority().RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Resume(t.Context(), c.ID, run.ID, conversationAuthority()); err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, c.ID, run.ID)
	if final.Status != "completed" {
		t.Fatalf("resume failed: %+v", final)
	}
	host.mu.Lock()
	invokes := host.invokes
	host.mu.Unlock()
	if invokes != 1 || pages < 3 {
		t.Fatalf("invokes=%d pages=%d", invokes, pages)
	}
	events, err := service.Events(t.Context(), c.ID, run.ID, 0, 100, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	compactions := 0
	for _, event := range events.Items {
		if event.Type == "context.tools_compacted" {
			compactions++
		}
	}
	if compactions != 1 {
		t.Fatalf("compaction events=%d; replay must not produce a new event", compactions)
	}
	stored, err := repo.ConversationResult(t.Context(), ref, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	var content struct {
		Body string `json:"body"`
	}
	if json.Unmarshal(stored.Result.Content, &content) != nil || content.Body != host.payload {
		t.Fatal("compaction replaced full ledger content")
	}
}

func TestResultReadReauthorizesOriginalAndNestedSourcesBeforeModel(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested=%t", nested), func(t *testing.T) {
			repo := conversationRepository(t)
			host := &resultHost{allowed: true, reader: true, payload: strings.Repeat("private-data ", 4000)}
			var ref agentsdk.ConversationResultReference
			seed := func(inputRef *agentsdk.ConversationResultReference, id string) {
				t.Helper()
				model := &executionModel{}
				model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
					if number == 1 {
						if inputRef != nil {
							return resultToolCall("tool_result_read", "seed-read", agentsdk.ConversationResultRead{Reference: *inputRef, MaxBytes: 256}), nil
						}
						return resultToolCall("large_operation", "seed-source", map[string]string{"title": "source"}), nil
					}
					last := in.Messages[len(in.Messages)-1]
					if last.ResultReference == nil {
						return agentsdk.ConversationStepResult{}, fmt.Errorf("source reference missing")
					}
					ref = *last.ResultReference
					return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "已保存供后续核对。"}, FinishReason: "stop"}, nil
				}
				service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, Poll: 5 * time.Millisecond})
				if err != nil {
					t.Fatal(err)
				}
				defer service.Close()
				c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: id}, conversationAuthority())
				if err != nil {
					t.Fatal(err)
				}
				run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "seed", Message: "请保存结果"}, conversationAuthority())
				if err != nil {
					t.Fatal(err)
				}
				if final := waitConversation(t, service, c.ID, run.ID); final.Status != "completed" {
					t.Fatalf("seed failed: %s", final.ErrorCode)
				}
			}
			seed(nil, "source")
			if nested {
				original := ref
				seed(&original, "nested-source")
			}
			host.mu.Lock()
			host.revokeOnSourceRead = true
			host.mu.Unlock()
			model := &executionModel{}
			model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				if number > 1 {
					t.Error("revoked source reached the next model request")
				}
				return resultToolCall("tool_result_read", "read-private", agentsdk.ConversationResultRead{Reference: ref, MaxBytes: 256}), nil
			}
			service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, Poll: 5 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "new-conversation"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "read", Message: "核对之前的原始结果"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			final := waitConversation(t, service, c.ID, run.ID)
			if final.ErrorCode != "tool_access_denied" || len(final.Steps) == 0 || final.Steps[0].Calls[0].Status != "completed" {
				t.Fatalf("original-source reauthorization missing: %+v", final)
			}
			model.mu.Lock()
			calls := model.calls
			model.mu.Unlock()
			if calls != 1 {
				t.Fatalf("model requests=%d", calls)
			}
		})
	}
}

func TestResultReadRejectsStaleReferencesVersionsPermissionsAndUTF8Offsets(t *testing.T) {
	repo := conversationRepository(t)
	host := &resultHost{allowed: true, reader: true, payload: strings.Repeat("私有数据\n", 2000)}
	seedModel := &executionModel{}
	var ref agentsdk.ConversationResultReference
	seedModel.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number == 1 {
			return resultToolCall("large_operation", "source", map[string]string{"title": "资料"}), nil
		}
		ref = *in.Messages[len(in.Messages)-1].ResultReference
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "已保存结果。"}, FinishReason: "stop"}, nil
	}
	service, err := application.NewConversationService(repo, seedModel, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, Poll: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "reference-source"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "保存资料"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	if final := waitConversation(t, service, c.ID, run.ID); final.Status != "completed" {
		t.Fatal(final.ErrorCode)
	}
	service.Close()
	stored, err := repo.ConversationResult(t.Context(), ref, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	full, _ := json.Marshal(stored.Result)
	for _, tc := range []struct{ name, code string }{
		{"hash", "result_reference_changed"}, {"missing", "result_not_found"}, {"version", "tool_changed"}, {"permission", "tool_access_denied"}, {"utf8", "result_offset_invalid"}, {"past_end", "result_offset_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := agentsdk.ConversationResultRead{Reference: ref, MaxBytes: 256}
			host.mu.Lock()
			host.allowed = true
			host.version = "1"
			switch tc.name {
			case "hash":
				args.Reference.SHA256 = strings.Repeat("0", 64)
			case "missing":
				args.Reference.CallID = "does-not-exist"
			case "version":
				host.version = "2"
			case "permission":
				host.allowed = false
			case "utf8":
				args.Offset = strings.Index(string(full), "私有数据") + 1
			case "past_end":
				args.Offset = len(full) + 1
			}
			host.mu.Unlock()
			model := &executionModel{}
			model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				if number == 1 {
					return resultToolCall("tool_result_read", "read", args), nil
				}
				var result agentsdk.ConversationToolResult
				last := in.Messages[len(in.Messages)-1]
				if json.Unmarshal([]byte(last.Content), &result) != nil || result.Status != "failed" || result.ErrorCode != tc.code || strings.Contains(last.Content, "私有数据") {
					t.Errorf("unexpected protected result: %s", last.Content)
				}
				return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "本次未读取到资料。"}, FinishReason: "stop"}, nil
			}
			reader, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, Poll: 5 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			c, err := reader.Create(t.Context(), agentsdk.ConversationCreate{ClientID: tc.name}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			run, err := reader.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "read", Message: "核对资料"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			if final := waitConversation(t, reader, c.ID, run.ID); final.Status != "completed" {
				t.Fatalf("read error did not return safely: %s", final.ErrorCode)
			}
		})
	}
}

func TestLargeResultWithoutReaderPreservesLedgerAndFailsBudget(t *testing.T) {
	repo := conversationRepository(t)
	host := &resultHost{allowed: true, payload: strings.Repeat("large ", 10000)}
	model := &executionModel{}
	model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number > 1 {
			t.Error("inaccessible truncated result reached model")
		}
		return resultToolCall("large_operation", "large", map[string]string{"title": "source"}), nil
	}
	service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, ContextBytes: 32768, Poll: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "without-reader"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "处理资料"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, c.ID, run.ID)
	if final.ErrorCode != "execution_context_exceeded" || final.Steps[0].Calls[0].Status != "completed" {
		t.Fatalf("lost completed operation: %+v", final)
	}
	host.mu.Lock()
	invokes := host.invokes
	host.mu.Unlock()
	if invokes != 1 {
		t.Fatalf("invokes=%d", invokes)
	}
}

type resultSummaryModel struct {
	*executionModel
	summaries int
}

func (m *resultSummaryModel) GenerateConversation(_ context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	if in.Purpose != "summary" {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("unexpected non-summary text call")
	}
	m.mu.Lock()
	m.summaries++
	m.mu.Unlock()
	return agentsdk.ConversationModelResult{Content: `{"goal":"整理资料","constraints":["费用仍待核对"],"facts":["work-item-awaiting-review"],"decisions":[],"open_items":["核对费用"]}`, Model: "summary-fixture"}, nil
}

func TestHistoryCompactionReservesToolCatalogAndStepFraming(t *testing.T) {
	repo := conversationRepository(t)
	host := &resultHost{allowed: true, reader: true, payload: "small result"}
	model := &resultSummaryModel{executionModel: &executionModel{}}
	model.step = func(_ int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		raw, _ := json.Marshal(in)
		if len(raw) > 16384 {
			t.Errorf("tool catalog was omitted from context budgeting: %d", len(raw))
		}
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "user" {
			return resultToolCall("large_operation", "work", map[string]string{"title": "整理资料"}), nil
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: strings.Repeat("资料整理中。", 350)}, FinishReason: "stop"}, nil
	}
	service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, ContextBytes: 16384, MaxInputBytes: 512, SummaryBytes: 512, Poll: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "catalog-reserve"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: fmt.Sprint(i), Message: "继续整理 work-item-awaiting-review；费用仍待核对。"}, conversationAuthority())
		if err != nil {
			t.Fatal(err)
		}
		if final := waitConversation(t, service, c.ID, run.ID); final.Status != "completed" {
			t.Fatalf("round %d failed: %s", i, final.ErrorCode)
		}
	}
	model.mu.Lock()
	summaries := model.summaries
	model.mu.Unlock()
	if summaries == 0 {
		t.Fatal("test never required historical compaction")
	}
	page, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, conversationAuthority())
	if err != nil || len(page.Items) != 12 {
		t.Fatal("history originals were lost", err)
	}
}

func TestResultBytePagesReconstructCompleteJSON(t *testing.T) {
	repo := conversationRepository(t)
	host := &resultHost{allowed: true, reader: true, payload: strings.Repeat("文\n\"\\", 1500)}
	model := &executionModel{}
	var ref agentsdk.ConversationResultReference
	var joined strings.Builder
	next, pages, reductions := 0, 0, 0
	model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if in.Compaction != nil {
			reductions++
		}
		if number == 1 {
			return resultToolCall("large_operation", "all-source", map[string]string{"title": "完整分页"}), nil
		}
		last := in.Messages[len(in.Messages)-1]
		if number == 2 {
			if last.ResultReference == nil {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("missing result reference")
			}
			ref = *last.ResultReference
		} else {
			var result agentsdk.ConversationToolResult
			var slice agentsdk.ConversationResultSlice
			if json.Unmarshal([]byte(last.Content), &result) != nil || json.Unmarshal(result.Content, &slice) != nil || result.Status != "completed" || slice.Offset != next || slice.NextOffset <= next || slice.Reference != ref {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("invalid page sequence")
			}
			joined.WriteString(slice.JSONText)
			next = slice.NextOffset
			pages++
			if slice.Complete {
				var reconstructed agentsdk.ConversationToolResult
				var content struct {
					Body string `json:"body"`
				}
				if next != slice.TotalBytes || json.Unmarshal([]byte(joined.String()), &reconstructed) != nil || json.Unmarshal(reconstructed.Content, &content) != nil || content.Body != host.payload || reconstructed.ResourceID != "work-item-awaiting-review" {
					return agentsdk.ConversationStepResult{}, fmt.Errorf("complete JSON did not round-trip exactly")
				}
				return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "已逐页核对保存的完整结果。"}, FinishReason: "stop"}, nil
			}
		}
		return resultToolCall("tool_result_read", fmt.Sprintf("all-page-%d", pages), agentsdk.ConversationResultRead{Reference: ref, Offset: next, MaxBytes: 4096}), nil
	}
	service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, ContextBytes: 32768, MaxSteps: 32, MaxToolCalls: 32, Poll: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "all-pages"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "read", Message: "核对完整结果"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	if final := waitConversation(t, service, c.ID, run.ID); final.Status != "completed" {
		t.Fatalf("full reconstruction failed: %s", final.ErrorCode)
	}
	if pages < 3 {
		t.Fatal("test did not span enough byte pages")
	}
	if reductions < 2 {
		t.Fatal("test did not exercise repeated compaction while reading pages")
	}
}
