package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

type userToolGrantPolicy map[string]map[string]bool

func (p userToolGrantPolicy) AuthorizeConversationTool(_ context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	return sdk.ConversationToolAuthorization{Granted: in.Authority.Known && p[in.Authority.UserID][in.Definition.Key]}, nil
}

func TestPermissionScopedCatalogDoesNotExposeOtherUsersToolDefinitions(t *testing.T) {
	policy := userToolGrantPolicy{"alice": {"time_now": true}, "bob": {"calculate": true}}
	h := &PersonalConversationHost{repo: privatePeerSources{}, authorizer: policy}
	s := &ConversationService{options: ConversationOptions{ToolHost: h}}
	ctx, finish := s.beginCatalogEvaluation(t.Context())
	defer finish()
	for _, user := range []string{"alice", "bob", "alice"} {
		a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: user}
		definitions, _, err := s.executionCatalog(ctx, a)
		if err != nil || len(definitions) != 1 || !policy[user][definitions[0].Key] {
			t.Fatal("catalog crossed user grants", user, definitions, err)
		}
		raw, _ := json.Marshal(definitions)
		hidden := "calculate"
		if user == "bob" {
			hidden = "time_now"
		}
		if strings.Contains(string(raw), `"key":"`+hidden+`"`) {
			t.Fatal("unauthorized tool definition entered model input", user, string(raw))
		}
	}
}

func TestPreviouslyLoadedToolIsStillRejectedAfterGrantRevocation(t *testing.T) {
	policy := userToolGrantPolicy{"alice": {"time_now": true}}
	h := &PersonalConversationHost{repo: privatePeerSources{}, authorizer: policy}
	s := &ConversationService{options: ConversationOptions{ToolHost: h}}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "alice"}
	definitions, _, err := s.executionCatalog(t.Context(), a)
	if err != nil || len(definitions) != 1 {
		t.Fatal(definitions, err)
	}
	delete(policy["alice"], "time_now")
	live, _, err := s.executionCatalog(t.Context(), a)
	if err != nil || len(live) != 0 {
		t.Fatal("revoked tool still loaded", live, err)
	}
	in := sdk.ConversationToolRequest{Authority: a, Definition: definitions[0], Call: sdk.ConversationToolCall{ID: "guessed", Name: "time_now", Arguments: `{}`}}
	auth, err := h.AuthorizeConversationTool(t.Context(), in)
	if err != nil || auth.Granted {
		t.Fatal("stale tool grant accepted", auth, err)
	}
	result, err := h.InvokeConversationTool(t.Context(), in)
	if err != nil || result.Status != "failed" || result.ErrorCode != "tool_access_denied" {
		t.Fatal("guessed tool bypassed execution authorization", result, err)
	}
}
