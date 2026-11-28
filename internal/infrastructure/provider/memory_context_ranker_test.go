package provider

import (
	"context"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type memoryRankModelStub struct {
	request agentsdk.ConversationModelRequest
	result  string
}

func (m *memoryRankModelStub) GenerateConversation(_ context.Context, request agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	m.request = request
	return agentsdk.ConversationModelResult{Content: m.result}, nil
}

func TestModelMemoryContextRankerBindsScoresToExactItems(t *testing.T) {
	model := &memoryRankModelStub{result: `{"scores":[{"provider_key":"personal","memory_id":"a","revision":"1","relevance":0.9},{"provider_key":"personal","memory_id":"b","revision":"2","relevance":0.1}]}`}
	ranker := NewModelMemoryContextRanker(model)
	request := agentsdk.MemoryContextRankRequest{
		Context: agentsdk.MemoryContextRequest{WorkspaceID: "workspace", UserID: "user", RunID: "run", Query: "production region"},
		Items: []agentsdk.MemoryContextItem{
			{ID: "a", ProviderKey: "personal", Revision: "1", Content: "Tokyo"},
			{ID: "b", ProviderKey: "personal", Revision: "2", Content: "Concise replies"},
		},
	}
	result, err := ranker.RankMemoryContext(t.Context(), request)
	if err != nil || len(result.Scores) != 2 || result.Scores[0].MemoryID != "a" || result.Scores[0].Relevance != 0.9 || model.request.Purpose != "memory" || model.request.IdempotencyKey == "" {
		t.Fatalf("result=%+v request=%+v err=%v", result, model.request, err)
	}

	model.result = `{"scores":[{"provider_key":"personal","memory_id":"unknown","revision":"1","relevance":1},{"provider_key":"personal","memory_id":"b","revision":"2","relevance":0.1}]}`
	if _, err = ranker.RankMemoryContext(t.Context(), request); err == nil {
		t.Fatal("ranker accepted a score for an unknown memory")
	}
}
