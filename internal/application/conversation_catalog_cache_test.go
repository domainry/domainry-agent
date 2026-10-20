package application

import (
	"context"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestCatalogEvaluationCachesOnlyWithinItsAuthorityAndLifetime(t *testing.T) {
	calls, starts, ends := 0, 0, 0
	host := catalogOnlyHost{definitions: []agentsdk.ConversationToolDefinition{agentsdk.PersonalConversationTools()[1]}, inspectContext: func(context.Context) { calls++ }}
	s := &ConversationService{options: ConversationOptions{ToolHost: host, BeginToolCatalogScope: func(ctx context.Context) (context.Context, func()) { starts++; return ctx, func() { ends++ } }}}
	ctx, end := s.beginCatalogEvaluation(t.Context())
	a := agentsdk.ConversationAuthority{UserID: "one"}
	first, compiled, err := s.registeredExecutionCatalog(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	firstSchema := compiled[first[0].Key].input
	delete(compiled, first[0].Key)
	first[0].InputSchema[0] = 'X'
	second, compiled, err := s.registeredExecutionCatalog(ctx, a)
	if err != nil || second[0].InputSchema[0] != '{' || len(compiled) != 1 || compiled[second[0].Key].input != firstSchema || calls != 1 {
		t.Fatalf("snapshot/schema cache invalid: %v calls=%d", err, calls)
	}
	a.UserID = "two"
	if _, _, err := s.registeredExecutionCatalog(ctx, a); err != nil || calls != 2 {
		t.Fatal("cache crossed users", err, calls)
	}
	end()
	if starts != 1 || ends != 1 {
		t.Fatal("scope not balanced", starts, ends)
	}
	if _, _, err := s.registeredExecutionCatalog(ctx, a); err != nil || calls != 3 || starts != 2 || ends != 2 {
		t.Fatal("closed scope survived", err, calls, starts, ends)
	}
	if len(s.catalogCache) != 1 {
		t.Fatal("identical schemas were compiled into multiple cache entries")
	}
}

func TestCatalogSchemaCacheInvalidatesChangedContracts(t *testing.T) {
	definition := agentsdk.PersonalConversationTools()[1]
	host := catalogOnlyHost{definitions: []agentsdk.ConversationToolDefinition{definition}}
	s := &ConversationService{options: ConversationOptions{ToolHost: host}}
	_, first, err := s.executionCatalog(t.Context(), agentsdk.ConversationAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	host.definitions[0].InputSchema = []byte(`{"type":"object","required":["new_required"]}`)
	_, second, err := s.executionCatalog(t.Context(), agentsdk.ConversationAuthority{})
	if err != nil || first[definition.Key].input == second[definition.Key].input {
		t.Fatal("schema contract change did not invalidate compiled schema", err)
	}
}
