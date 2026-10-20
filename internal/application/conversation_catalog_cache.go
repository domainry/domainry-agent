package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type conversationCatalogCacheEntry struct {
	compiled map[string]conversationCompiledTool
}
type conversationCatalogEvaluationKey struct{ service *ConversationService }
type conversationCatalogEvaluation struct {
	mu       sync.Mutex
	closed   bool
	catalogs map[string][]byte
}

func (s *ConversationService) compiledConversationTools(definitions []agentsdk.ConversationToolDefinition) (map[string]conversationCompiledTool, error) {
	raw, err := json.Marshal(definitions)
	if err != nil {
		return nil, err
	}
	_, compiled, err := s.compileCatalogSnapshot(raw)
	return compiled, err
}

// Reuse applies only to a synchronous discovery/catalog operation, never a
// model wait, tool execution, task lease or another HTTP request.
func (s *ConversationService) beginCatalogEvaluation(ctx context.Context) (context.Context, func()) {
	key := conversationCatalogEvaluationKey{s}
	if current, _ := ctx.Value(key).(*conversationCatalogEvaluation); current != nil {
		current.mu.Lock()
		active := !current.closed
		current.mu.Unlock()
		if active {
			return ctx, func() {}
		}
	}
	endOwner := func() {}
	if s.options.BeginToolCatalogScope != nil {
		ctx, endOwner = s.options.BeginToolCatalogScope(ctx)
	}
	memo := &conversationCatalogEvaluation{catalogs: make(map[string][]byte)}
	return context.WithValue(ctx, key, memo), func() {
		memo.mu.Lock()
		memo.closed, memo.catalogs = true, nil
		memo.mu.Unlock()
		endOwner()
	}
}

func (s *ConversationService) compileCatalogSnapshot(raw []byte) ([]agentsdk.ConversationToolDefinition, map[string]conversationCompiledTool, error) {
	var definitions []agentsdk.ConversationToolDefinition
	if err := json.Unmarshal(raw, &definitions); err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(raw)
	key := hex.EncodeToString(digest[:])
	s.catalogCacheMu.Lock()
	defer s.catalogCacheMu.Unlock()
	entry, found := s.catalogCache[key]
	if !found {
		compiled, err := compileConversationTools(definitions)
		if err != nil {
			return nil, nil, err
		}
		entry = conversationCatalogCacheEntry{compiled: compiled}
		if s.catalogCache == nil || len(s.catalogCache) >= 32 {
			s.catalogCache = make(map[string]conversationCatalogCacheEntry)
		}
		s.catalogCache[key] = entry
	}
	// Schema objects are immutable. Maps and definition bytes returned to a
	// caller are owned copies; availability filtering cannot poison the cache.
	compiled := make(map[string]conversationCompiledTool, len(definitions))
	for _, definition := range definitions {
		value := entry.compiled[definition.Key]
		value.definition = definition
		compiled[definition.Key] = value
	}
	return definitions, compiled, nil
}

func (s *ConversationService) catalogEvaluationCache(ctx context.Context, a agentsdk.ConversationAuthority) (*conversationCatalogEvaluation, string) {
	memo, _ := ctx.Value(conversationCatalogEvaluationKey{s}).(*conversationCatalogEvaluation)
	all, _ := ctx.Value(conversationAgentCatalogKey{}).(bool)
	key := conversationDigest([]any{a, selectedConversationAgent(ctx), all})
	return memo, key
}
