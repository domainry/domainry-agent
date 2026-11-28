package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/logging"
)

const (
	defaultTaskMemoryItems  = 8
	defaultTaskMemoryBytes  = 4096
	maximumTaskMemoryItems  = agentsdk.AgentMemoryMaxItems
	maximumTaskMemoryBytes  = agentsdk.AgentMemoryMaxBytes
	maximumMemoryCandidates = 256
)

type personalMemoryReader interface {
	Memories(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationMemory, error)
}

type personalMemoryWriter interface {
	WriteMemory(context.Context, agentsdk.ConversationMemoryWrite, agentsdk.ConversationAuthority) (agentsdk.ConversationMemory, error)
}

type personalMemoryContextProvider struct {
	repository personalMemoryReader
	ranker     agentsdk.MemoryContextRanker
}

type conversationHistoryMemoryContextProvider struct {
	repository agentpersistence.ConversationHistoryRepository
	recent     agentpersistence.ConversationMemoryHistoryRepository
	semantic   bool
}

func NewPersonalMemoryContextProvider(repository personalMemoryReader, rankers ...agentsdk.MemoryContextRanker) agentsdk.MemoryContextProvider {
	var ranker agentsdk.MemoryContextRanker
	if len(rankers) != 0 {
		ranker = rankers[0]
	}
	return &personalMemoryContextProvider{repository: repository, ranker: ranker}
}

func NewDefaultMemoryContextProviders(repository personalMemoryReader, rankers ...agentsdk.MemoryContextRanker) []agentsdk.MemoryContextProvider {
	providers := []agentsdk.MemoryContextProvider{NewPersonalMemoryContextProvider(repository, rankers...)}
	if history, ok := repository.(agentpersistence.ConversationHistoryRepository); ok {
		recent, _ := repository.(agentpersistence.ConversationMemoryHistoryRepository)
		providers = append(providers, &conversationHistoryMemoryContextProvider{repository: history, recent: recent, semantic: len(rankers) != 0 && rankers[0] != nil})
	}
	return providers
}

func (p *conversationHistoryMemoryContextProvider) ResolveMemoryContext(ctx context.Context, request agentsdk.MemoryContextRequest) (agentsdk.MemoryContextResult, error) {
	result := agentsdk.MemoryContextResult{ProviderKey: agentsdk.AgentMemoryProviderHistory}
	if p == nil || p.repository == nil || !request.Policy.ReferenceHistory || request.Preference.ProjectOnly {
		return result, nil
	}
	authority := agentsdk.ConversationAuthority{
		Known: true, RuntimeID: strings.TrimSpace(request.RuntimeID), WorkspaceID: strings.TrimSpace(request.WorkspaceID), UserID: strings.TrimSpace(request.UserID), RoleKey: strings.TrimSpace(request.RoleKey),
	}
	seen := map[string]bool{}
	appendHit := func(hit agentsdk.ConversationHistoryHit, relevance float64) {
		key := hit.ConversationID + "\x00" + hit.MessageID
		if seen[key] || hit.Role != "user" && hit.Role != "assistant" || hit.ConversationID == request.ConversationID || strings.TrimSpace(hit.Excerpt) == "" {
			return
		}
		seen[key] = true
		result.Items = append(result.Items, agentsdk.MemoryContextItem{
			ID: "history_" + hashText(key)[:32], ProviderKey: agentsdk.AgentMemoryProviderHistory, Revision: strconv.FormatInt(hit.Seq, 10),
			Kind: "conversation_history", Scope: agentsdk.ConversationMemoryScopeWorkspace, Title: "Related historical " + hit.Role + " message", Content: hit.Excerpt,
			Status: agentsdk.AgentMemoryStatusActive, Importance: 0.4, Confidence: 1, Relevance: relevance, UpdatedAt: hit.CreatedAt,
			Source: agentsdk.AgentMemorySource{
				ProviderKey: agentsdk.AgentMemoryProviderHistory, SourceID: hit.MessageID, SourceRevision: strconv.FormatInt(hit.Seq, 10),
				Kind: "conversation_message", URI: "conversation://" + hit.ConversationID + "/messages/" + hit.MessageID, CapturedAt: hit.CreatedAt,
			},
		})
	}
	for _, queryText := range memoryHistoryQueries(request.Query) {
		cursor, seenCursors := "", map[string]bool{}
		for {
			page, err := p.repository.SearchHistory(ctx, agentsdk.ConversationHistorySearch{Query: queryText, Cursor: cursor, Limit: 10}, authority)
			if err != nil {
				return agentsdk.MemoryContextResult{}, err
			}
			for _, hit := range page.Items {
				appendHit(hit, historyQueryRelevance(queryText))
				if len(result.Items) == 32 {
					break
				}
			}
			if len(result.Items) == 32 || page.Complete || page.NextCursor == "" || seenCursors[page.NextCursor] {
				break
			}
			seenCursors[page.NextCursor] = true
			cursor = page.NextCursor
		}
		if len(result.Items) == 32 {
			break
		}
	}
	if p.semantic && p.recent != nil && agentsdk.EffectiveAgentMemoryRetrievalMode(request.Policy, request.Preference) == agentsdk.AgentMemoryRetrievalHybrid && len(result.Items) < agentsdk.AgentMemoryMaxRankItems {
		recent, err := p.recent.RecentMemoryHistory(ctx, request.ConversationID, agentsdk.AgentMemoryMaxRankItems-len(result.Items), authority)
		if err != nil {
			return agentsdk.MemoryContextResult{}, err
		}
		for _, hit := range recent {
			appendHit(hit, 0)
		}
	}
	result.Revision = memoryProviderRevision(result.Items)
	return result, nil
}

func memoryHistoryQueries(value string) []string {
	terms := memoryTerms(value)
	sort.SliceStable(terms, func(i, j int) bool { return len([]rune(terms[i])) > len([]rune(terms[j])) })
	out := []string{}
	seen := map[string]bool{}
	appendQuery := func(value string) {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || len([]rune(value)) < 2 || len(value) > 256 || seen[key] || len(out) == 4 {
			return
		}
		seen[key] = true
		out = append(out, value)
	}
	for _, term := range terms {
		runes := []rune(term)
		if len(runes) <= 24 {
			appendQuery(term)
			continue
		}
		appendQuery(string(runes[:8]))
		appendQuery(string(runes[len(runes)/2-4 : len(runes)/2+4]))
		appendQuery(string(runes[len(runes)-8:]))
	}
	return out
}

func historyQueryRelevance(value string) float64 {
	return min(0.85, 0.45+float64(min(16, len([]rune(value))))/40)
}

func (p *personalMemoryContextProvider) ResolveMemoryContext(ctx context.Context, request agentsdk.MemoryContextRequest) (agentsdk.MemoryContextResult, error) {
	result := agentsdk.MemoryContextResult{ProviderKey: agentsdk.AgentMemoryProviderPersonal}
	if p == nil || p.repository == nil || !request.Policy.ReferenceSaved {
		return result, nil
	}
	authority := agentsdk.ConversationAuthority{
		Known: true, RuntimeID: strings.TrimSpace(request.RuntimeID), WorkspaceID: strings.TrimSpace(request.WorkspaceID), UserID: strings.TrimSpace(request.UserID), RoleKey: strings.TrimSpace(request.RoleKey),
	}
	memories, err := p.repository.Memories(ctx, authority)
	if err != nil {
		return agentsdk.MemoryContextResult{}, err
	}
	result.Items = make([]agentsdk.MemoryContextItem, 0, len(memories))
	for _, memory := range memories {
		if request.Preference.ProjectOnly && (memory.Scope.Kind != agentsdk.ConversationMemoryScopeProject || memory.Scope.ProjectID != request.ProjectID) {
			continue
		}
		status := strings.TrimSpace(memory.Status)
		if status == "" {
			status = agentsdk.AgentMemoryStatusActive
		}
		if !memory.Enabled && status == agentsdk.AgentMemoryStatusActive {
			status = agentsdk.AgentMemoryStatusRetracted
		}
		scopeID := ""
		switch memory.Scope.Kind {
		case agentsdk.ConversationMemoryScopeProject:
			scopeID = memory.Scope.ProjectID
		case agentsdk.ConversationMemoryScopeConversation:
			scopeID = memory.Scope.ConversationID
		case agentsdk.ConversationMemoryScopeTask:
			scopeID = memory.Scope.TaskID
		}
		importance := memory.Importance
		if importance == 0 {
			switch memory.Kind {
			case agentsdk.ConversationMemoryKindUserPreference:
				importance = 0.8
			case agentsdk.ConversationMemoryKindProjectFact:
				importance = 0.65
			default:
				importance = 0.5
			}
		}
		confidence := memory.Confidence
		if confidence == 0 {
			confidence = 0.75
		}
		source := agentsdk.AgentMemorySource{ProviderKey: agentsdk.AgentMemoryProviderPersonal, SourceID: memory.ID, SourceRevision: strconv.FormatInt(memory.Revision, 10), Kind: "saved_memory"}
		if memory.Source != nil {
			source.CapturedAt = memory.Source.CapturedAt
			switch {
			case memory.Source.MessageID != "":
				source.SourceID = memory.Source.MessageID
				source.URI = "conversation://" + memory.Source.ConversationID + "/messages/" + memory.Source.MessageID
			case memory.Source.ConversationID != "":
				source.URI = "conversation://" + memory.Source.ConversationID
			}
		}
		result.Items = append(result.Items, agentsdk.MemoryContextItem{
			ID: memory.ID, ProviderKey: agentsdk.AgentMemoryProviderPersonal, Revision: strconv.FormatInt(memory.Revision, 10),
			Kind: memory.Kind, Scope: memory.Scope.Kind, ScopeID: scopeID, Title: memory.Title, Content: memory.Content,
			AppliesTo: append([]string(nil), memory.AppliesTo...), SupersedesID: memory.SupersedesID, Status: status,
			DoNotMention: memory.DoNotMention, Importance: importance, Confidence: confidence, Uncertainty: memory.Uncertainty,
			ValidFrom: cloneTimePointer(memory.ValidFrom), ValidUntil: cloneTimePointer(memory.ValidUntil), LastVerifiedAt: cloneTimePointer(memory.LastVerifiedAt),
			Source: source, UpdatedAt: memory.UpdatedAt,
		})
	}
	result.Revision = memoryProviderRevision(result.Items)
	return result, nil
}

func (p *personalMemoryContextProvider) SubmitMemoryCandidates(ctx context.Context, request agentsdk.AgentMemoryCandidateRequest) ([]agentsdk.AgentMemoryCandidateReceipt, error) {
	writer, ok := p.repository.(personalMemoryWriter)
	if !ok {
		return nil, nil
	}
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: request.Context.RuntimeID, WorkspaceID: request.Context.WorkspaceID, UserID: request.Context.UserID, RoleKey: request.Context.RoleKey}
	existing, err := p.repository.Memories(ctx, authority)
	if err != nil {
		return nil, err
	}
	receipts := []agentsdk.AgentMemoryCandidateReceipt{}
	for _, candidate := range request.Candidates {
		personalCandidate := candidate.Kind == agentsdk.ConversationMemoryKindUserPreference && candidate.Scope == agentsdk.ConversationMemoryScopeWorkspace || candidate.Kind == agentsdk.ConversationMemoryKindProjectFact && (candidate.Scope == agentsdk.ConversationMemoryScopeWorkspace || candidate.Scope == agentsdk.ConversationMemoryScopeProject) || candidate.Kind == agentsdk.ConversationMemoryKindTaskContext && candidate.Scope == agentsdk.ConversationMemoryScopeTask
		if !personalCandidate {
			continue
		}
		scope := agentsdk.ConversationMemoryScope{Kind: candidate.Scope}
		if candidate.Scope == agentsdk.ConversationMemoryScopeProject {
			scope.ProjectID = candidate.ScopeID
		} else if candidate.Scope == agentsdk.ConversationMemoryScopeTask {
			scope.TaskID = candidate.ScopeID
			scope.ConversationID = request.Context.ConversationID
		}
		receipt := agentsdk.AgentMemoryCandidateReceipt{CandidateID: candidate.ID}
		var conflict *agentsdk.ConversationMemory
		semanticComparisonExists := false
		for index := range existing {
			memory := &existing[index]
			if memory.Kind != candidate.Kind || !personalMemoryCandidateScopeMatches(*memory, candidate) {
				continue
			}
			if (memory.Status == agentsdk.AgentMemoryStatusActive || memory.Status == agentsdk.AgentMemoryStatusCandidate) && strings.EqualFold(strings.TrimSpace(memory.Content), strings.TrimSpace(candidate.Content)) {
				receipt.Disposition, receipt.MemoryID, receipt.Revision = agentsdk.AgentMemoryCandidateDuplicate, memory.ID, strconv.FormatInt(memory.Revision, 10)
				break
			}
			if memory.Status != agentsdk.AgentMemoryStatusActive || !memory.Enabled {
				continue
			}
			semanticComparisonExists = true
			if personalMemoryCandidateRelated(*memory, candidate) && (conflict == nil || memory.UpdatedAt.After(conflict.UpdatedAt)) {
				conflict = memory
			}
		}
		if receipt.Disposition != "" {
			receipts = append(receipts, receipt)
			continue
		}
		semanticReviewReason := ""
		if candidate.Kind == agentsdk.ConversationMemoryKindUserPreference && !candidate.RequiresReview && conflict == nil && semanticComparisonExists {
			switch {
			case agentsdk.EffectiveAgentMemoryRetrievalMode(request.Context.Policy, request.Context.Preference) != agentsdk.AgentMemoryRetrievalHybrid:
				semanticReviewReason = "semantic_conflict_check_disabled"
			case p.ranker == nil:
				semanticReviewReason = "semantic_conflict_check_unavailable"
			default:
				semanticConflict, rankErr := semanticPersonalMemoryConflict(ctx, p.ranker, request.Context, existing, candidate)
				if rankErr != nil {
					semanticReviewReason = "semantic_conflict_check_failed"
				} else if semanticConflict != nil {
					conflict = semanticConflict
					semanticReviewReason = "possible_semantic_conflict"
				}
			}
		}
		status, enabled := agentsdk.AgentMemoryStatusCandidate, false
		if request.Context.Policy.CandidateMode == agentsdk.AgentMemoryCandidatePersonal && candidate.Confidence >= 0.85 && conflict == nil && semanticReviewReason == "" && !candidate.RequiresReview {
			status, enabled = agentsdk.AgentMemoryStatusActive, true
		}
		memoryID := "mem_auto_" + hashText(request.Context.WorkspaceID + "\x00" + request.Context.UserID + "\x00" + candidate.ID + "\x00" + candidate.Title + "\x00" + candidate.Content)[:32]
		supersedes := ""
		if conflict != nil {
			supersedes = conflict.ID
		}
		capturedAt := candidate.Source.CapturedAt
		if capturedAt.IsZero() {
			capturedAt = time.Now().UTC()
		}
		memory, writeErr := writer.WriteMemory(ctx, agentsdk.ConversationMemoryWrite{
			ID: memoryID, Kind: candidate.Kind, Title: strings.TrimSpace(candidate.Title), Content: strings.TrimSpace(candidate.Content),
			Enabled: enabled, Status: status, Scope: scope,
			AppliesTo: normalizeMemoryTopics(candidate.AppliesTo), Importance: 0.6, Confidence: clamp01(candidate.Confidence), SupersedesID: supersedes,
			Source: &agentsdk.ConversationMemorySource{
				Kind: "automatic_candidate", ProviderKey: candidate.Source.ProviderKey, SourceID: candidate.Source.SourceID,
				SourceRevision: candidate.Source.SourceRevision, URI: candidate.Source.URI, CapturedAt: capturedAt,
			}, ExpectedRevision: 0,
		}, authority)
		if writeErr != nil {
			receipt.Disposition, receipt.Reason = agentsdk.AgentMemoryCandidateFailed, errorCode(writeErr, "memory_candidate_write_failed")
			receipts = append(receipts, receipt)
			continue
		}
		receipt.MemoryID, receipt.Revision = memory.ID, strconv.FormatInt(memory.Revision, 10)
		if status == agentsdk.AgentMemoryStatusActive {
			receipt.Disposition = agentsdk.AgentMemoryCandidateAutoApproved
		} else if conflict != nil {
			receipt.Disposition, receipt.Reason = agentsdk.AgentMemoryCandidateReviewRequired, valueOrDefault(semanticReviewReason, "possible_conflict")
		} else if semanticReviewReason != "" {
			receipt.Disposition, receipt.Reason = agentsdk.AgentMemoryCandidateReviewRequired, semanticReviewReason
		} else {
			receipt.Disposition = agentsdk.AgentMemoryCandidateReviewRequired
		}
		receipts = append(receipts, receipt)
		existing = append(existing, memory)
	}
	return receipts, nil
}

func semanticPersonalMemoryConflict(ctx context.Context, ranker agentsdk.MemoryContextRanker, request agentsdk.MemoryContextRequest, existing []agentsdk.ConversationMemory, candidate agentsdk.AgentMemoryCandidate) (*agentsdk.ConversationMemory, error) {
	items := make([]agentsdk.MemoryContextItem, 0, len(existing))
	positions := map[string]int{}
	for index, value := range existing {
		memory := normalizeConversationMemory(value)
		if memory.Kind != candidate.Kind || memory.Status != agentsdk.AgentMemoryStatusActive || !memory.Enabled || !personalMemoryCandidateScopeMatches(memory, candidate) {
			continue
		}
		scopeID := ""
		switch memory.Scope.Kind {
		case agentsdk.ConversationMemoryScopeProject:
			scopeID = memory.Scope.ProjectID
		case agentsdk.ConversationMemoryScopeConversation:
			scopeID = memory.Scope.ConversationID
		case agentsdk.ConversationMemoryScopeTask:
			scopeID = memory.Scope.TaskID
		}
		item := agentsdk.MemoryContextItem{
			ID: memory.ID, ProviderKey: agentsdk.AgentMemoryProviderPersonal, Revision: strconv.FormatInt(memory.Revision, 10), Kind: memory.Kind,
			Scope: memory.Scope.Kind, ScopeID: scopeID, Title: memory.Title, Content: memory.Content, AppliesTo: append([]string(nil), memory.AppliesTo...),
			Status: memory.Status, Importance: memory.Importance, Confidence: memory.Confidence, UpdatedAt: memory.UpdatedAt,
			Source: agentsdk.AgentMemorySource{ProviderKey: agentsdk.AgentMemoryProviderPersonal, SourceID: memory.ID, SourceRevision: strconv.FormatInt(memory.Revision, 10)},
		}
		key := memoryRankKey(item.ProviderKey, item.ID, item.Revision)
		positions[key] = index
		items = append(items, item)
		if len(items) == agentsdk.AgentMemoryMaxRankItems {
			break
		}
	}
	if len(items) == 0 {
		return nil, nil
	}
	rankCtx, cancel, ready := boundedMemoryRankContext(ctx)
	if !ready {
		return nil, fmt.Errorf("memory semantic conflict check deadline is too short")
	}
	defer cancel()
	contextRequest := request
	contextRequest.Query = strings.TrimSpace(candidate.Title + "\n" + candidate.Content + "\n" + strings.Join(candidate.AppliesTo, "\n"))
	ranked, err := ranker.RankMemoryContext(rankCtx, agentsdk.MemoryContextRankRequest{Context: contextRequest, Items: items})
	if err != nil {
		return nil, err
	}
	bestIndex, bestScore := -1, 0.0
	seen := map[string]bool{}
	for _, score := range ranked.Scores {
		key := memoryRankKey(strings.TrimSpace(score.ProviderKey), strings.TrimSpace(score.MemoryID), strings.TrimSpace(score.Revision))
		index, ok := positions[key]
		if !ok || seen[key] || !finiteUnit(score.Relevance) {
			return nil, fmt.Errorf("memory semantic conflict score is invalid")
		}
		seen[key] = true
		if score.Relevance > bestScore {
			bestIndex, bestScore = index, score.Relevance
		}
	}
	if bestIndex < 0 || bestScore < 0.78 {
		return nil, nil
	}
	return &existing[bestIndex], nil
}

func personalMemoryCandidateScopeMatches(memory agentsdk.ConversationMemory, candidate agentsdk.AgentMemoryCandidate) bool {
	if memory.Scope.Kind != candidate.Scope {
		return false
	}
	switch candidate.Scope {
	case agentsdk.ConversationMemoryScopeWorkspace:
		return candidate.ScopeID == ""
	case agentsdk.ConversationMemoryScopeProject:
		return candidate.ScopeID != "" && memory.Scope.ProjectID == candidate.ScopeID
	case agentsdk.ConversationMemoryScopeConversation:
		return candidate.ScopeID != "" && memory.Scope.ConversationID == candidate.ScopeID
	case agentsdk.ConversationMemoryScopeTask:
		return candidate.ScopeID != "" && memory.Scope.TaskID == candidate.ScopeID
	default:
		return false
	}
}

func personalMemoryCandidateRelated(memory agentsdk.ConversationMemory, candidate agentsdk.AgentMemoryCandidate) bool {
	if strings.EqualFold(strings.TrimSpace(memory.Title), strings.TrimSpace(candidate.Title)) {
		return true
	}
	topics := map[string]bool{}
	for _, topic := range memory.AppliesTo {
		topics[strings.ToLower(strings.TrimSpace(topic))] = true
	}
	for _, topic := range candidate.AppliesTo {
		if topics[strings.ToLower(strings.TrimSpace(topic))] {
			return true
		}
	}
	item := agentsdk.MemoryContextItem{Title: memory.Title, Content: memory.Content, AppliesTo: memory.AppliesTo}
	query := candidate.Title + "\n" + strings.Join(candidate.AppliesTo, "\n")
	return memoryLexicalRelevance(item, query) >= 0.7
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func memoryProviderRevision(items []agentsdk.MemoryContextItem) string {
	raw, _ := json.Marshal(items)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func effectiveMemoryPreference(policy agentsdk.AgentMemoryPolicy, preference agentsdk.AgentMemoryPreference) (read, create bool) {
	if !policy.Enabled {
		return false, false
	}
	switch strings.TrimSpace(preference.Mode) {
	case "", agentsdk.AgentMemoryPreferenceInherit:
		return policy.ReferenceSaved || policy.ReferenceHistory || len(policy.ProviderKeys) != 0, policy.CandidateMode != "" && policy.CandidateMode != agentsdk.AgentMemoryCandidateDisabled
	case agentsdk.AgentMemoryPreferenceDisabled:
		return false, false
	case agentsdk.AgentMemoryPreferenceReferenceOnly:
		return true, false
	case agentsdk.AgentMemoryPreferenceReferenceAndCreate:
		return true, policy.CandidateMode != "" && policy.CandidateMode != agentsdk.AgentMemoryCandidateDisabled
	default:
		return false, false
	}
}

func effectiveMemoryContextRequest(request agentsdk.MemoryContextRequest) agentsdk.MemoryContextRequest {
	request.Policy.RetrievalMode = agentsdk.EffectiveAgentMemoryRetrievalMode(request.Policy, request.Preference)
	return request
}

func taskMemoryQuery(task agentsdk.AgentTaskDefinition, input map[string]any) string {
	raw, _ := json.Marshal(input)
	query := strings.TrimSpace(task.Name + "\n" + task.Description + "\n" + task.Instruction + "\n" + string(raw))
	if len(query) > 8192 {
		query = query[:8192]
	}
	return query
}

func (s *TaskExecutionService) resolveTaskMemoryContext(ctx context.Context, run agentmodel.AgentTaskRun, authorization modulehost.TaskAuthorization) (*agentsdk.MemoryContextSnapshot, error) {
	policy := authorization.Task.Memory
	read, _ := effectiveMemoryPreference(policy, run.MemoryPreference)
	if !read {
		return nil, nil
	}
	if run.MemoryContext != nil {
		return run.MemoryContext, nil
	}
	query := taskMemoryQuery(authorization.Task, run.Input)
	s.mu.RLock()
	runtimeID := s.memoryRuntimeID
	s.mu.RUnlock()
	request := agentsdk.MemoryContextRequest{
		Consumer: agentsdk.AgentMemoryConsumerTask, RuntimeID: runtimeID, WorkspaceID: run.WorkspaceID,
		UserID: authorization.Principal.UserID, RoleKey: authorization.Principal.RoleKey, AuthorizationRevision: authorization.Principal.AuthorizationRevision,
		ProjectID: run.MemoryPreference.ProjectID, TaskID: run.ID, RunID: run.ID,
		Query: query, Policy: policy, Preference: run.MemoryPreference,
	}
	request = effectiveMemoryContextRequest(request)
	providers := s.taskMemoryProviders()
	results := make([]agentsdk.MemoryContextResult, 0, len(providers))
	for _, provider := range providers {
		result, err := provider.ResolveMemoryContext(ctx, request)
		if err != nil {
			return nil, fmt.Errorf("resolve Agent task memory: %w", err)
		}
		results = append(results, result)
	}
	now := time.Now().UTC()
	ranked, ranking, err := rankMemoryContextResults(ctx, request, results, s.taskMemoryRanker(), now)
	if err != nil {
		return nil, err
	}
	snapshot, err := selectTaskMemorySnapshot(request, ranked, now)
	if err != nil {
		return nil, err
	}
	snapshot.Ranking = ranking
	state, ok := s.state.(agentpersistence.AgentTaskMemoryStateService)
	if !ok {
		return nil, unavailable("agent.task.memory_state_unavailable")
	}
	frozen, err := state.FreezeMemoryContext(ctx, run, run.Lease.Owner, run.Lease.FencingToken, snapshot)
	if err != nil {
		return nil, err
	}
	return frozen.MemoryContext, nil
}

func (s *TaskExecutionService) taskMemoryRanker() agentsdk.MemoryContextRanker {
	s.mu.RLock()
	configured, host := s.memoryRanker, s.host
	s.mu.RUnlock()
	if ranker, ok := host.(agentsdk.MemoryContextRanker); ok {
		return ranker
	}
	return configured
}

func (s *TaskExecutionService) taskMemoryProviders() []agentsdk.MemoryContextProvider {
	s.mu.RLock()
	configured := append([]agentsdk.MemoryContextProvider(nil), s.memoryProviders...)
	host := s.host
	s.mu.RUnlock()
	providers := configured
	if registry, ok := host.(agentsdk.MemoryContextProviderRegistry); ok {
		for _, provider := range registry.MemoryContextProviders() {
			if provider != nil {
				providers = append(providers, provider)
			}
		}
	} else if provider, ok := host.(agentsdk.MemoryContextProvider); ok {
		providers = append(providers, provider)
	}
	return providers
}

func (s *TaskExecutionService) submitTaskMemoryCandidates(ctx context.Context, run agentmodel.AgentTaskRun, authorization modulehost.TaskAuthorization, candidates []agentsdk.AgentMemoryCandidate) []agentsdk.AgentMemoryCandidateReceipt {
	_, create := effectiveMemoryPreference(authorization.Task.Memory, run.MemoryPreference)
	if !create || len(candidates) == 0 {
		return nil
	}
	s.mu.RLock()
	runtimeID, configured, host := s.memoryRuntimeID, append([]agentsdk.MemoryContextProvider(nil), s.memoryProviders...), s.host
	s.mu.RUnlock()
	request := agentsdk.AgentMemoryCandidateRequest{
		Context: agentsdk.MemoryContextRequest{
			Consumer: agentsdk.AgentMemoryConsumerTask, RuntimeID: runtimeID, WorkspaceID: run.WorkspaceID,
			UserID: authorization.Principal.UserID, RoleKey: authorization.Principal.RoleKey, AuthorizationRevision: authorization.Principal.AuthorizationRevision,
			ProjectID: run.MemoryPreference.ProjectID, TaskID: run.ID, RunID: run.ID,
			Query: taskMemoryQuery(authorization.Task, run.Input), Policy: authorization.Task.Memory, Preference: run.MemoryPreference,
		},
		Candidates: append([]agentsdk.AgentMemoryCandidate(nil), candidates...),
	}
	request.Context = effectiveMemoryContextRequest(request.Context)
	sinks := []agentsdk.AgentMemoryCandidateSink{}
	for _, provider := range configured {
		if sink, ok := provider.(agentsdk.AgentMemoryCandidateSink); ok {
			sinks = append(sinks, sink)
		}
	}
	if registry, ok := host.(agentsdk.MemoryContextProviderRegistry); ok {
		for _, provider := range registry.MemoryContextProviders() {
			if provider == nil {
				continue
			}
			if sink, ok := provider.(agentsdk.AgentMemoryCandidateSink); ok {
				sinks = append(sinks, sink)
			}
		}
	} else if sink, ok := host.(agentsdk.AgentMemoryCandidateSink); ok {
		sinks = append(sinks, sink)
	}
	receiptByCandidate := map[string]agentsdk.AgentMemoryCandidateReceipt{}
	sinkFailure := ""
	candidateIDs := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		candidateIDs[candidate.ID] = true
	}
	for _, sink := range sinks {
		values, err := sink.SubmitMemoryCandidates(ctx, request)
		if err != nil {
			sinkFailure = errorCode(err, "memory_candidate_sink_failed")
			continue
		}
		if !validMemoryCandidateReceipts(values, candidateIDs) {
			sinkFailure = "memory_candidate_receipt_invalid"
			continue
		}
		for _, receipt := range values {
			if _, claimed := receiptByCandidate[receipt.CandidateID]; claimed {
				receiptByCandidate[receipt.CandidateID] = agentsdk.AgentMemoryCandidateReceipt{CandidateID: receipt.CandidateID, Disposition: agentsdk.AgentMemoryCandidateFailed, Reason: "multiple_candidate_owners"}
				continue
			}
			receiptByCandidate[receipt.CandidateID] = receipt
		}
	}
	receipts := make([]agentsdk.AgentMemoryCandidateReceipt, 0, len(candidates))
	for _, candidate := range candidates {
		if receipt, covered := receiptByCandidate[candidate.ID]; covered {
			receipts = append(receipts, receipt)
		} else if sinkFailure != "" {
			receipts = append(receipts, agentsdk.AgentMemoryCandidateReceipt{CandidateID: candidate.ID, Disposition: agentsdk.AgentMemoryCandidateFailed, Reason: sinkFailure})
		} else {
			receipts = append(receipts, agentsdk.AgentMemoryCandidateReceipt{CandidateID: candidate.ID, Disposition: agentsdk.AgentMemoryCandidateNotSubmitted, Reason: "no_owning_candidate_sink"})
		}
	}
	return receipts
}

func validMemoryCandidateReceipts(receipts []agentsdk.AgentMemoryCandidateReceipt, candidateIDs map[string]bool) bool {
	seen := make(map[string]bool, len(receipts))
	for _, receipt := range receipts {
		if !candidateIDs[receipt.CandidateID] || seen[receipt.CandidateID] || len(receipt.MemoryID) > 256 || len(receipt.Revision) > 128 || len(receipt.Reason) > 512 {
			return false
		}
		seen[receipt.CandidateID] = true
		switch receipt.Disposition {
		case agentsdk.AgentMemoryCandidateDuplicate, agentsdk.AgentMemoryCandidateAutoApproved, agentsdk.AgentMemoryCandidateReviewRequired:
			if strings.TrimSpace(receipt.MemoryID) == "" || strings.TrimSpace(receipt.Revision) == "" {
				return false
			}
		case agentsdk.AgentMemoryCandidateNotSubmitted, agentsdk.AgentMemoryCandidateFailed:
			if receipt.MemoryID != "" || receipt.Revision != "" || strings.TrimSpace(receipt.Reason) == "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func memoryCandidateDeliveryPending(evidence *agentsdk.AgentMemoryEvidence) bool {
	if evidence == nil || len(evidence.Candidates) == 0 {
		return false
	}
	if len(evidence.CandidateReceipts) == 0 {
		return true
	}
	for _, receipt := range evidence.CandidateReceipts {
		if receipt.Disposition == agentsdk.AgentMemoryCandidateFailed {
			return true
		}
	}
	return false
}

func validateTaskMemoryCandidates(candidates []agentsdk.AgentMemoryCandidate, run agentmodel.AgentTaskRun, policy agentsdk.AgentMemoryPolicy) bool {
	if len(candidates) > 8 || policy.CandidateMode == "" || policy.CandidateMode == agentsdk.AgentMemoryCandidateDisabled {
		return len(candidates) == 0
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if !conversationKey(candidate.ID) || seen[candidate.ID] || !conversationText(candidate.Title, 128, true) || !conversationText(candidate.Content, 512, true) || !finiteUnit(candidate.Confidence) || len(candidate.AppliesTo) > 16 || strings.TrimSpace(candidate.Source.ProviderKey) == "" || strings.TrimSpace(candidate.Source.SourceID) == "" || len(candidate.Source.ProviderKey) > 128 || len(candidate.Source.SourceID) > 512 || len(candidate.Source.SourceRevision) > 128 || len(candidate.Source.Kind) > 128 || len(candidate.Source.URI) > 2048 || candidate.Source.CapturedAt.IsZero() {
			return false
		}
		seen[candidate.ID] = true
		for _, topic := range candidate.AppliesTo {
			if !conversationText(topic, 64, true) {
				return false
			}
		}
		if _, err := json.Marshal(candidate); err != nil {
			return false
		}
		switch candidate.Kind {
		case agentsdk.ConversationMemoryKindUserPreference:
			if run.MemoryPreference.ProjectOnly || candidate.Scope != agentsdk.ConversationMemoryScopeWorkspace || candidate.ScopeID != "" {
				return false
			}
		case agentsdk.ConversationMemoryKindProjectFact:
			projectScope := candidate.Scope == agentsdk.ConversationMemoryScopeProject && run.MemoryPreference.ProjectID != "" && candidate.ScopeID == run.MemoryPreference.ProjectID
			workspaceScope := candidate.Scope == agentsdk.ConversationMemoryScopeWorkspace && candidate.ScopeID == "" && !run.MemoryPreference.ProjectOnly
			if (!projectScope && !workspaceScope) || !candidate.RequiresReview {
				return false
			}
		case agentsdk.ConversationMemoryKindTaskContext:
			if candidate.Scope != agentsdk.ConversationMemoryScopeTask || candidate.ScopeID != run.ID || !candidate.RequiresReview {
				return false
			}
		default:
			return false
		}
		if policy.CandidateMode == agentsdk.AgentMemoryCandidateReview && !candidate.RequiresReview {
			return false
		}
	}
	return true
}

func (s *TaskExecutionService) recoverTaskMemoryCandidates(ctx context.Context, run agentmodel.AgentTaskRun) (agentmodel.AgentTaskRun, bool) {
	if !memoryCandidateDeliveryPending(run.Evidence.Memory) {
		return run, false
	}
	host := s.taskHost()
	authorization, err := authorizeTaskRun(ctx, host, run)
	if err != nil {
		logging.RecordFailure(ctx, "agent_task_memory_candidate_reauthorization_failed", err, logging.Failure{Stage: "memory_candidate_recovery", Dependency: "task_host"})
		return run, false
	}
	receipts := s.submitTaskMemoryCandidates(ctx, run, authorization, run.Evidence.Memory.Candidates)
	memoryState, ok := s.state.(agentpersistence.AgentTaskMemoryStateService)
	if !ok || len(receipts) == 0 {
		return run, false
	}
	recorded, err := memoryState.RecordMemoryCandidateReceipts(ctx, run, receipts)
	if err != nil {
		logging.RecordFailure(ctx, "agent_task_memory_receipts_failed", err, logging.Failure{Stage: "memory_candidate_recovery", Dependency: "agent_state"})
		return run, false
	}
	return recorded, true
}

type scoredMemoryItem struct {
	item    agentsdk.MemoryContextItem
	score   float64
	reasons []string
	size    int
}

type memoryRankCandidate struct {
	resultIndex int
	itemIndex   int
	item        agentsdk.MemoryContextItem
	baseScore   float64
	size        int
}

func memoryAllowedProviders(request agentsdk.MemoryContextRequest) map[string]bool {
	allowed := make(map[string]bool, len(request.Policy.ProviderKeys)+2)
	for _, key := range request.Policy.ProviderKeys {
		if key = strings.TrimSpace(key); key != "" {
			allowed[key] = true
		}
	}
	if request.Policy.ReferenceSaved {
		allowed[agentsdk.AgentMemoryProviderPersonal] = true
	}
	if request.Policy.ReferenceHistory {
		allowed[agentsdk.AgentMemoryProviderHistory] = true
	}
	return allowed
}

func rankMemoryContextResults(ctx context.Context, request agentsdk.MemoryContextRequest, results []agentsdk.MemoryContextResult, ranker agentsdk.MemoryContextRanker, now time.Time) ([]agentsdk.MemoryContextResult, *agentsdk.MemoryContextRankingEvidence, error) {
	if ranker == nil || strings.TrimSpace(request.Query) == "" || agentsdk.EffectiveAgentMemoryRetrievalMode(request.Policy, request.Preference) != agentsdk.AgentMemoryRetrievalHybrid {
		return results, nil, nil
	}
	allowed := memoryAllowedProviders(request)
	candidates := []memoryRankCandidate{}
	for resultIndex := range results {
		providerKey := strings.TrimSpace(results[resultIndex].ProviderKey)
		if providerKey == "" || len(providerKey) > 128 {
			return nil, nil, fmt.Errorf("memory provider returned an invalid provider key")
		}
		for itemIndex, value := range results[resultIndex].Items {
			item := value
			item.ProviderKey = strings.TrimSpace(item.ProviderKey)
			if item.ProviderKey == "" {
				item.ProviderKey = providerKey
			}
			if item.ProviderKey != providerKey {
				return nil, nil, fmt.Errorf("memory provider returned an item for another provider")
			}
			baseScore, _, eligible := gateMemoryItem(request, item, allowed, now)
			if !eligible {
				continue
			}
			raw, err := json.Marshal(memoryPromptProjection(item))
			if err != nil {
				return nil, nil, fmt.Errorf("marshal memory rank candidate: %w", err)
			}
			candidates = append(candidates, memoryRankCandidate{resultIndex: resultIndex, itemIndex: itemIndex, item: item, baseScore: baseScore + max(item.Relevance, memoryLexicalRelevance(item, request.Query))*100, size: len(raw)})
		}
	}
	if len(candidates) == 0 {
		return results, nil, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].baseScore != candidates[j].baseScore {
			return candidates[i].baseScore > candidates[j].baseScore
		}
		if !candidates[i].item.UpdatedAt.Equal(candidates[j].item.UpdatedAt) {
			return candidates[i].item.UpdatedAt.After(candidates[j].item.UpdatedAt)
		}
		return candidates[i].item.ProviderKey+"\x00"+candidates[i].item.ID < candidates[j].item.ProviderKey+"\x00"+candidates[j].item.ID
	})
	rankItems := make([]agentsdk.MemoryContextItem, 0, min(len(candidates), agentsdk.AgentMemoryMaxRankItems))
	positions := map[string]memoryRankCandidate{}
	used := 0
	for _, candidate := range candidates {
		if len(rankItems) == agentsdk.AgentMemoryMaxRankItems || candidate.size > agentsdk.AgentMemoryMaxRankBytes || used+candidate.size > agentsdk.AgentMemoryMaxRankBytes {
			continue
		}
		used += candidate.size
		rankItems = append(rankItems, candidate.item)
		positions[memoryRankKey(candidate.item.ProviderKey, candidate.item.ID, candidate.item.Revision)] = candidate
	}
	if len(rankItems) == 0 {
		return results, nil, nil
	}
	rankCtx, cancel, ready := boundedMemoryRankContext(ctx)
	if !ready {
		return results, &agentsdk.MemoryContextRankingEvidence{CandidateCount: len(rankItems), DegradedReason: "deadline_budget"}, nil
	}
	defer cancel()
	ranked, err := ranker.RankMemoryContext(rankCtx, agentsdk.MemoryContextRankRequest{Context: request, Items: rankItems})
	if err != nil {
		logging.RecordFailure(ctx, "agent_memory_semantic_rank_failed", err, logging.Failure{Stage: "memory_semantic_rank", Dependency: "memory_ranker"})
		return results, &agentsdk.MemoryContextRankingEvidence{CandidateCount: len(rankItems), DegradedReason: "ranker_failed"}, nil
	}
	usagePayload, usageErr := json.Marshal(ranked.Usage)
	if len(ranked.Ranker) > 128 || usageErr != nil || len(usagePayload) > 16<<10 {
		logging.RecordFailure(ctx, "agent_memory_semantic_rank_invalid", fmt.Errorf("memory ranker returned invalid evidence"), logging.Failure{Stage: "memory_semantic_rank", Dependency: "memory_ranker"})
		return results, &agentsdk.MemoryContextRankingEvidence{CandidateCount: len(rankItems), DegradedReason: "ranker_invalid"}, nil
	}
	if len(ranked.Scores) > len(rankItems) {
		logging.RecordFailure(ctx, "agent_memory_semantic_rank_invalid", fmt.Errorf("memory ranker returned too many scores"), logging.Failure{Stage: "memory_semantic_rank", Dependency: "memory_ranker"})
		return results, &agentsdk.MemoryContextRankingEvidence{Ranker: ranked.Ranker, CandidateCount: len(rankItems), ScoreCount: len(ranked.Scores), Usage: cloneTaskMap(ranked.Usage), DegradedReason: "ranker_invalid"}, nil
	}
	out := append([]agentsdk.MemoryContextResult(nil), results...)
	for index := range out {
		out[index].Items = append([]agentsdk.MemoryContextItem(nil), results[index].Items...)
	}
	seen := map[string]bool{}
	for _, score := range ranked.Scores {
		key := memoryRankKey(strings.TrimSpace(score.ProviderKey), strings.TrimSpace(score.MemoryID), strings.TrimSpace(score.Revision))
		candidate, ok := positions[key]
		if !ok || seen[key] || !finiteUnit(score.Relevance) {
			logging.RecordFailure(ctx, "agent_memory_semantic_rank_invalid", fmt.Errorf("memory ranker returned an invalid score"), logging.Failure{Stage: "memory_semantic_rank", Dependency: "memory_ranker"})
			return results, &agentsdk.MemoryContextRankingEvidence{Ranker: ranked.Ranker, CandidateCount: len(rankItems), ScoreCount: len(ranked.Scores), Usage: cloneTaskMap(ranked.Usage), DegradedReason: "ranker_invalid"}, nil
		}
		seen[key] = true
		item := out[candidate.resultIndex].Items[candidate.itemIndex]
		item.ProviderKey = candidate.item.ProviderKey
		if score.Relevance > item.Relevance {
			item.Relevance = score.Relevance
			item.RelevanceSource = "semantic_ranker"
		}
		out[candidate.resultIndex].Items[candidate.itemIndex] = item
	}
	scorePayload, _ := json.Marshal(ranked.Scores)
	evidence := &agentsdk.MemoryContextRankingEvidence{Ranker: ranked.Ranker, CandidateCount: len(rankItems), ScoreCount: len(ranked.Scores), ScoresHash: hashBytes(scorePayload), Usage: cloneTaskMap(ranked.Usage)}
	return out, evidence, nil
}

func boundedMemoryRankContext(parent context.Context) (context.Context, context.CancelFunc, bool) {
	timeout := 5 * time.Second
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 2*time.Second {
			return nil, func() {}, false
		}
		timeout = min(timeout, remaining-time.Second)
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	return ctx, cancel, true
}

func memoryRankKey(providerKey, memoryID, revision string) string {
	return providerKey + "\x00" + memoryID + "\x00" + revision
}

func selectTaskMemorySnapshot(request agentsdk.MemoryContextRequest, results []agentsdk.MemoryContextResult, now time.Time) (agentsdk.MemoryContextSnapshot, error) {
	maxItems := request.Policy.MaxItems
	if maxItems <= 0 {
		maxItems = defaultTaskMemoryItems
	}
	if maxItems > maximumTaskMemoryItems {
		maxItems = maximumTaskMemoryItems
	}
	maxBytes := request.Policy.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultTaskMemoryBytes
	}
	if maxBytes > maximumTaskMemoryBytes {
		maxBytes = maximumTaskMemoryBytes
	}
	allowedProviders := memoryAllowedProviders(request)
	candidates := []scoredMemoryItem{}
	receipts := []agentsdk.MemoryContextReceipt{}
	seen := map[string]bool{}
	provided := 0
	for _, result := range results {
		result.ProviderKey = strings.TrimSpace(result.ProviderKey)
		if result.ProviderKey == "" || len(result.ProviderKey) > 128 {
			return agentsdk.MemoryContextSnapshot{}, fmt.Errorf("memory provider returned an invalid provider key")
		}
		provided += len(result.Items) + len(result.Receipts)
		if provided > maximumMemoryCandidates {
			return agentsdk.MemoryContextSnapshot{}, fmt.Errorf("memory providers exceeded the candidate contract")
		}
		for _, receipt := range result.Receipts {
			if receipt.ProviderKey != result.ProviderKey || !validProviderMemoryReceipt(receipt) {
				return agentsdk.MemoryContextSnapshot{}, fmt.Errorf("memory provider returned an invalid selection receipt")
			}
			receipts = append(receipts, receipt)
		}
		for _, item := range result.Items {
			item.ProviderKey = strings.TrimSpace(item.ProviderKey)
			if item.ProviderKey == "" {
				item.ProviderKey = result.ProviderKey
			}
			if item.ProviderKey != result.ProviderKey {
				return agentsdk.MemoryContextSnapshot{}, fmt.Errorf("memory provider returned an item for another provider")
			}
			key := item.ProviderKey + "\x00" + item.ID
			if seen[key] {
				return agentsdk.MemoryContextSnapshot{}, fmt.Errorf("memory provider returned multiple revisions for one item")
			}
			seen[key] = true
			score, reasons, eligible := scoreMemoryItem(request, item, allowedProviders, now)
			contentHash := memoryItemHash(item)
			if !eligible {
				receipts = append(receipts, agentsdk.MemoryContextReceipt{ProviderKey: item.ProviderKey, MemoryID: item.ID, Revision: item.Revision, Decision: agentsdk.AgentMemoryDecisionExcluded, Reasons: reasons, Score: score, ContentHash: contentHash})
				continue
			}
			raw, _ := json.Marshal(memoryPromptProjection(item))
			candidates = append(candidates, scoredMemoryItem{item: item, score: score, reasons: reasons, size: len(raw)})
		}
	}
	superseded := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.item.SupersedesID != "" {
			superseded[candidate.item.ProviderKey+"\x00"+candidate.item.SupersedesID] = true
		}
	}
	filtered := candidates[:0]
	for _, candidate := range candidates {
		if superseded[candidate.item.ProviderKey+"\x00"+candidate.item.ID] {
			receipts = append(receipts, excludedMemoryReceipt(candidate, "superseded"))
			continue
		}
		filtered = append(filtered, candidate)
	}
	candidates = filtered
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if !candidates[i].item.UpdatedAt.Equal(candidates[j].item.UpdatedAt) {
			return candidates[i].item.UpdatedAt.After(candidates[j].item.UpdatedAt)
		}
		return candidates[i].item.ProviderKey+"\x00"+candidates[i].item.ID < candidates[j].item.ProviderKey+"\x00"+candidates[j].item.ID
	})
	items := make([]agentsdk.MemoryContextItem, 0, min(maxItems, len(candidates)))
	used := 0
	for _, candidate := range candidates {
		if len(items) >= maxItems {
			receipts = append(receipts, excludedMemoryReceipt(candidate, "item_budget"))
			continue
		}
		if candidate.size > maxBytes || used+candidate.size > maxBytes {
			receipts = append(receipts, excludedMemoryReceipt(candidate, "byte_budget"))
			continue
		}
		used += candidate.size
		items = append(items, candidate.item)
		reasons := append([]string{"selected"}, candidate.reasons...)
		receipts = append(receipts, agentsdk.MemoryContextReceipt{ProviderKey: candidate.item.ProviderKey, MemoryID: candidate.item.ID, Revision: candidate.item.Revision, Decision: agentsdk.AgentMemoryDecisionSelected, Reasons: reasons, Score: candidate.score, ContentHash: memoryItemHash(candidate.item)})
	}
	queryHash := hashText(request.Query)
	contentRaw, _ := json.Marshal(struct {
		Items    []agentsdk.MemoryContextItem
		Receipts []agentsdk.MemoryContextReceipt
	}{items, receipts})
	contentHash := hashBytes(contentRaw)
	id := "memctx_" + hashText(request.WorkspaceID + "\x00" + request.RunID + "\x00" + queryHash + "\x00" + contentHash)[:32]
	return agentsdk.MemoryContextSnapshot{ID: id, Consumer: request.Consumer, QueryHash: queryHash, ContentHash: contentHash, Items: items, Receipts: receipts, FrozenAt: now.UTC()}, nil
}

func validProviderMemoryReceipt(receipt agentsdk.MemoryContextReceipt) bool {
	if strings.TrimSpace(receipt.ProviderKey) == "" || strings.TrimSpace(receipt.MemoryID) == "" || strings.TrimSpace(receipt.Revision) == "" || receipt.Decision != agentsdk.AgentMemoryDecisionExcluded || math.IsNaN(receipt.Score) || math.IsInf(receipt.Score, 0) || receipt.Score < 0 || receipt.Score > 10000 || len(receipt.ContentHash) != sha256.Size*2 || len(receipt.Reasons) == 0 || len(receipt.Reasons) > 8 {
		return false
	}
	if _, err := hex.DecodeString(receipt.ContentHash); err != nil {
		return false
	}
	for _, reason := range receipt.Reasons {
		if reason = strings.TrimSpace(reason); reason == "" || len(reason) > 64 || strings.ContainsAny(reason, "\x00\r\n\t") {
			return false
		}
	}
	return true
}

func scoreMemoryItem(request agentsdk.MemoryContextRequest, item agentsdk.MemoryContextItem, allowed map[string]bool, now time.Time) (float64, []string, bool) {
	baseScore, reasons, eligible := gateMemoryItem(request, item, allowed, now)
	if !eligible {
		return baseScore, reasons, false
	}
	lexical := memoryLexicalRelevance(item, request.Query)
	relevance := clamp01(max(item.Relevance, lexical))
	alwaysPreference := item.ProviderKey == agentsdk.AgentMemoryProviderPersonal && item.Kind == agentsdk.ConversationMemoryKindUserPreference && len(item.AppliesTo) == 0
	if relevance == 0 && !alwaysPreference {
		return baseScore, append(reasons, "unrelated"), false
	}
	if item.Relevance > lexical && item.Relevance > 0 {
		if item.RelevanceSource == "semantic_ranker" {
			reasons = append(reasons, "semantic_relevance")
		} else {
			reasons = append(reasons, "provider_relevance")
		}
	} else if lexical > 0 {
		reasons = append(reasons, "lexical_relevance")
	}
	if alwaysPreference {
		reasons = append(reasons, "general_preference")
		relevance = max(relevance, 0.2)
	}
	return baseScore + relevance*100, reasons, true
}

func gateMemoryItem(request agentsdk.MemoryContextRequest, item agentsdk.MemoryContextItem, allowed map[string]bool, now time.Time) (float64, []string, bool) {
	reasons := []string{}
	if !validMemoryContextItem(item) {
		return 0, []string{"invalid_contract"}, false
	}
	if !allowed[item.ProviderKey] && !allowed["*"] {
		return 0, []string{"provider_not_allowed"}, false
	}
	status := strings.TrimSpace(item.Status)
	if status == "" {
		status = agentsdk.AgentMemoryStatusActive
	}
	if !validMemoryStatus(status) {
		return 0, []string{"invalid_contract"}, false
	}
	if status != agentsdk.AgentMemoryStatusActive {
		return 0, []string{"status_" + status}, false
	}
	if item.DoNotMention {
		return 0, []string{"mention_suppressed"}, false
	}
	if item.ValidFrom != nil && now.Before(item.ValidFrom.UTC()) {
		return 0, []string{"not_yet_valid"}, false
	}
	if item.ValidUntil != nil && !now.Before(item.ValidUntil.UTC()) {
		return 0, []string{"expired"}, false
	}
	scopeScore := 0.0
	switch item.Scope {
	case agentsdk.ConversationMemoryScopeTask:
		if item.ScopeID != request.TaskID {
			return 0, []string{"scope_mismatch"}, false
		}
		scopeScore = 300
		reasons = append(reasons, "task_scope")
	case agentsdk.ConversationMemoryScopeProject:
		if request.ProjectID == "" || item.ScopeID != request.ProjectID {
			return 0, []string{"scope_mismatch"}, false
		}
		scopeScore = 250
		reasons = append(reasons, "project_scope")
	case agentsdk.ConversationMemoryScopeConversation:
		if request.ConversationID == "" || item.ScopeID != request.ConversationID {
			return 0, []string{"scope_mismatch"}, false
		}
		scopeScore = 200
		reasons = append(reasons, "conversation_scope")
	case agentsdk.ConversationMemoryScopeWorkspace, "":
		if request.Preference.ProjectOnly {
			return 0, []string{"project_only"}, false
		}
		scopeScore = 100
		reasons = append(reasons, "workspace_scope")
	default:
		return 0, []string{"scope_invalid"}, false
	}
	freshness := 0.0
	verified := item.UpdatedAt
	if item.LastVerifiedAt != nil {
		verified = item.LastVerifiedAt.UTC()
		reasons = append(reasons, "verified")
	}
	if !verified.IsZero() {
		days := now.Sub(verified).Hours() / 24
		if days < 0 {
			days = 0
		}
		freshness = max(0, 10-days/36.5)
	}
	return scopeScore + clamp01(item.Importance)*20 + clamp01(item.Confidence)*10 + freshness, reasons, true
}

func validMemoryContextItem(item agentsdk.MemoryContextItem) bool {
	if item.ID == "" || item.ProviderKey == "" || item.Revision == "" || strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Content) == "" || len(item.ID) > 256 || len(item.ProviderKey) > 128 || len(item.Revision) > 128 || len(item.Kind) > 128 || len(item.Scope) > 32 || len(item.ScopeID) > 256 || len(item.Title) > 256 || len(item.Content) > 4096 || len(item.AppliesTo) > 16 || len(item.SupersedesID) > 256 || item.SupersedesID == item.ID || len(item.Uncertainty) > 512 || !finiteUnit(item.Importance) || !finiteUnit(item.Confidence) || !finiteUnit(item.Relevance) || item.RelevanceSource != "" && item.RelevanceSource != "provider" && item.RelevanceSource != "semantic_ranker" || item.Source.ProviderKey != item.ProviderKey || len(item.Source.SourceID) > 512 || len(item.Source.SourceRevision) > 128 || len(item.Source.Kind) > 128 || len(item.Source.URI) > 2048 || item.ValidFrom != nil && item.ValidUntil != nil && !item.ValidUntil.After(*item.ValidFrom) {
		return false
	}
	for _, topic := range item.AppliesTo {
		if strings.TrimSpace(topic) == "" || len(topic) > 64 {
			return false
		}
	}
	if _, err := json.Marshal(memoryPromptProjection(item)); err != nil {
		return false
	}
	return true
}

func finiteUnit(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func validMemoryStatus(status string) bool {
	switch status {
	case agentsdk.AgentMemoryStatusCandidate, agentsdk.AgentMemoryStatusActive, agentsdk.AgentMemoryStatusSuperseded, agentsdk.AgentMemoryStatusContradicted, agentsdk.AgentMemoryStatusExpired, agentsdk.AgentMemoryStatusRetracted:
		return true
	default:
		return false
	}
}

func memoryLexicalRelevance(item agentsdk.MemoryContextItem, query string) float64 {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0
	}
	haystack := strings.ToLower(item.Title + "\n" + item.Content + "\n" + strings.Join(item.AppliesTo, "\n"))
	for _, topic := range item.AppliesTo {
		topic = strings.ToLower(strings.TrimSpace(topic))
		if topic != "" && (strings.Contains(query, topic) || strings.Contains(topic, query)) {
			return 1
		}
	}
	if title := strings.ToLower(strings.TrimSpace(item.Title)); title != "" && (strings.Contains(query, title) || strings.Contains(title, query)) {
		return 0.9
	}
	words := memoryTerms(query)
	matched := 0
	for _, word := range words {
		if len([]rune(word)) >= 2 && strings.Contains(haystack, word) {
			matched++
		}
	}
	if matched > 0 {
		return min(0.8, float64(matched)/float64(max(2, len(words)))+0.2)
	}
	runes := []rune(query)
	for index := 0; index+3 <= len(runes); index++ {
		fragment := string(runes[index : index+3])
		if strings.IndexFunc(fragment, unicode.IsLetter) >= 0 && strings.Contains(haystack, fragment) {
			return 0.25
		}
	}
	return 0
}

func memoryTerms(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

func memoryPromptProjection(item agentsdk.MemoryContextItem) map[string]any {
	return map[string]any{
		"id": item.ID, "provider": item.ProviderKey, "revision": item.Revision, "kind": item.Kind, "scope": item.Scope,
		"title": item.Title, "content": item.Content, "applies_to": item.AppliesTo, "uncertainty": item.Uncertainty,
		"valid_from": item.ValidFrom, "valid_until": item.ValidUntil, "source": item.Source,
	}
}

func excludedMemoryReceipt(candidate scoredMemoryItem, reason string) agentsdk.MemoryContextReceipt {
	return agentsdk.MemoryContextReceipt{ProviderKey: candidate.item.ProviderKey, MemoryID: candidate.item.ID, Revision: candidate.item.Revision, Decision: agentsdk.AgentMemoryDecisionExcluded, Reasons: []string{reason}, Score: candidate.score, ContentHash: memoryItemHash(candidate.item)}
}

func memoryItemHash(item agentsdk.MemoryContextItem) string {
	raw, _ := json.Marshal(memoryPromptProjection(item))
	return hashBytes(raw)
}

func hashText(value string) string { return hashBytes([]byte(value)) }
func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
