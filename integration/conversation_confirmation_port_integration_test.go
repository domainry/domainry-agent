package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
)

type verifiedConfirmationHost struct {
	sdk.ConversationToolHost
	verifier sdk.ConversationConfirmationVerifier
	t        *testing.T
	mu       sync.Mutex
	executed []sdk.ConversationToolRequest
}

func (h *verifiedConfirmationHost) AuthorizeConversationTool(ctx context.Context, r sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	auth, err := h.ConversationToolHost.AuthorizeConversationTool(ctx, r)
	if err != nil || !auth.Granted {
		return auth, err
	}
	auth.ConfirmationRequired = true
	if r.Confirmation != nil {
		valid, err := h.verifier.VerifyConversationToolConfirmation(ctx, r)
		if err != nil {
			return sdk.ConversationToolAuthorization{}, err
		}
		auth.ConfirmationRequired = !valid
	}
	return auth, nil
}

func (h *verifiedConfirmationHost) InvokeConversationTool(ctx context.Context, r sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	valid, err := h.verifier.VerifyConversationToolConfirmation(ctx, r)
	if err != nil || !valid {
		return sdk.ConversationToolResult{}, fmt.Errorf("durable approval missing: %v", err)
	}
	// Probe the actual persisted approval while this worker owns a valid lease.
	// Every changed field must fail without changing the stored interaction.
	for name, mutate := range map[string]func(*sdk.ConversationToolRequest){
		"actor":            func(q *sdk.ConversationToolRequest) { q.Authority.UserID = "another-user" },
		"workspace":        func(q *sdk.ConversationToolRequest) { q.Authority.WorkspaceID = "another-workspace" },
		"runtime":          func(q *sdk.ConversationToolRequest) { q.Authority.RuntimeID = "another-runtime" },
		"unknown_identity": func(q *sdk.ConversationToolRequest) { q.Authority.Known = false },
		"conversation":     func(q *sdk.ConversationToolRequest) { q.ConversationID = "another-conversation" },
		"run":              func(q *sdk.ConversationToolRequest) { q.RunID = "another-run" },
		"call":             func(q *sdk.ConversationToolRequest) { q.Call.ID = "new-call" },
		"call_name":        func(q *sdk.ConversationToolRequest) { q.Call.Name = "different-tool" },
		"arguments":        func(q *sdk.ConversationToolRequest) { q.Call.Arguments = `{"title":"unapproved content"}` },
		"definition":       func(q *sdk.ConversationToolRequest) { q.Definition.Description = "changed contract" },
		"version":          func(q *sdk.ConversationToolRequest) { q.Definition.Version = "2" },
		"effect":           func(q *sdk.ConversationToolRequest) { q.Definition.Effect = "read" },
		"action":           func(q *sdk.ConversationToolRequest) { q.Definition.ActionKey = "different.action" },
		"step":             func(q *sdk.ConversationToolRequest) { q.Step++ },
		"lease":            func(q *sdk.ConversationToolRequest) { q.LeaseOwner = "different-worker" },
		"fence":            func(q *sdk.ConversationToolRequest) { q.Fence++ },
		"confirmation_id":  func(q *sdk.ConversationToolRequest) { q.ConfirmationID = "forged" },
		"receipt_time": func(q *sdk.ConversationToolRequest) {
			q.Confirmation.ApprovedAt = q.Confirmation.ApprovedAt.Add(time.Second)
		},
		"receipt_actor":  func(q *sdk.ConversationToolRequest) { q.Confirmation.UserID = "another-user" },
		"receipt_absent": func(q *sdk.ConversationToolRequest) { q.Confirmation = nil },
	} {
		q := r
		receipt := *r.Confirmation
		q.Confirmation = &receipt
		mutate(&q)
		if ok, _ := h.verifier.VerifyConversationToolConfirmation(ctx, q); ok {
			h.t.Errorf("%s changed persisted confirmation authority", name)
		}
	}
	h.mu.Lock()
	h.executed = append(h.executed, r)
	h.mu.Unlock()
	return h.ConversationToolHost.InvokeConversationTool(ctx, r)
}
func (h *verifiedConfirmationHost) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.executed)
}

func TestConversationConfirmationPortUsesDurableExactApproval(t *testing.T) {
	for _, scenario := range []string{"listed_restart_new_call", "single_call", "reject", "cross_user"} {
		t.Run(scenario, func(t *testing.T) {
			repo, a := conversationRepository(t), conversationAuthority()
			base := &interactionHost{executionHost: executionHost{allowed: true}, respondAllowed: true}
			gate := &operationScopeGate{}
			var current *verifiedConfirmationHost
			var all []*verifiedConfirmationHost
			options := application.ConversationOptions{ToolHost: base, ExecutionAuthorizer: gate, Lease: 2 * time.Second, Poll: 10 * time.Millisecond, AssembleTools: func(host sdk.ConversationToolHost) (sdk.ConversationToolHost, error) {
				verifier, ok := host.(sdk.ConversationConfirmationVerifier)
				if !ok {
					return nil, fmt.Errorf("missing confirmation port")
				}
				current = &verifiedConfirmationHost{ConversationToolHost: host, verifier: verifier, t: t}
				all = append(all, current)
				return current, nil
			}}
			model := &executionModel{step: func(n int, in sdk.ConversationStepRequest) (sdk.ConversationStepResult, error) {
				if n == 1 {
					return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: "first", Name: "create_item", Arguments: `{"title":"first exact item"}`}, {ID: "second", Name: "create_item", Arguments: `{"title":"second exact item"}`}}}}, nil
				}
				if n == 2 && scenario == "listed_restart_new_call" {
					return resultToolCall("create_item", "outside-list", map[string]string{"title": "new item"}), nil
				}
				return (&executionModel{}).answerResult(), nil
			}}
			s, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			c, err := s.Create(t.Context(), sdk.ConversationCreate{ClientID: scenario}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := s.Send(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "send", Message: "create these items"}, a)
			if err != nil {
				t.Fatal(err)
			}
			waiting := waitConversationState(t, s, c.ID, run.ID, "waiting_confirmation")
			i := waiting.Interaction
			if i == nil || len(i.Operations) != 2 || current.count() != 0 {
				t.Fatal("missing frozen list or effect before approval")
			}
			defs, _ := base.ConversationTools(t.Context(), a)
			forged := sdk.ConversationToolRequest{Authority: a, ConversationID: c.ID, RunID: run.ID, Step: i.Step, Call: sdk.ConversationToolCall{ID: i.CallID, Name: i.Tool, Arguments: i.Arguments}, Definition: defs[0], ConfirmationID: i.ID, Confirmation: &sdk.ConversationConfirmation{ID: i.ID, UserID: a.UserID, ActionKey: i.ActionKey, ToolVersion: i.ToolVersion, ArgumentsHash: i.ArgumentsHash, ApprovedAt: time.Now()}}
			if ok, _ := current.verifier.VerifyConversationToolConfirmation(t.Context(), forged); ok {
				t.Fatal("pending confirmation accepted forged approval")
			}
			response := sdk.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "approval", ExpectedRevision: i.Revision, Decision: "approve", Scope: "listed_operations"}
			if scenario == "single_call" {
				response.Scope = ""
			}
			if scenario == "reject" {
				response.Decision = "reject"
				response.Scope = ""
			}
			if scenario == "cross_user" {
				other := a
				other.UserID = "other-user"
				if _, err := s.Respond(t.Context(), c.ID, run.ID, response, other); err == nil || current.count() != 0 {
					t.Fatal("foreign user approved")
				}
				return
			}
			gate.pause.Store(true)
			if _, err := s.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
				t.Fatal(err)
			}
			if scenario == "reject" {
				if current.count() != 0 {
					t.Fatal("rejected operation executed")
				}
				return
			}
			s.Close()
			if current.count() != 0 {
				t.Fatal("approval bypassed paused execution")
			}
			gate.pause.Store(false)
			s, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "single_call" || scenario == "listed_restart_new_call" {
				next := waitConversationState(t, s, c.ID, run.ID, "waiting_confirmation")
				expected, count := "second", 1
				if scenario == "listed_restart_new_call" {
					expected, count = "outside-list", 2
				}
				if next.Interaction.CallID != expected || current.count() != count {
					t.Fatalf("approval expanded: %+v effects=%d", next.Interaction, current.count())
				}
				if _, err := s.Respond(t.Context(), c.ID, run.ID, sdk.ConversationInteractionResponse{InteractionID: next.Interaction.ID, ClientID: "next-approval", ExpectedRevision: next.Interaction.Revision, Decision: "approve"}, a); err != nil {
					t.Fatal(err)
				}
			}
			final := waitConversation(t, s, c.ID, run.ID)
			if final.Status != "completed" {
				t.Fatalf("execution %+v", final)
			}
			expected := 2
			if scenario == "listed_restart_new_call" {
				expected = 3
			}
			if current.count() != expected {
				t.Fatal("wrong effect count", current.count())
			}
			current.mu.Lock()
			saved := append([]sdk.ConversationToolRequest(nil), current.executed...)
			current.mu.Unlock()
			for _, q := range saved {
				if ok, _ := current.verifier.VerifyConversationToolConfirmation(t.Context(), q); ok {
					t.Fatal("completed lease reused to execute old approval")
				}
			}
			for _, h := range all[:len(all)-1] {
				if h.count() != 0 {
					t.Fatal("closed worker executed")
				}
			}
		})
	}
}

type originalResultPolicyHost struct {
	*interactionHost
	denials int
}

func (h *originalResultPolicyHost) AuthorizeConversationToolResult(context.Context, sdk.ConversationToolRequest, sdk.ConversationToolResult) error {
	h.denials++
	return fmt.Errorf("original result policy denied")
}

func TestConversationConfirmationAssemblyPreservesOriginalOptionalPolicies(t *testing.T) {
	base := &originalResultPolicyHost{interactionHost: &interactionHost{executionHost: executionHost{allowed: true}, respondAllowed: true}}
	var wrapped sdk.ConversationToolHost
	s, err := conversationassembly.NewService(conversationRepository(t), &executionModel{}, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: base, AssembleTools: func(h sdk.ConversationToolHost) (sdk.ConversationToolHost, error) { wrapped = h; return h, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	policy, ok := wrapped.(sdk.ConversationToolResultAuthorizer)
	if !ok {
		t.Fatal("optional result policy hidden")
	}
	if policy.AuthorizeConversationToolResult(t.Context(), sdk.ConversationToolRequest{}, sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{}`)}) == nil || base.denials != 1 {
		t.Fatal("result policy bypassed")
	}
	interaction, ok := wrapped.(sdk.ConversationInteractionAuthorizer)
	if !ok {
		t.Fatal("optional interaction policy hidden")
	}
	if auth, err := interaction.AuthorizeConversationInteraction(t.Context(), conversationAuthority(), sdk.ConversationInteraction{}); err != nil || !auth.Granted {
		t.Fatal("interaction policy lost", err)
	}
}
