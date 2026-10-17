package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

type runPlannerStub struct {
	request agentsdk.ConversationRunPlanningRequest
}

func (p *runPlannerStub) PlanConversationRun(_ context.Context, request agentsdk.ConversationRunPlanningRequest) (agentsdk.ConversationRunPlan, error) {
	p.request = request
	request.Input.Messages[0].Content = "planner mutation"
	request.Tools[0].InputSchema[0] = '['
	return agentsdk.ConversationRunPlan{Key: "custom_plan", Label: "äº§åè®¡å", Steps: []agentsdk.ConversationRunPlanStep{{Key: "understand", Label: "çè§£è¯·æ±", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}}}}, nil
}

type runPlannerToolHost struct {
	definition agentsdk.ConversationToolDefinition
}

func (h runPlannerToolHost) ConversationTools(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	return []agentsdk.ConversationToolDefinition{h.definition}, nil
}
func (runPlannerToolHost) AuthorizeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: true}, nil
}
func (runPlannerToolHost) InvokeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return agentsdk.ConversationToolResult{}, nil
}
func (runPlannerToolHost) ReconcileConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return agentsdk.ConversationToolResult{}, nil
}

func TestConversationRunPlannerIsProductReplaceableAndReceivesOwnedCopies(t *testing.T) {
	definition := agentsdk.ConversationToolDefinition{
		Key: "lookup", Version: "1", Description: "Read data", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`), ActionKey: "test.lookup", Effect: "read", Idempotency: "natural", TimeoutMillis: 1000, MaxOutputBytes: 1024,
	}
	planner := &runPlannerStub{}
	service := &ConversationService{options: ConversationOptions{ToolHost: runPlannerToolHost{definition: definition}, RunPlanner: planner}}
	input := agentsdk.ConversationModelRequest{Messages: []agentsdk.ConversationModelMessage{{Role: "system", Content: "product policy"}, {Role: "user", Content: "do it"}}}
	claim := persistence.ConversationClaim{Authority: agentsdk.ConversationAuthority{Known: true}, Run: agentsdk.ConversationRun{ID: "run", ConversationID: "conversation", Attempt: 2}}
	plan, err := service.generateConversationRunPlan(t.Context(), claim, input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Key != "custom_plan" || planner.request.RunID != "run" || planner.request.Attempt != 2 || len(planner.request.Tools) != 1 {
		t.Fatalf("custom planner request/result mismatch: request=%+v plan=%+v", planner.request, plan)
	}
	if input.Messages[0].Content != "product policy" || definition.InputSchema[0] != '{' {
		t.Fatal("custom planner mutated execution-owned input")
	}
}

func TestDefaultConversationRunPlannerKeepsProductContext(t *testing.T) {
	input := agentsdk.ConversationModelRequest{Messages: []agentsdk.ConversationModelMessage{
		{Role: "system", Content: "aurora product policy"},
		{Role: "system", Content: "authorized record context", ContextSourceKey: "record.current"},
		{Role: "user", Content: "follow up this account"},
	}, Context: &agentsdk.ConversationContextManifest{Sources: []agentsdk.ConversationContextSourceReference{{Key: "record.current", MessageIndex: 1}}}}
	planning, err := defaultConversationRunPlanningInput(input, []byte(`[{"key":"lookup"}]`), "run-1", 32768)
	if err != nil {
		t.Fatal(err)
	}
	if len(planning.Messages) != 4 || planning.Messages[0].Content != "aurora product policy" || planning.Messages[1].Content != "authorized record context" {
		t.Fatalf("product context was not preserved: %+v", planning.Messages)
	}
	var snapshot struct {
		Conversation []agentsdk.ConversationModelMessage `json:"conversation"`
	}
	if planning.Messages[3].Role != "user" || json.Unmarshal([]byte(planning.Messages[3].Content), &snapshot) != nil || len(snapshot.Conversation) != 1 || snapshot.Conversation[0].Content != "follow up this account" {
		t.Fatalf("current request must be planning data, not a live chat turn: %+v", planning.Messages)
	}
	if planning.Context.Sources[0].MessageIndex != 1 || planning.Messages[1].ContextSourceKey != planning.Context.Sources[0].Key {
		t.Fatal("planning changed authorized context-source positions")
	}
	if planning.Messages[2].Role != "system" || planning.Messages[2].ContextSourceKey != "" || !strings.Contains(planning.Messages[2].Content, "not this planning response") || !strings.Contains(planning.Messages[2].Content, `"key":"lookup"`) {
		t.Fatal("planning output contract must follow product/context systems without replacing their content")
	}
	if input.Messages[0].Content != "aurora product policy" || planning.Purpose != "summary" || planning.IdempotencyKey != "conversation:run-1:plan" || planning.MaxOutputBytes != 16*1024 {
		t.Fatalf("planning input mismatch: input=%+v planning=%+v", input, planning)
	}
}

func TestDefaultConversationRunPlannerProjectsAttachmentsToMetadataWithoutMutatingExecutionInput(t *testing.T) {
	input := agentsdk.ConversationModelRequest{Messages: []agentsdk.ConversationModelMessage{
		{Role: "system", Content: "product policy"},
		{Role: "user", Content: "process the attachment", ContentBlocks: []agentsdk.ConversationContentBlock{
			{Type: "text", Text: "process the attachment"},
			{Type: "image", Image: &agentsdk.ConversationImageReference{AttachmentID: "att-secret", Filename: "card.png", ContentType: "image/png", Bytes: 258205, SHA256: "secret-hash", Data: []byte("expensive-image-bytes")}},
			{Type: "file", File: &agentsdk.ConversationFileReference{AttachmentID: "file-secret", Filename: "brief.pdf", ContentType: "application/pdf", Bytes: 4096, SHA256: "secret-file-hash", Data: []byte("expensive-file-bytes")}},
		}},
	}}
	planning, err := defaultConversationRunPlanningInput(input, []byte(`[]`), "run-attachments", 32768)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Conversation []agentsdk.ConversationModelMessage `json:"conversation"`
	}
	if json.Unmarshal([]byte(planning.Messages[2].Content), &snapshot) != nil || len(snapshot.Conversation) != 1 {
		t.Fatal("planning did not wrap attachment context as data")
	}
	blocks := snapshot.Conversation[0].ContentBlocks
	if len(blocks) != 3 || blocks[0].Text != "process the attachment" || blocks[1].Type != "text" || !strings.Contains(blocks[1].Text, "card.png") || blocks[1].Image != nil || blocks[2].Type != "text" || !strings.Contains(blocks[2].Text, "brief.pdf") || blocks[2].File != nil {
		t.Fatalf("planning attachment projection mismatch: %+v", blocks)
	}
	if strings.Contains(blocks[1].Text, "att-secret") || strings.Contains(blocks[1].Text, "secret-hash") || strings.Contains(blocks[2].Text, "file-secret") || strings.Contains(blocks[2].Text, "secret-file-hash") {
		t.Fatalf("planner received private attachment identity: %+v", blocks)
	}
	if snapshot.Conversation[0].Content != blocks[0].Text+blocks[1].Text+blocks[2].Text {
		t.Fatalf("planning content and text blocks diverged: content=%q blocks=%+v", snapshot.Conversation[0].Content, blocks)
	}
	if string(input.Messages[1].ContentBlocks[1].Image.Data) != "expensive-image-bytes" || string(input.Messages[1].ContentBlocks[2].File.Data) != "expensive-file-bytes" {
		t.Fatal("planning projection mutated execution-owned attachment bytes")
	}
}

func TestDefaultConversationRunPlannerSeparatesHistoryFromLiveResponseInstructions(t *testing.T) {
	input := agentsdk.ConversationModelRequest{Messages: []agentsdk.ConversationModelMessage{
		{Role: "system", Content: "authorized product policy"},
		{Role: "user", Content: "analyze the image"},
		{Role: "assistant", Content: "earlier image facts"},
		{Role: "user", Content: "Write three paragraphs, about 400 words. Do not query or modify CRM."},
	}}
	planning, err := defaultConversationRunPlanningInput(input, []byte(`[]`), "run", 32768)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Conversation []agentsdk.ConversationModelMessage `json:"conversation"`
	}
	if len(planning.Messages) != 3 || json.Unmarshal([]byte(planning.Messages[2].Content), &snapshot) != nil || len(snapshot.Conversation) != 3 || snapshot.Conversation[2].Content != input.Messages[3].Content {
		t.Fatalf("conversation facts and current request were not preserved as planning data: %+v", planning)
	}
	if !strings.Contains(planning.Messages[1].Content, "120 Unicode characters") || !strings.Contains(planning.Messages[1].Content, "do not plan attachment_read") || input.Messages[3].Content != snapshot.Conversation[2].Content {
		t.Fatal("planner lacks bounded response/attachment contract or mutated execution input")
	}
}

func TestConversationRunPlanValidationKeepsSafeDiagnosticWithoutSourceContent(t *testing.T) {
	_, err := expandConversationRunPlan(conversationPlanDraft{Label: "plan", Steps: []conversationPlanDraftStep{
		{Label: "analyze", Detail: strings.Repeat("private-source-data", 100), Kind: "model"},
	}}, nil)
	var invalid *conversationRunPlanValidationError
	if !errors.As(err, &invalid) || invalid.reason != "step_text" || strings.Contains(err.Error(), "private-source-data") || conversationModelFailureCode(err, "failed") != "run_plan_model_invalid" {
		t.Fatalf("invalid plan lost its safe reason or stable error code: %v", err)
	}
}

type runProgressRepository struct {
	persistence.ConversationRepository
	events []map[string]any
}

func (r *runProgressRepository) AppendEvent(_ context.Context, _ persistence.ConversationClaim, kind string, data map[string]any) error {
	if kind == "run.progress" {
		r.events = append(r.events, data)
	}
	return nil
}

func TestExpandConversationRunPlanUsesGenericToolMetadata(t *testing.T) {
	definitions := map[string]agentsdk.ConversationToolDefinition{
		"lookup": {Key: "lookup", Effect: "read", ExecutionPlan: []toolsdk.ProgressStep{{Key: "find", Label: "æ¥è¯¢è®°å½", Detail: "è¯»åå¹éè®°å½"}, {Key: "compare", Label: "æ ¸å¯¹ç»æ", Detail: "æ¯è¾åéé¡¹", Optional: true}}},
		"send":   {Key: "send", Effect: "write"},
	}
	plan, err := expandConversationRunPlan(conversationPlanDraft{Label: "å¤çè¯·æ±", Steps: []conversationPlanDraftStep{
		{Label: "çè§£è¯·æ±", Detail: "ç¡®å®æ§è¡èå´", Kind: "model"},
		{Label: "æ¥æ¾ä¿¡æ¯", Detail: "è¯»åå½åæ°æ®", Kind: "tool", Tool: "lookup"},
		{Label: "åéç»æ", Detail: "ç¡®è®¤åååº", Kind: "tool", Tool: "send"},
		{Label: "æ±æ»ç»æ", Detail: "è¿åçå®æ§è¡ç»æ", Kind: "model"},
	}}, definitions)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 5 || plan.Steps[1].Binding.Kind != "tool_progress" || plan.Steps[1].Binding.ProgressKey != "find" || plan.Steps[2].Binding.ProgressKey != "compare" || !plan.Steps[2].Optional || plan.Steps[3].Binding.Kind != "tool" || plan.Steps[3].Binding.Tools[0] != "send" {
		t.Fatalf("generic tool metadata was not expanded: %+v", plan.Steps)
	}
}

func TestExpandConversationRunPlanRejectsUnknownToolAndAllowsPlannedRepeatedCalls(t *testing.T) {
	definitions := map[string]agentsdk.ConversationToolDefinition{"lookup": {Key: "lookup", Effect: "read"}}
	unknown := []conversationPlanDraftStep{{Label: "çè§£", Kind: "model"}, {Label: "æªç¥", Kind: "tool", Tool: "missing"}, {Label: "æ±æ»", Kind: "model"}}
	if _, err := expandConversationRunPlan(conversationPlanDraft{Label: "è®¡å", Steps: unknown}, definitions); err == nil {
		t.Fatalf("unknown tool plan was accepted: %+v", unknown)
	}
	repeated := []conversationPlanDraftStep{{Label: "çè§£", Kind: "model"}, {Label: "æ¥è¯¢ç¬¬ä¸ç»", Kind: "tool", Tool: "lookup"}, {Label: "æ¥è¯¢ç¬¬äºç»", Kind: "tool", Tool: "lookup"}, {Label: "æ±æ»", Kind: "model"}}
	plan, err := expandConversationRunPlan(conversationPlanDraft{Label: "è®¡å", Steps: repeated}, definitions)
	if err != nil || len(plan.Steps) != 4 || plan.Steps[1].Binding.Tools[0] != "lookup" || plan.Steps[2].Binding.Tools[0] != "lookup" {
		t.Fatalf("planned repeated calls were not preserved: plan=%+v err=%v", plan, err)
	}
}

func TestConversationRunProgressBindsRepeatedToolCallsToDistinctSteps(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "first", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"lookup"}}},
		{Key: "second", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"lookup"}}},
	}}
	repo := &runProgressRepository{}
	service := &ConversationService{repo: repo}
	claim := persistence.ConversationClaim{Run: agentsdk.ConversationRun{Plan: plan}}
	for _, update := range []struct{ callID, status string }{{"call-1", "started"}, {"call-1", "completed"}, {"call-2", "started"}, {"call-2", "completed"}} {
		if err := service.appendConversationRunProgress(t.Context(), claim, "tool", "lookup", "", update.callID, update.status, update.status); err != nil {
			t.Fatal(err)
		}
	}
	if plan.Steps[0].CallID != "call-1" || plan.Steps[0].Status != "completed" || plan.Steps[1].CallID != "call-2" || plan.Steps[1].Status != "completed" || len(repo.events) != 4 {
		t.Fatalf("repeated tool calls were not tracked independently: steps=%+v events=%+v", plan.Steps, repo.events)
	}
}

func TestConversationRunProgressKeepsWriteToolDetailsInsideTheAtomicPlanStep(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "send", Status: "started", CallID: "call-1", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"send"}}},
	}}
	repo := &runProgressRepository{}
	service := &ConversationService{repo: repo}
	claim := persistence.ConversationClaim{Run: agentsdk.ConversationRun{Plan: plan}}
	if err := service.appendConversationRunProgress(t.Context(), claim, "tool_progress", "send", "provider_call", "call-1", "started", "æ­£å¨è°ç¨å¤é¨æå¡"); err != nil {
		t.Fatal(err)
	}
	if plan.Steps[0].Status != "started" || len(repo.events) != 0 {
		t.Fatalf("inner progress changed the atomic run step: step=%+v events=%+v", plan.Steps[0], repo.events)
	}
}

func TestConversationRunProgressKeepsModelRetryOnTheActiveStep(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "understand", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}},
		{Key: "answer", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}},
	}}
	repo := &runProgressRepository{}
	service := &ConversationService{repo: repo}
	claim := persistence.ConversationClaim{Run: agentsdk.ConversationRun{Plan: plan}}
	for _, status := range []string{"started", "started", "completed", "started", "completed"} {
		if err := service.appendConversationRunProgress(t.Context(), claim, "model", "", "", "", status, status); err != nil {
			t.Fatal(err)
		}
	}
	if plan.Steps[0].Status != "completed" || plan.Steps[1].Status != "completed" || len(repo.events) != 4 {
		t.Fatalf("model retry advanced the wrong plan step: steps=%+v events=%+v", plan.Steps, repo.events)
	}
}

func TestConversationRunProgressDoesNotAdvanceFinalModelAcrossPendingRequiredTools(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "understand", Status: "completed", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}},
		{Key: "lookup", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"lookup"}}},
		{Key: "answer", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}},
	}}
	repo := &runProgressRepository{}
	service := &ConversationService{repo: repo}
	claim := persistence.ConversationClaim{Run: agentsdk.ConversationRun{Plan: plan}}
	for _, status := range []string{"started", "completed"} {
		if err := service.appendConversationRunProgress(t.Context(), claim, "model", "", "", "", status, status); err != nil {
			t.Fatal(err)
		}
	}
	if plan.Steps[2].Status != "planned" || len(repo.events) != 0 {
		t.Fatalf("intermediate model call advanced the final step: steps=%+v events=%+v", plan.Steps, repo.events)
	}
	for _, update := range []struct{ kind, tool, callID, status string }{
		{"tool", "lookup", "call-1", "started"},
		{"tool", "lookup", "call-1", "completed"},
		{"model", "", "", "started"},
		{"model", "", "", "completed"},
	} {
		if err := service.appendConversationRunProgress(t.Context(), claim, update.kind, update.tool, "", update.callID, update.status, update.status); err != nil {
			t.Fatal(err)
		}
	}
	if plan.Steps[1].Status != "completed" || plan.Steps[2].Status != "completed" || len(repo.events) != 4 {
		t.Fatalf("ordered tool and final model were not tracked: steps=%+v events=%+v", plan.Steps, repo.events)
	}
}

func TestConversationRunPlanContinuationRequiresOnlyNonOptionalToolWork(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "understand", Label: "çè§£è¯·æ±", Status: "completed", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}},
		{Key: "lookup", Label: "æ¥è¯¢ä»»å¡", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"lookup"}}},
		{Key: "optional", Label: "å¯éè¡¥å", Status: "planned", Optional: true, Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"lookup"}}},
		{Key: "answer", Label: "æ´çç»æ", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}},
	}}
	message, pending := conversationRunPlanContinuationMessage(plan)
	if !pending || message.Role != "system" || !strings.Contains(message.Content, "æ¥è¯¢ä»»å¡") || strings.Contains(message.Content, "å¯éè¡¥å") {
		t.Fatalf("continuation message mismatch: pending=%v message=%+v", pending, message)
	}
	plan.Steps[1].Status = "completed"
	if _, pending = conversationRunPlanContinuationMessage(plan); pending {
		t.Fatal("completed required work still requested continuation")
	}
}

func TestConversationRunPlanStateMessageCarriesCurrentGenericProgress(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "done", Label: "è¯»åç®å½", Status: "completed", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"catalog"}}},
		{Key: "next", Label: "è¯»åè®°å½", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"query"}}},
	}}
	message, ok := conversationRunPlanStateMessage(plan)
	if !ok || message.Role != "system" || !strings.Contains(message.Content, `"label":"è¯»åç®å½","status":"completed"`) || !strings.Contains(message.Content, `"label":"è¯»åè®°å½","status":"planned"`) || !strings.Contains(message.Content, "Never repeat a completed step") {
		t.Fatalf("plan state message mismatch: ok=%v message=%+v", ok, message)
	}
}

func TestConversationRunPlanControlStaysInLeadingSystemPrefixAndReplacesPriorState(t *testing.T) {
	messages := []agentsdk.ConversationStepMessage{
		{Role: "system", Content: "product policy", ContextSourceKey: "product.policy"},
		{Role: "system", Content: conversationRunPlanStatePrefix + " old state"},
		{Role: "user", Content: "do it"},
		{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "call-1", Name: "lookup", Arguments: `{}`}}},
		{Role: "tool", ToolCallID: "call-1", Content: `{}`},
	}
	control := agentsdk.ConversationStepMessage{Role: "system", Content: conversationRunPlanStatePrefix + " new state"}

	got := conversationMessagesWithRunPlanControl(messages, control)
	if len(got) != len(messages) || got[0].Content != "product policy" || got[1].Content != conversationRunPlanStatePrefix+" new state" || got[2].Role != "user" {
		t.Fatalf("run plan control was not replaced in the leading system prefix: %+v", got)
	}
	for index, message := range got {
		if index > 1 && message.Role == "system" {
			t.Fatalf("non-leading system message at %d: %+v", index, got)
		}
	}
}

func TestConversationRunProgressRejectsAnUnplannedRepeatWithoutChangingCompletedStep(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "lookup", Status: "completed", CallID: "call-1", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"lookup"}}},
	}}
	service := &ConversationService{repo: &runProgressRepository{}}
	claim := persistence.ConversationClaim{Run: agentsdk.ConversationRun{Plan: plan}}
	err := service.appendConversationRunProgress(t.Context(), claim, "tool", "lookup", "", "call-2", "started", "")
	if err == nil || !strings.Contains(err.Error(), "run_plan_progress_order") || plan.Steps[0].CallID != "call-1" || plan.Steps[0].Status != "completed" {
		t.Fatalf("unplanned repeat was not rejected safely: err=%v step=%+v", err, plan.Steps[0])
	}
}

func TestConversationRunProgressDoesNotSkipOptionalToolBeforeModelResultIsKnown(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "assess", Status: "completed", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}},
		{Key: "write", Status: "planned", Optional: true, Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"create"}}},
		{Key: "answer", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}},
	}}
	service := &ConversationService{repo: &runProgressRepository{}}
	claim := persistence.ConversationClaim{Run: agentsdk.ConversationRun{Plan: plan}}

	if err := service.appendConversationRunProgress(t.Context(), claim, "model", "", "", "", "started", ""); err != nil {
		t.Fatal(err)
	}
	if plan.Steps[1].Status != "planned" || plan.Steps[2].Status != "planned" {
		t.Fatalf("an optional tool was skipped before the model result was known: %+v", plan.Steps)
	}
}

func TestConversationRunPlanModelStopSkipsUnusedOptionalToolThenCompletesSummary(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "assess", Status: "completed", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}},
		{Key: "write", Status: "planned", Optional: true, Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"create"}}},
		{Key: "answer", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}},
	}}
	service := &ConversationService{repo: &runProgressRepository{}}
	claim := persistence.ConversationClaim{Run: agentsdk.ConversationRun{Plan: plan}}

	if err := service.finishConversationRunPlanForModelStop(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	if plan.Steps[1].Status != "skipped" || plan.Steps[2].Status != "completed" {
		t.Fatalf("terminal model result did not settle the remaining plan honestly: %+v", plan.Steps)
	}
}

func TestConversationRunProgressRejectsStartingARequiredStepOutOfOrderBeforePersistence(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "first", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"first"}}},
		{Key: "second", Status: "planned", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"second"}}},
	}}
	repo := &runProgressRepository{}
	service := &ConversationService{repo: repo}
	claim := persistence.ConversationClaim{Run: agentsdk.ConversationRun{Plan: plan}}
	err := service.appendConversationRunProgress(t.Context(), claim, "tool", "second", "", "call-2", "started", "")
	if err == nil || !strings.Contains(err.Error(), "run_plan_progress_order") || len(repo.events) != 0 || plan.Steps[1].Status != "planned" {
		t.Fatalf("out-of-order step reached persistence: err=%v steps=%+v events=%+v", err, plan.Steps, repo.events)
	}
}

func TestExpandConversationRunPlanRequiresExecutionOrderedModelBoundaries(t *testing.T) {
	definitions := map[string]agentsdk.ConversationToolDefinition{"lookup": {Key: "lookup", Effect: "read"}}
	for _, steps := range [][]conversationPlanDraftStep{
		{{Label: "æ¥è¯¢", Kind: "tool", Tool: "lookup"}, {Label: "æ±æ»", Kind: "model"}},
		{{Label: "çè§£", Kind: "model"}, {Label: "æ¥è¯¢", Kind: "tool", Tool: "lookup"}},
	} {
		if _, err := expandConversationRunPlan(conversationPlanDraft{Label: "è®¡å", Steps: steps}, definitions); err == nil {
			t.Fatalf("out-of-order plan was accepted: %+v", steps)
		}
	}
}

func TestConversationRunPlanTerminalUpdatesRejectIncompleteSuccess(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "understand", Status: "completed"},
		{Key: "lookup", Status: "failed"},
		{Key: "decide", Status: "planned"},
		{Key: "review", Status: "planned", Optional: true},
		{Key: "save", Status: "planned"},
	}}
	updates, code := conversationRunPlanTerminalUpdates(plan, "")
	if code != "run_plan_failed" {
		t.Fatalf("failed plan was allowed to complete: %q", code)
	}
	if len(updates) != 3 {
		t.Fatalf("remaining steps were not closed: %+v", updates)
	}
	for _, update := range updates {
		if update.status != "skipped" {
			t.Fatalf("unstarted step should be explicitly skipped: %+v", update)
		}
	}
}

func TestConversationRunPlanTerminalUpdatesSkipUnusedOptionalStep(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "understand", Status: "completed"},
		{Key: "review", Status: "planned", Optional: true},
		{Key: "save", Status: "completed"},
	}}
	updates, code := conversationRunPlanTerminalUpdates(plan, "")
	if code != "" || len(updates) != 1 || updates[0].key != "review" || updates[0].status != "skipped" {
		t.Fatalf("optional terminalization mismatch: code=%q updates=%+v", code, updates)
	}
}

func TestConversationRunPlanTerminalUpdatesFailActiveStepOnRunFailure(t *testing.T) {
	plan := &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "understand", Status: "started", CallID: "call-1"},
		{Key: "lookup", Status: "planned"},
	}}
	updates, code := conversationRunPlanTerminalUpdates(plan, "provider_failed")
	if code != "provider_failed" || len(updates) != 2 || updates[0].status != "failed" || updates[0].callID != "call-1" || updates[1].status != "skipped" {
		t.Fatalf("failed run did not close its plan: code=%q updates=%+v", code, updates)
	}
}
