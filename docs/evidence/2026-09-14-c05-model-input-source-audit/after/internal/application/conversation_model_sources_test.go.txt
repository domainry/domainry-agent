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

type modelSourceTestRepository struct {
	privatePeerSources
	persistence.ConversationExecutionRepository
	owner     sdk.ConversationRunReference
	snapshot  persistence.ConversationSourceSnapshot
	other     map[sdk.ConversationRunReference]persistence.ConversationSourceSnapshot
	previous  persistence.ConversationExecutionStep
	exists    bool
	stored    []persistence.ConversationToolExecution
	afterRead func()
}

func (r *modelSourceTestRepository) ConversationSourceSnapshot(_ context.Context, ref sdk.ConversationRunReference, _ sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	if ref != r.owner {
		if snapshot, ok := r.other[ref]; ok {
			return snapshot, nil
		}
		return persistence.ConversationSourceSnapshot{}, conversationFailure("not_found", "run_not_found")
	}
	if r.afterRead != nil {
		r.afterRead()
	}
	return r.snapshot, nil
}

func (r *modelSourceTestRepository) ExecutionStep(context.Context, persistence.ConversationClaim, int, *sdk.ConversationStepRequest) (persistence.ConversationExecutionStep, bool, error) {
	return r.previous, r.exists, nil
}

func (r *modelSourceTestRepository) ExecutionTools(context.Context, persistence.ConversationClaim, int) ([]persistence.ConversationToolExecution, error) {
	return r.stored, nil
}

type modelSourceTestPolicy struct {
	allowed []string
	checks  int
	inspect func(context.Context) error
}

func (p *modelSourceTestPolicy) AuthorizeConversationCollaboration(ctx context.Context, _ sdk.ConversationCollaborationAuthorizationRequest) (sdk.ConversationCollaborationAuthorization, error) {
	p.checks++
	if p.inspect != nil {
		if err := p.inspect(ctx); err != nil {
			return sdk.ConversationCollaborationAuthorization{}, err
		}
	}
	return sdk.ConversationCollaborationAuthorization{Allowed: append([]string(nil), p.allowed...), Revision: "current"}, nil
}

type modelSourceTestHost struct {
	sdk.ConversationToolHost
	definitions []sdk.ConversationToolDefinition
	concrete    func()
	result      func(context.Context, sdk.ConversationToolRequest, sdk.ConversationToolResult) error
	results     int
}

func (h *modelSourceTestHost) ConversationTools(context.Context, sdk.ConversationAuthority) ([]sdk.ConversationToolDefinition, error) {
	return h.definitions, nil
}

func (h *modelSourceTestHost) AuthorizeConversationTool(_ context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	if in.Call.Name != "" && h.concrete != nil {
		h.concrete()
	}
	return sdk.ConversationToolAuthorization{Granted: in.Definition.Key == "delegation_get"}, nil
}

func (h *modelSourceTestHost) AuthorizeConversationToolResult(ctx context.Context, in sdk.ConversationToolRequest, result sdk.ConversationToolResult) error {
	h.results++
	if h.result != nil {
		return h.result(ctx, in, result)
	}
	return nil
}

func modelSourceTestFixture(t *testing.T, native bool) (*ConversationService, *modelSourceTestRepository, *modelSourceTestPolicy, *modelSourceTestHost, persistence.ConversationClaim, persistence.ConversationExecutionStep) {
	t.Helper()
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", RoleKey: "current-role"}
	owner := sdk.ConversationRunReference{ConversationID: "current", RunID: "current-run"}
	definition, _ := collaborationTool("delegation_get")
	call := sdk.ConversationToolCall{ID: "get-call", Name: definition.Key, Arguments: `{"id":"released"}`}
	body, err := json.Marshal(sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "released"}, Messages: []sdk.ConversationAgentMessage{{ID: "message", Content: "original working text"}}})
	if err != nil {
		t.Fatal(err)
	}
	record := persistence.ConversationToolExecution{Step: 0, Call: call, Definition: definition, State: "completed", Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: "released", Content: body}}
	r := &modelSourceTestRepository{owner: owner, exists: true, stored: []persistence.ConversationToolExecution{record}, previous: persistence.ConversationExecutionStep{Number: 0, Result: &sdk.ConversationStepResult{Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}}}
	r.snapshot = persistence.ConversationSourceSnapshot{Authority: a, Run: sdk.ConversationRun{ID: owner.RunID, ConversationID: owner.ConversationID}, Calls: []persistence.ConversationToolExecution{record}}
	policy := &modelSourceTestPolicy{allowed: sdk.ConversationCollaborationOperations()}
	host := &modelSourceTestHost{}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: r, options: ConversationOptions{PersonalAuthorizer: host, CollaborationAuthorizer: policy}}
	if native {
		s.options.ToolHost = &collaborationToolHost{ConversationToolHost: host, service: s}
	} else {
		host.definitions = []sdk.ConversationToolDefinition{definition}
		s.options.ToolHost = host
	}
	claim := persistence.ConversationClaim{Authority: a, Run: r.snapshot.Run}
	step := persistence.ConversationExecutionStep{Number: 1, Input: sdk.ConversationStepRequest{Tools: []sdk.ConversationToolDefinition{definition}}}
	return s, r, policy, host, claim, step
}

func TestModelSourceReceiptIsFullyAuditedOnceWithNativeAndCustomHosts(t *testing.T) {
	for _, mode := range []string{"native", "custom-no-native-checker", "assembled-forwarder", "assembled-no-result-checker"} {
		t.Run(mode, func(t *testing.T) {
			s, _, policy, host, claim, step := modelSourceTestFixture(t, mode != "custom-no-native-checker")
			if mode == "assembled-forwarder" {
				native := s.options.ToolHost.(sdk.ConversationToolResultAuthorizer)
				forward := &modelSourceTestHost{definitions: step.Input.Tools, result: native.AuthorizeConversationToolResult}
				s.options.ToolHost = &extensionInteractionHost{ConversationToolHost: forward, original: s.options.ToolHost}
			}
			if mode == "assembled-no-result-checker" {
				s.options.ToolHost = &extensionInteractionHost{ConversationToolHost: &modelSourceCatalogOnlyHost{definition: step.Input.Tools[0]}, original: s.options.ToolHost}
			}
			if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); err != nil || policy.checks != 1 {
				t.Fatal("receipt source not fully checked exactly once", policy.checks, err)
			}
			if mode == "custom-no-native-checker" && host.results != 1 {
				t.Fatal("custom result policy was bypassed", host.results)
			}
			policy.allowed = []string{"view"}
			if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); !collaborationDenied(err) || policy.checks != 2 {
				t.Fatal("next model request reused a source decision after communication revocation", policy.checks, err)
			}
		})
	}
}

type modelSourceCatalogOnlyHost struct {
	sdk.ConversationToolHost
	definition sdk.ConversationToolDefinition
}

func (h *modelSourceCatalogOnlyHost) ConversationTools(context.Context, sdk.ConversationAuthority) ([]sdk.ConversationToolDefinition, error) {
	return []sdk.ConversationToolDefinition{h.definition}, nil
}

func (h *modelSourceCatalogOnlyHost) AuthorizeConversationTool(context.Context, sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	return sdk.ConversationToolAuthorization{Granted: true}, nil
}

func TestModelSourceReceiptChecksRevocationAfterInitialTraversal(t *testing.T) {
	for _, native := range []bool{true, false} {
		t.Run(map[bool]string{true: "native", false: "custom"}[native], func(t *testing.T) {
			s, _, policy, host, claim, step := modelSourceTestFixture(t, native)
			host.concrete = func() { policy.allowed = []string{"view"} }
			if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); !collaborationDenied(err) || policy.checks != 1 {
				t.Fatal("pre-model current source revocation was not checked", policy.checks, err)
			}
		})
	}
}

func TestModelSourceReceiptRetainsOriginalTraversalValuesAndFreshCancellation(t *testing.T) {
	s, r, policy, host, claim, step := modelSourceTestFixture(t, false)
	type valueKey struct{}
	policy.inspect = func(ctx context.Context) error {
		if ctx.Err() != nil || ctx.Value(valueKey{}) != "source-value" || ctx.Value(conversationSourceParentKey{}) != claim.Authority {
			return errors.New("original source values or fresh cancellation lost")
		}
		return nil
	}
	if err := s.reauthorizeExecutionInputs(context.WithValue(t.Context(), valueKey{}, "source-value"), claim, step); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	host.result = func(context.Context, sdk.ConversationToolRequest, sdk.ConversationToolResult) error {
		cancel()
		return nil
	}
	r.afterRead = nil
	if err := s.reauthorizeExecutionInputs(ctx, claim, step); err == nil {
		t.Fatal("cancelled current request reused an uncancelled traversal context")
	}
}

func TestModelSourceReceiptDoesNotDeferChangedOrUnrepresentedSourceCalls(t *testing.T) {
	for _, scenario := range []string{"changed-content", "orphan", "unknown-original-role", "different-original-role", "inherited-run"} {
		t.Run(scenario, func(t *testing.T) {
			s, r, policy, host, claim, step := modelSourceTestFixture(t, false)
			policy.allowed = []string{"view"}
			switch scenario {
			case "changed-content":
				result := *r.snapshot.Calls[0].Result
				result.Content = append(result.Content[:len(result.Content)-1:len(result.Content)-1], []byte(`,"revision":2}`)...)
				r.snapshot.Calls[0].Result = &result
			case "orphan":
				r.snapshot.Calls[0].Call.ID = "unrepresented-call"
			case "unknown-original-role":
				r.snapshot.Authority = sdk.ConversationAuthority{}
			case "different-original-role":
				r.snapshot.Authority.RoleKey = "original-role"
			case "inherited-run":
				ref := sdk.ConversationRunReference{ConversationID: "inherited", RunID: "inherited-run"}
				r.other = map[sdk.ConversationRunReference]persistence.ConversationSourceSnapshot{ref: r.snapshot}
				r.snapshot.Calls = nil
				r.snapshot.Input = &sdk.ConversationModelRequest{Sources: &sdk.ConversationSources{Version: 1, Runs: []sdk.ConversationRunReference{ref}}}
			}
			if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); !collaborationDenied(err) || policy.checks != 1 || host.results != 0 {
				t.Fatal("nonmatching source escaped initial traversal", policy.checks, host.results, err)
			}
		})
	}
}

func TestModelSourceReceiptRejectsIncompletePreviousExecutionBeforeModel(t *testing.T) {
	for _, scenario := range []string{"missing-step", "different-model-step", "missing-result", "missing-call", "missing-tool-result", "uncertain-call", "different-arguments", "different-step"} {
		t.Run(scenario, func(t *testing.T) {
			s, r, _, host, claim, step := modelSourceTestFixture(t, false)
			switch scenario {
			case "missing-step":
				r.exists = false
			case "different-model-step":
				r.previous.Number = 1
			case "missing-result":
				r.previous.Result = nil
			case "missing-call":
				r.stored = nil
			case "missing-tool-result":
				r.stored[0].Result = nil
			case "uncertain-call":
				r.stored[0].State = "uncertain"
			case "different-arguments":
				r.stored[0].Call.Arguments = `{"id":"another"}`
			case "different-step":
				r.stored[0].Step = 1
			}
			if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); err == nil || !strings.Contains(err.Error(), "tool_result_invalid") || host.results != 0 {
				t.Fatal("incomplete or mismatched model execution accepted", host.results, err)
			}
		})
	}
}

func TestModelSourceReceiptRechecksInheritedPrivateAttachment(t *testing.T) {
	for _, native := range []bool{true, false} {
		t.Run(map[bool]string{true: "native", false: "custom"}[native], func(t *testing.T) {
			s, r, _, _, claim, step := modelSourceTestFixture(t, native)
			ref := sdk.ConversationRunReference{ConversationID: "private-source", RunID: "private-run"}
			definition := sdk.AttachmentConversationTools()[0]
			r.other = map[sdk.ConversationRunReference]persistence.ConversationSourceSnapshot{ref: {Authority: claim.Authority, Run: sdk.ConversationRun{ID: ref.RunID, ConversationID: ref.ConversationID}, Calls: []persistence.ConversationToolExecution{{Definition: definition, Call: sdk.ConversationToolCall{ID: "private-read", Name: definition.Key, Arguments: `{}`}, State: "completed", Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"private":"contents"}`)}}}}}
			body, err := json.Marshal(sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "released"}, Messages: []sdk.ConversationAgentMessage{{ID: "message", Source: &ref}}})
			if err != nil {
				t.Fatal(err)
			}
			r.stored[0].Result.Content = body
			if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); err == nil || !strings.Contains(err.Error(), "attachment_conversation_mismatch") {
				t.Fatal("nested private attachment reached model input", err)
			}
		})
	}
}

func TestModelSourceReceiptCustomResultPolicyCannotBeBypassed(t *testing.T) {
	s, _, policy, host, claim, step := modelSourceTestFixture(t, false)
	denied := conversationFailure("forbidden", "custom_result_denied")
	host.result = func(context.Context, sdk.ConversationToolRequest, sdk.ConversationToolResult) error { return denied }
	if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); !errors.Is(err, denied) || policy.checks != 0 {
		t.Fatal("custom result denial bypassed", policy.checks, err)
	}
}

func TestModelSourceReceiptPreservesNativeRechecksWithinOneResultPolicy(t *testing.T) {
	s, _, policy, _, claim, step := modelSourceTestFixture(t, true)
	native := s.options.ToolHost.(sdk.ConversationToolResultAuthorizer)
	custom := &modelSourceTestHost{definitions: step.Input.Tools}
	custom.result = func(ctx context.Context, in sdk.ConversationToolRequest, result sdk.ConversationToolResult) error {
		if err := native.AuthorizeConversationToolResult(ctx, in, result); err != nil {
			return err
		}
		policy.allowed = []string{"view"}
		return native.AuthorizeConversationToolResult(ctx, in, result)
	}
	s.options.ToolHost = custom
	if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); !collaborationDenied(err) || policy.checks != 2 {
		t.Fatal("an explicit second check reused the first source decision", policy.checks, err)
	}
}

func TestModelSourceReceiptDoesNotDeferPrefixesChangedDefinitionsOrPendingResults(t *testing.T) {
	for _, scenario := range []string{"prefix", "definition", "state", "pending-result"} {
		t.Run(scenario, func(t *testing.T) {
			s, r, policy, _, claim, _ := modelSourceTestFixture(t, true)
			owner := r.owner
			switch scenario {
			case "prefix":
				owner.BeforeStep = 2
				r.owner = owner
			case "definition":
				r.snapshot.Calls[0].Definition.Version = "changed"
			case "state":
				r.snapshot.Calls[0].State = "uncertain"
			case "pending-result":
				result := *r.snapshot.Calls[0].Result
				result.Status = "pending"
				r.snapshot.Calls[0].Result = &result
			}
			pending, err := s.checkModelRunSources(t.Context(), owner, claim.Authority, r.stored)
			if err != nil || len(pending) != 0 || scenario != "pending-result" && policy.checks != 1 {
				t.Fatal("bounded or noncompleted receipt incorrectly postponed", len(pending), policy.checks, err)
			}
		})
	}
}

func TestModelSourceReceiptCannotSubstituteAnotherServiceSourceAudit(t *testing.T) {
	first, r, _, _, claim, _ := modelSourceTestFixture(t, true)
	second, _, policy, _, _, _ := modelSourceTestFixture(t, true)
	policy.allowed = []string{"view"}
	pending, err := first.checkModelRunSources(t.Context(), r.owner, claim.Authority, r.stored)
	if err != nil || len(pending) != 1 {
		t.Fatal("missing exact first-service receipt", len(pending), err)
	}
	record := r.stored[0]
	ctx := context.WithValue(t.Context(), conversationModelSourceRecordKey{}, pending[conversationRecordHash(record)])
	in := sdk.ConversationToolRequest{Authority: claim.Authority, ConversationID: r.owner.ConversationID, RunID: r.owner.RunID, Step: record.Step, Call: record.Call, Definition: record.Definition}
	if err := second.options.ToolHost.(sdk.ConversationToolResultAuthorizer).AuthorizeConversationToolResult(ctx, in, *record.Result); !collaborationDenied(err) || policy.checks != 1 {
		t.Fatal("native host audited another service's data instead of its own", policy.checks, err)
	}
}

func TestModelSourceReceiptRetainsStricterNativeSourcePurpose(t *testing.T) {
	s, _, policy, _, claim, step := modelSourceTestFixture(t, true)
	policy.allowed = []string{"view", "delivery_read"}
	native := s.options.ToolHost.(sdk.ConversationToolResultAuthorizer)
	custom := &modelSourceTestHost{definitions: step.Input.Tools}
	custom.result = func(ctx context.Context, in sdk.ConversationToolRequest, result sdk.ConversationToolResult) error {
		ctx = context.WithValue(ctx, conversationRawExecutionSourceKey{}, true)
		return native.AuthorizeConversationToolResult(ctx, in, result)
	}
	s.options.ToolHost = custom
	if err := s.reauthorizeExecutionInputs(deliverySourceContext(t.Context(), "released"), claim, step); !collaborationDenied(err) || policy.checks != 1 {
		t.Fatal("postponed delivery source proof replaced the native raw communication check", policy.checks, err)
	}
}

func TestModelSourceReceiptDiscardedNativeDenialInvalidatesCompletionMarker(t *testing.T) {
	s, _, policy, _, claim, step := modelSourceTestFixture(t, true)
	native := s.options.ToolHost.(sdk.ConversationToolResultAuthorizer)
	custom := &modelSourceTestHost{definitions: step.Input.Tools}
	custom.result = func(ctx context.Context, in sdk.ConversationToolRequest, result sdk.ConversationToolResult) error {
		if err := native.AuthorizeConversationToolResult(ctx, in, result); err != nil {
			return err
		}
		policy.allowed = []string{"view"}
		_ = native.AuthorizeConversationToolResult(ctx, in, result)
		return nil
	}
	s.options.ToolHost = custom
	if err := s.reauthorizeExecutionInputs(t.Context(), claim, step); !collaborationDenied(err) || policy.checks != 2 {
		t.Fatal("discarded later source denial retained an earlier completion marker", policy.checks, err)
	}
}

func TestModelSourceReceiptDiscardedStricterDenialCannotAuthorizeInput(t *testing.T) {
	s, _, policy, _, claim, step := modelSourceTestFixture(t, true)
	policy.allowed = []string{"view", "delivery_read"}
	native := s.options.ToolHost.(sdk.ConversationToolResultAuthorizer)
	custom := &modelSourceTestHost{definitions: step.Input.Tools}
	custom.result = func(ctx context.Context, in sdk.ConversationToolRequest, result sdk.ConversationToolResult) error {
		if err := native.AuthorizeConversationToolResult(ctx, in, result); err != nil {
			return err
		}
		ctx = context.WithValue(ctx, conversationRawExecutionSourceKey{}, true)
		_ = native.AuthorizeConversationToolResult(ctx, in, result)
		return nil
	}
	s.options.ToolHost = custom
	if err := s.reauthorizeExecutionInputs(deliverySourceContext(t.Context(), "released"), claim, step); !collaborationDenied(err) || policy.checks != 3 {
		t.Fatal("discarded raw source denial authorized the model input", policy.checks, err)
	}
}
