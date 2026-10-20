package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestSplitExecutionStorageKeepsExactRecoveryAndReusesCatalogByContent(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	a := conversationTestAuthority()
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "split-storage"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "split-input", Message: "Check customer"}, a); err != nil {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "split-worker", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	in := executionStoreInput()
	in.Messages = append(in.Messages, sdk.ConversationStepMessage{Role: "user", Content: strings.Repeat("unrelated-history-text", 4096)})
	in.ContextSources = []sdk.ConversationRunReference{{ConversationID: "source-conversation", RunID: "source-run", BeforeStep: 2}}
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &in); err != nil {
		t.Fatal(err)
	}
	var before []byte
	if err = store.Database().QueryRowContext(t.Context(), `SELECT payload_json FROM _agent_step_inputs WHERE run_id=? AND step_no=0`, claim.Run.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), `"input_schema"`) || strings.Contains(string(before), "source-run") {
		t.Fatal("catalog or source edges were duplicated in the frozen transcript")
	}
	// A result may not update any immutable input/catalog row, even incidentally.
	for _, table := range []string{conversationStepInputTable, conversationToolCatalogTable, conversationStepSourceTable} {
		if _, err = store.Database().ExecContext(t.Context(), `CREATE TRIGGER no_update_`+table+` BEFORE UPDATE ON `+table+` BEGIN SELECT RAISE(ABORT, 'immutable snapshot rewritten'); END`); err != nil {
			t.Fatal(err)
		}
	}
	advance := func(number int) {
		t.Helper()
		call := sdk.ConversationToolCall{ID: "one", Name: "create_thing", Arguments: `{"name":"one"}`}
		if err := repo.CompleteExecutionStep(t.Context(), claim, number, sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}, ProviderState: json.RawMessage(`{"opaque":"preserved"}`)}}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := repo.BeginExecutionTool(t.Context(), claim, number, call.ID, sdk.ConversationToolAuthorization{Granted: true, Revision: "current-revision"}); err != nil {
			t.Fatal(err)
		}
		if err := repo.FinishExecutionTool(t.Context(), claim, number, call.ID, sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"saved"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	advance(0)
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 1, &in); err != nil {
		t.Fatal(err)
	}
	advance(1)
	changed := in
	changed.Tools = append([]sdk.ConversationToolDefinition(nil), in.Tools...)
	changed.Tools[0].Description = "Changed tool description"
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 2, &changed); err != nil {
		t.Fatal(err)
	}
	var catalogs int
	if err = store.Database().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_tool_catalogs`).Scan(&catalogs); err != nil || catalogs != 2 {
		t.Fatalf("catalogs=%d want 2 (unchanged two steps share one) err=%v", catalogs, err)
	}
	// New repository instance must restore the old catalog, not today's one.
	repo = newTestConversationStore(t, store)
	for number, expected := range []sdk.ConversationStepRequest{in, in, changed} {
		step, found, err := repo.ExecutionStep(t.Context(), claim, number, nil)
		if err != nil || !found || !reflect.DeepEqual(step.Input, expected) {
			t.Fatalf("exact frozen request %d changed on recovery: %v", number, err)
		}
	}
	var after []byte
	if err = store.Database().QueryRowContext(t.Context(), `SELECT payload_json FROM _agent_step_inputs WHERE run_id=? AND step_no=0`, claim.Run.ID).Scan(&after); err != nil || string(before) != string(after) {
		t.Fatal("result completion rewrote its frozen request", err)
	}
	var policy persistence.ConversationExecutionStep
	if _, err = repo.readExecutionStepPolicy(t.Context(), store.Database(), executionScope(claim, 0), &policy); err != nil || len(policy.Input.Messages) != 0 || len(policy.Input.Tools) != 1 || policy.Input.MaxToolCalls != in.MaxToolCalls {
		t.Fatal("hot completion still reads transcript or lost frozen execution limits", err)
	}
	snapshot, err := repo.ConversationSourceSnapshot(t.Context(), sdk.ConversationRunReference{ConversationID: c.ID, RunID: claim.Run.ID}, a)
	if err != nil || len(snapshot.StepSources) != 3 || len(snapshot.Steps) != 3 || len(snapshot.Steps[0].Input.Tools) != 0 || len(snapshot.Steps[0].Input.Messages) != 0 {
		t.Fatal("source auditor loaded unrelated transcripts/catalogs or lost edges", err)
	}
	// Corrupting the catalog cannot silently change a recovered request.
	if _, err = store.Database().ExecContext(t.Context(), `DROP TRIGGER no_update__agent_tool_catalogs`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Database().ExecContext(t.Context(), `UPDATE _agent_tool_catalogs SET payload_json='[]'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, nil); err == nil {
		t.Fatal("catalog tampering was accepted during recovery")
	}
}

func TestSplitSourceProjectionKeepsRegisteredHashesImagesAndModelRoots(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	a := conversationTestAuthority()
	c, _ := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "source-projection"}, a)
	_, _ = repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "projection", Message: "Check"}, a)
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "source-worker", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	message := sdk.ConversationStepMessage{Role: "system", Content: "registered current customer", ContextSourceKey: "customer"}
	manifest := &sdk.ConversationContextManifest{Version: 1, RefreshedAt: time.Now().UTC().Truncate(time.Millisecond), StablePrefixHash: strings.Repeat("a", 64), DynamicHash: strings.Repeat("b", 64), Sources: []sdk.ConversationContextSourceReference{{Key: "customer", MessageIndex: 1, MessageHash: conversationHash(message.Content), ContentHash: strings.Repeat("c", 64), DefinitionHash: strings.Repeat("d", 64), Version: "1", UpdatedAt: time.Now().UTC().Truncate(time.Millisecond)}}}
	model := sdk.ConversationModelRequest{Messages: []sdk.ConversationModelMessage{{Role: "user", Content: strings.Repeat("unrelated-model-history", 1000)}, {Role: "system", Content: message.Content}}, Context: manifest, Sources: &sdk.ConversationSources{Version: 1, Runs: []sdk.ConversationRunReference{{ConversationID: "earlier", RunID: "earlier-run", BeforeStep: 2}}}, Purpose: "reply"}
	if _, _, err = repo.ModelInput(t.Context(), claim, &model); err != nil {
		t.Fatal(err)
	}
	in := executionStoreInput()
	in.Messages = []sdk.ConversationStepMessage{{Role: "user", Content: strings.Repeat("unrelated-step-history", 1000)}, message}
	in.Context = manifest
	in.ContextWindow = &sdk.ConversationContextWindow{LimitBytes: 4096, InputBytes: 100, PressurePermille: 24}
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &in); err != nil {
		t.Fatal(err)
	}
	// Durable times are millisecond-precision. Replay compares that exact
	// representation, rather than an in-memory sub-millisecond timestamp.
	replay := in
	contextCopy := *manifest
	contextCopy.RefreshedAt = contextCopy.RefreshedAt.Add(99 * time.Nanosecond)
	replay.Context = &contextCopy
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &replay); err != nil {
		t.Fatal("same durable input rejected because of sub-millisecond time", err)
	}
	snapshot, err := repo.ConversationSourceSnapshot(t.Context(), sdk.ConversationRunReference{ConversationID: c.ID, RunID: claim.Run.ID}, a)
	if err != nil || len(snapshot.StepContexts) != 1 || snapshot.Input == nil || !reflect.DeepEqual(snapshot.Input.Sources, model.Sources) {
		t.Fatal("source roots/registered context lost", err)
	}
	ctx := snapshot.StepContexts[0]
	if len(ctx.Messages) != 2 || ctx.Messages[0].Content != "" || !reflect.DeepEqual(ctx.Messages[1], message) || !reflect.DeepEqual(ctx.Context, manifest) || snapshot.Input.Messages[0].Content != "" || snapshot.Input.Messages[1].Content != message.Content {
		t.Fatal("source projection changed original indexes, message hashes or manifests")
	}
	restored, found, err := newTestConversationStore(t, store).ModelInput(t.Context(), claim, nil)
	if err != nil || !found || !reflect.DeepEqual(restored, model) {
		t.Fatal("exact initial model request changed after source normalization", err)
	}
	// Projection must retain image references, including linked-source metadata.
	image := sdk.ConversationContentBlock{Type: "image", Image: &sdk.ConversationImageReference{AttachmentID: "image", ConversationID: c.ID, Source: &sdk.ConversationRunReference{ConversationID: "earlier", RunID: "earlier-run"}}}
	in.Messages = append(in.Messages, sdk.ConversationStepMessage{Role: "user", Content: "unrelated", ContentBlocks: []sdk.ConversationContentBlock{{Type: "text", Text: "unused"}, image}})
	projected, err := stepSourceContext(in).stepContext(0)
	if err != nil || len(projected.Messages) != 3 || projected.Messages[2].Content != "" || !reflect.DeepEqual(projected.Messages[2].ContentBlocks, []sdk.ConversationContentBlock{image}) {
		t.Fatal("image authorization reference was dropped", err)
	}
}
