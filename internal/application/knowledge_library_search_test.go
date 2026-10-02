package application

import (
	"context"
	"encoding/json"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type libraryKnowledgeProbe struct {
	agentsdk.ConversationLibraryKnowledgeSource
	library, query string
	authority      agentsdk.ConversationAuthority
	result         agentsdk.ConversationKnowledgeResult
}

func (*libraryKnowledgeProbe) Search(context.Context, string, agentsdk.ConversationAuthority) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (p *libraryKnowledgeProbe) SearchLibraryKnowledge(_ context.Context, libraryID, query string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	p.library, p.query, p.authority = libraryID, query, authority
	return p.result, nil
}

func TestConversationServiceExposesConfiguredLibraryKnowledgeSource(t *testing.T) {
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime-a", WorkspaceID: "workspace-a", UserID: "user-a", RoleKey: "sales_rep"}
	probe := &libraryKnowledgeProbe{result: agentsdk.ConversationKnowledgeResult{LibraryID: "library-a", Operation: "search", Query: "风险"}}
	service := &ConversationService{options: ConversationOptions{Knowledge: probe}}
	result, err := service.SearchLibraryKnowledge(t.Context(), "library-a", "风险", authority)
	if err != nil {
		t.Fatal(err)
	}
	if result.LibraryID != "library-a" || probe.library != "library-a" || probe.query != "风险" || probe.authority != authority {
		t.Fatalf("result=%#v probe=%#v", result, probe)
	}
}

var _ ConversationKnowledge = (*libraryKnowledgeProbe)(nil)
