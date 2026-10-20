package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type executionSnapshotRepository struct {
	privatePeerSources
	persistence.ConversationExecutionRepository
	previous    persistence.ConversationExecutionStep
	stored      []persistence.ConversationToolExecution
	exists      bool
	sourceReads int
}

func (r *executionSnapshotRepository) ConversationSourceSnapshot(context.Context, sdk.ConversationRunReference, sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	r.sourceReads++
	return persistence.ConversationSourceSnapshot{}, errors.New("historical business access revoked")
}
func (r *executionSnapshotRepository) ExecutionStep(context.Context, persistence.ConversationClaim, int, *sdk.ConversationStepRequest) (persistence.ConversationExecutionStep, bool, error) {
	return r.previous, r.exists, nil
}
func (r *executionSnapshotRepository) ExecutionTools(context.Context, persistence.ConversationClaim, int) ([]persistence.ConversationToolExecution, error) {
	return r.stored, nil
}

type executionSnapshotHost struct {
	sdk.ConversationToolHost
	definition   sdk.ConversationToolDefinition
	allowed      bool
	resultChecks int
}

func (h *executionSnapshotHost) ConversationTools(context.Context, sdk.ConversationAuthority) ([]sdk.ConversationToolDefinition, error) {
	if !h.allowed {
		return nil, nil
	}
	return []sdk.ConversationToolDefinition{h.definition}, nil
}
func (h *executionSnapshotHost) AuthorizeConversationToolResult(context.Context, sdk.ConversationToolRequest, sdk.ConversationToolResult) error {
	h.resultChecks++
	return errors.New("historical result access revoked")
}

func executionSnapshotFixture() (*ConversationService, *executionSnapshotRepository, *executionSnapshotHost, persistence.ConversationClaim, persistence.ConversationExecutionStep) {
	definition := sdk.PersonalConversationTools()[1]
	call := sdk.ConversationToolCall{ID: "saved-call", Name: definition.Key, Arguments: `{}`}
	record := persistence.ConversationToolExecution{Step: 0, State: "completed", Call: call, Definition: definition, Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"saved":"authorized observation"}`)}}
	r := &executionSnapshotRepository{exists: true, stored: []persistence.ConversationToolExecution{record}, previous: persistence.ConversationExecutionStep{Number: 0, Result: &sdk.ConversationStepResult{Message: sdk.ConversationStepMessage{ToolCalls: []sdk.ConversationToolCall{call}}}}}
	h := &executionSnapshotHost{definition: definition, allowed: true}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: r, options: ConversationOptions{ToolHost: h}}
	return s, r, h, persistence.ConversationClaim{Authority: a, Run: sdk.ConversationRun{ID: "run", ConversationID: "conversation"}}, persistence.ConversationExecutionStep{Number: 1, Input: sdk.ConversationStepRequest{Tools: []sdk.ConversationToolDefinition{definition}}}
}

func TestExecutionInputDoesNotReopenHistoricalResources(t *testing.T) {
	s, r, h, claim, step := executionSnapshotFixture()
	for range 3 {
		if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); err != nil {
			t.Fatal(err)
		}
	}
	if r.sourceReads != 0 || h.resultChecks != 0 {
		t.Fatal("historical authorization survived", r.sourceReads, h.resultChecks)
	}
}

func TestExecutionInputStillRejectsRevokedOrChangedToolCatalog(t *testing.T) {
	for _, mode := range []string{"revoked", "changed"} {
		t.Run(mode, func(t *testing.T) {
			s, r, h, claim, step := executionSnapshotFixture()
			code := "tool_access_denied"
			if mode == "revoked" {
				h.allowed = false
			} else {
				h.definition.Version = "changed"
				code = "tool_changed"
			}
			if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); err == nil || !strings.Contains(err.Error(), code) {
				t.Fatal("unsafe tool catalog accepted", err)
			}
			if r.sourceReads != 0 || h.resultChecks != 0 {
				t.Fatal("catalog check reopened history")
			}
		})
	}
}

func TestExecutionInputStillRejectsIncompleteOrMismatchedReceipts(t *testing.T) {
	for _, mode := range []string{"missing-step", "wrong-step", "missing-model-result", "missing-call", "missing-tool-result", "uncertain-call", "different-arguments", "different-tool-step"} {
		t.Run(mode, func(t *testing.T) {
			s, r, h, claim, step := executionSnapshotFixture()
			switch mode {
			case "missing-step":
				r.exists = false
			case "wrong-step":
				r.previous.Number = 1
			case "missing-model-result":
				r.previous.Result = nil
			case "missing-call":
				r.stored = nil
			case "missing-tool-result":
				r.stored[0].Result = nil
			case "uncertain-call":
				r.stored[0].State = "uncertain"
			case "different-arguments":
				r.stored[0].Call.Arguments = `{"changed":true}`
			case "different-tool-step":
				r.stored[0].Step = 2
			}
			if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); err == nil || !strings.Contains(err.Error(), "tool_result_invalid") {
				t.Fatal("corrupt receipt accepted", err)
			}
			if r.sourceReads != 0 || h.resultChecks != 0 {
				t.Fatal("receipt check reopened history")
			}
		})
	}
}

func TestExecutionInputOwnerChangeAndCancellationStillReject(t *testing.T) {
	s, _, _, claim, step := executionSnapshotFixture()
	denied := errors.New("identity changed during current read")
	s.options.BeginSourceAuthorizationScope = func(ctx context.Context) (context.Context, func() error) { return ctx, func() error { return denied } }
	if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); !errors.Is(err, denied) {
		t.Fatal("late identity denial ignored", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.reauthorizeExecutionInputs(ctx, claim, step); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled execution accepted", err)
	}
}
