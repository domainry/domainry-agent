package application

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type productSourceReadProbe struct {
	sdk.ConversationToolHost
	authorizations, results int
	err                     error
}

func (h *productSourceReadProbe) AuthorizeConversationTool(context.Context, sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	h.authorizations++
	return sdk.ConversationToolAuthorization{Granted: true}, nil
}
func (h *productSourceReadProbe) AuthorizeConversationToolResult(context.Context, sdk.ConversationToolRequest, sdk.ConversationToolResult) error {
	h.results++
	return h.err
}

func TestProductSourceReadMemoReusesOverlappingPrefixesOnlyWithinOneAudit(t *testing.T) {
	definition := sdk.ConversationToolDefinition{Key: "crm_fixture_read", Version: "1", Effect: "read"}
	host := &productSourceReadProbe{}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", RoleKey: "reader"}
	s := &ConversationService{runtimeID: a.RuntimeID, options: ConversationOptions{ToolHost: host, ToolDefinitions: []sdk.ConversationToolDefinition{definition}}}
	record := persistence.ConversationToolExecution{Step: 0, State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "call", Name: definition.Key, Arguments: `{}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: []byte(`{"value":1}`)}}
	owner := sdk.ConversationRunReference{ConversationID: "conversation", RunID: "run"}
	audit := s.sourceAudit(a, owner.ConversationID)
	for _, cutoff := range []int{0, 2, 3, 0} {
		owner.BeforeStep = cutoff
		roots, err := audit.record(t.Context(), owner, record)
		if err != nil || len(roots) != 1 || roots[0].BeforeStep != 2 {
			t.Fatalf("prefix %d: %+v %v", cutoff, roots, err)
		}
		roots[0].RunID = "caller-mutation"
	}
	if host.authorizations != 1 || host.results != 1 {
		t.Fatalf("same exact read revalidated per prefix: checks=%d results=%d", host.authorizations, host.results)
	}
	host.err = errors.New("source access revoked")
	if _, err := s.sourceAudit(a, owner.ConversationID).record(t.Context(), owner, record); !errors.Is(err, host.err) {
		t.Fatal("next model/read boundary reused stale source access", err)
	}
	if host.results != 2 {
		t.Fatal("new audit did not recheck the source")
	}
	if _, err := s.sourceAudit(a).record(t.Context(), owner, record); !errors.Is(err, host.err) {
		t.Fatal("failed validation was cached", err)
	}
	// Exact content, configured Agent and cancellation remain independent keys.
	record.Result.Content = []byte(`{"value":2}`)
	if _, err := audit.record(t.Context(), owner, record); !errors.Is(err, host.err) {
		t.Fatal("changed receipt reused an earlier decision", err)
	}
	host.err = nil
	before := host.results
	if _, err := audit.record(t.Context(), owner, record); err != nil || host.results != before+1 {
		t.Fatal("failed receipt check was memoized", err)
	}
	record.Result.Content = []byte(`{"value":1}`)
	agentCtx := context.WithValue(t.Context(), conversationAgentContextKey{}, &sdk.ConversationAgentSnapshot{ID: "another-agent"})
	before = host.results
	if _, err := audit.record(agentCtx, owner, record); err != nil || host.results != before+1 {
		t.Fatal("memo crossed Agent configuration", err)
	}
	child := audit.childSourceAudit()
	child.a.RoleKey = "different-role"
	before = host.results
	if _, err := child.record(t.Context(), owner, record); err != nil || host.results != before+1 {
		t.Fatal("memo crossed principal role", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := audit.record(cancelled, owner, record); !errors.Is(err, context.Canceled) {
		t.Fatal("cache ignored cancellation", err)
	}
}

func TestProductSourceReadMemoExcludesWritesAndCoreTools(t *testing.T) {
	s := &ConversationService{}
	audit := s.sourceAudit(sdk.ConversationAuthority{})
	for _, definition := range append(agentPersonalConversationTools(), sdk.ConversationToolDefinition{Key: "crm_write", Effect: "write"}) {
		s.options.ToolDefinitions = []sdk.ConversationToolDefinition{definition}
		record := persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{Name: definition.Key}, Result: &sdk.ConversationToolResult{Status: "completed"}}
		if audit.memoizedProductRead(record) {
			t.Fatal("core/write tool acquired the product read memo", definition.Key)
		}
	}
}
