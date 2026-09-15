package application

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

type codingTestPolicy struct{ requests []sdk.ConversationToolRequest }

func (p *codingTestPolicy) AuthorizeConversationTool(_ context.Context, request sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	p.requests = append(p.requests, request)
	return sdk.ConversationToolAuthorization{Granted: true, Revision: "policy-7"}, nil
}

type codingTestHost struct{ policy *codingTestPolicy }

func (h codingTestHost) ConversationTools(context.Context, sdk.ConversationAuthority) ([]sdk.ConversationToolDefinition, error) {
	return []sdk.ConversationToolDefinition{}, nil
}
func (h codingTestHost) AuthorizeConversationTool(ctx context.Context, request sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	return h.policy.AuthorizeConversationTool(ctx, request)
}
func (codingTestHost) InvokeConversationTool(context.Context, sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	return sdk.ConversationToolResult{}, nil
}
func (codingTestHost) ReconcileConversationTool(context.Context, sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	return sdk.ConversationToolResult{}, nil
}

type codingTestRuntime struct {
	request sdk.ConversationCodingRequest
	closed  sdk.ConversationCodingScope
}

func (r *codingTestRuntime) ExecuteConversationCoding(_ context.Context, request sdk.ConversationCodingRequest) (sdk.ConversationToolResult, error) {
	r.request = request
	return sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"kind":"file_read"}`)}, nil
}
func (r *codingTestRuntime) CloseConversationCodingScope(_ context.Context, scope sdk.ConversationCodingScope) error {
	r.closed = scope
	return nil
}

func TestCodingRuntimeCatalogAuthorizationAndScope(t *testing.T) {
	authority := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", RoleKey: "developer"}
	policy := &codingTestPolicy{}
	runtime := &codingTestRuntime{}
	service := &ConversationService{repo: nil, options: ConversationOptions{ToolHost: codingTestHost{policy}, PersonalAuthorizer: policy, CodingRuntime: runtime}}
	definitions, _, err := service.registeredExecutionCatalog(t.Context(), authority)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, definition := range definitions {
		found[definition.Key] = true
	}
	for _, definition := range sdk.ConversationCodingTools() {
		if !found[definition.Key] {
			t.Fatal("authorized coding tool missing", definition.Key)
		}
		auth, err := service.executionToolAuthorizer(definition.Key).AuthorizeConversationTool(t.Context(), sdk.ConversationToolRequest{Authority: authority, Definition: definition})
		if err != nil || !auth.Granted || auth.ConfirmationRequired != (definition.Effect == "write") {
			t.Fatalf("authorization %s = %+v, %v", definition.Key, auth, err)
		}
	}
	tampered := sdk.ConversationCodingTools()[0]
	tampered.ActionKey = "other.action"
	if auth, _ := service.executionToolAuthorizer(tampered.Key).AuthorizeConversationTool(t.Context(), sdk.ConversationToolRequest{Authority: authority, Definition: tampered}); auth.Granted {
		t.Fatal("tampered coding definition was authorized")
	}
	request := sdk.ConversationToolRequest{Authority: authority, ConversationID: "conversation", RunID: "run", Call: sdk.ConversationToolCall{Name: "coding_file_read", Arguments: `{"path":"main.go"}`}, IdempotencyKey: "receipt"}
	if result, err := service.executeConversationCoding(t.Context(), request, true); err != nil || result.Status != "completed" {
		t.Fatal(result, err)
	}
	if runtime.request.Scope != (sdk.ConversationCodingScope{RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", ConversationID: "conversation", RunID: "run"}) || !runtime.request.Reconcile || runtime.request.IdempotencyKey != "receipt" {
		t.Fatalf("runtime request = %+v", runtime.request)
	}
	service.closeConversationCodingScope(t.Context(), authority, "conversation", "run")
	if runtime.closed != runtime.request.Scope {
		t.Fatal("runtime scope was not closed")
	}
}
