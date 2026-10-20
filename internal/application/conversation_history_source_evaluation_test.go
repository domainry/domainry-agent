package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type modelHistorySourceGraph struct {
	privatePeerSources
	snapshots map[string]persistence.ConversationSourceSnapshot
	reads     int
}

func (r *modelHistorySourceGraph) ConversationSourceSnapshot(_ context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	r.reads++
	snapshot, exists := r.snapshots[ref.RunID]
	if !exists || snapshot.Authority != a || snapshot.Run.ConversationID != ref.ConversationID {
		return persistence.ConversationSourceSnapshot{}, conversationFailure("forbidden", "history_owner_denied")
	}
	return snapshot, nil
}

func modelHistorySourceFixture(count int) (*ConversationService, *modelHistorySourceGraph, *productSourceReadProbe, sdk.ConversationAuthority, []sdk.ConversationMessage) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", RoleKey: "reader"}
	definition := sdk.ConversationToolDefinition{Key: "crm_fixture_read", Version: "1", Effect: "read"}
	host := &productSourceReadProbe{}
	repo := &modelHistorySourceGraph{snapshots: map[string]persistence.ConversationSourceSnapshot{}}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{ToolHost: host, ToolDefinitions: []sdk.ConversationToolDefinition{definition}, BeginSourceAuthorizationScope: func(ctx context.Context) (context.Context, func() error) { return ctx, func() error { return nil } }}}
	history := []sdk.ConversationMessage{}
	sources := []sdk.ConversationRunReference{}
	for i := 0; i < count; i++ {
		runID := fmt.Sprintf("run-%d", i)
		ref := sdk.ConversationRunReference{ConversationID: "conversation", RunID: runID}
		call := persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: runID, Name: definition.Key, Arguments: `{}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: []byte(`{"value":1}`)}}
		repo.snapshots[runID] = persistence.ConversationSourceSnapshot{Authority: a, Run: sdk.ConversationRun{ID: runID, ConversationID: ref.ConversationID}, Input: &sdk.ConversationModelRequest{Sources: &sdk.ConversationSources{Version: 1, Runs: append([]sdk.ConversationRunReference(nil), sources...)}}, Calls: []persistence.ConversationToolExecution{call}}
		history = append(history, sdk.ConversationMessage{Role: "user", Content: "user correction"}, sdk.ConversationMessage{Role: "assistant", Content: "saved answer", RunID: runID, ConversationID: ref.ConversationID})
		sources = append(sources, ref)
	}
	return s, repo, host, a, history
}

func TestModelHistoryPreservesSnapshotsWithoutReopeningBusinessSources(t *testing.T) {
	const count = 12
	s, repo, host, _, history := modelHistorySourceFixture(count)
	starts, finishes := 0, 0
	s.options.BeginSourceAuthorizationScope = func(ctx context.Context) (context.Context, func() error) {
		starts++
		return ctx, func() error { finishes++; return nil }
	}
	host.err = errors.New("current record permission revoked")
	roots, err := s.modelHistorySources(t.Context(), history)
	if err != nil || starts != 1 || finishes != 1 || host.results != 0 || repo.reads != 0 {
		t.Fatalf("repeated historical checks: err=%v starts=%d finishes=%d results=%d snapshots=%d", err, starts, finishes, host.results, repo.reads)
	}
	for i, message := range history {
		if message.Role == "user" && message.Content != "user correction" {
			t.Fatal("original user message changed")
		}
		if message.Role == "assistant" && (message.Content != "saved answer" || message.AccessError != "" || len(roots[i]) != 1 || roots[i][0].RunID != message.RunID) {
			t.Fatal("historical snapshot or direct trace locator changed", i)
		}
	}
	_, err = s.modelHistorySources(t.Context(), history)
	if err != nil || starts != 2 || finishes != 2 || host.results != 0 || repo.reads != 0 {
		t.Fatal("next input preparation reopened historical sources", err, starts, finishes, host.results, repo.reads)
	}
}

func TestModelHistoryFinalOwnerChangeRejectsTheWholeProjection(t *testing.T) {
	s, _, _, _, history := modelHistorySourceFixture(3)
	denied := errors.New("owner revision changed before model input release")
	s.options.BeginSourceAuthorizationScope = func(ctx context.Context) (context.Context, func() error) { return ctx, func() error { return denied } }
	roots, err := s.modelHistorySources(t.Context(), history)
	if !errors.Is(err, denied) || roots != nil {
		t.Fatal("late owner denial released model input", roots, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.modelHistorySources(ctx, history); !errors.Is(err, context.Canceled) {
		t.Fatal("history assembly ignored cancellation", err)
	}
}
