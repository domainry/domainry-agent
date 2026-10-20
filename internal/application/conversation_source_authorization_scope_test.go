package application

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestSourceAuthorizationScopeRejectsLateOwnerRevisionChange(t *testing.T) {
	starts, finishes := 0, 0
	denied := errors.New("current owner revision changed")
	definition := sdk.ConversationToolDefinition{Key: "crm_fixture_read", Version: "1", Effect: "read"}
	host := &productSourceReadProbe{}
	s := &ConversationService{options: ConversationOptions{ToolHost: host, ToolDefinitions: []sdk.ConversationToolDefinition{definition}, BeginSourceAuthorizationScope: func(ctx context.Context) (context.Context, func() error) {
		starts++
		return ctx, func() error { finishes++; return denied }
	}}}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	record := persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "call", Name: definition.Key, Arguments: `{}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: []byte(`{}`)}}
	audit := s.sourceAudit(a)
	owner := sdk.ConversationRunReference{ConversationID: "conversation", RunID: "run"}
	for i := 0; i < 2; i++ {
		roots, err := audit.record(t.Context(), owner, record)
		if !errors.Is(err, denied) || len(roots) != 0 {
			t.Fatal("late owner denial released a source result", roots, err)
		}
	}
	if starts != 2 || finishes != 2 || host.results != 2 {
		t.Fatal("source memo crossed a completed owner evaluation", starts, finishes, host.results)
	}
	ctx, finish := s.beginSourceAuthorizationEvaluation(t.Context())
	_, nestedFinish := s.beginSourceAuthorizationEvaluation(ctx)
	if err := nestedFinish(); err != nil || finishes != 2 {
		t.Fatal("nested read prematurely closed the owner boundary", err)
	}
	if !errors.Is(finish(), denied) || finishes != 3 {
		t.Fatal("outer read did not validate its owner revision")
	}
	_, nextFinish := s.beginSourceAuthorizationEvaluation(ctx)
	if !errors.Is(nextFinish(), denied) || starts != 4 || finishes != 4 {
		t.Fatal("closed context retained an earlier read evaluation")
	}
}
