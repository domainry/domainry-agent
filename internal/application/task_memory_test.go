package application

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type historyMemoryRepositoryStub struct {
	queries []string
}

type pagedHistoryMemoryRepositoryStub struct {
	cursors []string
}

type candidateMemoryRepositoryStub struct {
	items []agentsdk.ConversationMemory
}

type semanticMemoryRankerStub struct {
	seen   []agentsdk.MemoryContextItem
	scores map[string]float64
	fail   bool
}

func (r *semanticMemoryRankerStub) RankMemoryContext(_ context.Context, request agentsdk.MemoryContextRankRequest) (agentsdk.MemoryContextRankResult, error) {
	r.seen = append([]agentsdk.MemoryContextItem(nil), request.Items...)
	if r.fail {
		return agentsdk.MemoryContextRankResult{}, fmt.Errorf("ranker unavailable")
	}
	scores := make([]agentsdk.MemoryContextRankScore, 0, len(request.Items))
	for _, item := range request.Items {
		relevance := r.scores[item.ID]
		if relevance == 0 {
			relevance = 0.1
		}
		scores = append(scores, agentsdk.MemoryContextRankScore{ProviderKey: item.ProviderKey, MemoryID: item.ID, Revision: item.Revision, Relevance: relevance})
	}
	return agentsdk.MemoryContextRankResult{Scores: scores}, nil
}

func (r *candidateMemoryRepositoryStub) Memories(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationMemory, error) {
	return append([]agentsdk.ConversationMemory(nil), r.items...), nil
}

func (r *candidateMemoryRepositoryStub) WriteMemory(_ context.Context, in agentsdk.ConversationMemoryWrite, _ agentsdk.ConversationAuthority) (agentsdk.ConversationMemory, error) {
	now := time.Now().UTC()
	memory := agentsdk.ConversationMemory{
		ID: in.ID, Kind: in.Kind, Title: in.Title, Content: in.Content, Enabled: in.Enabled, Status: in.Status, Scope: in.Scope,
		AppliesTo: append([]string(nil), in.AppliesTo...), Source: in.Source, Importance: in.Importance, Confidence: in.Confidence,
		SupersedesID: in.SupersedesID, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	r.items = append(r.items, memory)
	return memory, nil
}

func (r *historyMemoryRepositoryStub) SearchHistory(_ context.Context, in agentsdk.ConversationHistorySearch, _ agentsdk.ConversationAuthority) (agentsdk.ConversationHistorySearchResult, error) {
	r.queries = append(r.queries, in.Query)
	now := time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)
	return agentsdk.ConversationHistorySearchResult{Items: []agentsdk.ConversationHistoryHit{
		{ConversationID: "current", MessageID: "current-message", Seq: 1, Role: "user", Excerpt: "current duplicate", CreatedAt: now},
		{ConversationID: "older", MessageID: "assistant-message", Seq: 2, Role: "assistant", Excerpt: "untrusted assistant claim", CreatedAt: now.Add(-2 * time.Hour)},
		{ConversationID: "older", MessageID: "older-message", Seq: 3, Role: "user", Excerpt: "Agent 架构使用 Go", CreatedAt: now.Add(-time.Hour)},
	}, Complete: true}, nil
}

func (*historyMemoryRepositoryStub) HistoryMessage(context.Context, string, string, agentsdk.ConversationAuthority) (agentsdk.ConversationMessage, error) {
	return agentsdk.ConversationMessage{}, nil
}

func (r *pagedHistoryMemoryRepositoryStub) SearchHistory(_ context.Context, in agentsdk.ConversationHistorySearch, _ agentsdk.ConversationAuthority) (agentsdk.ConversationHistorySearchResult, error) {
	r.cursors = append(r.cursors, in.Cursor)
	if in.Cursor == "" {
		return agentsdk.ConversationHistorySearchResult{NextCursor: "older-page", Complete: false}, nil
	}
	return agentsdk.ConversationHistorySearchResult{Items: []agentsdk.ConversationHistoryHit{{ConversationID: "older", MessageID: "deep-match", Seq: 1, Role: "user", Excerpt: "Agent architecture uses Go", CreatedAt: time.Now().UTC()}}, Complete: true}, nil
}

func (*pagedHistoryMemoryRepositoryStub) HistoryMessage(context.Context, string, string, agentsdk.ConversationAuthority) (agentsdk.ConversationMessage, error) {
	return agentsdk.ConversationMessage{}, nil
}

func (*historyMemoryRepositoryStub) RecentMemoryHistory(_ context.Context, _ string, _ int, _ agentsdk.ConversationAuthority) ([]agentsdk.ConversationHistoryHit, error) {
	now := time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)
	return []agentsdk.ConversationHistoryHit{{ConversationID: "older", MessageID: "semantic-history", Seq: 4, Role: "user", Excerpt: "Primary workloads execute in Tokyo", CreatedAt: now}}, nil
}

func TestConversationHistoryMemoryProviderIsOwnerScopedAndProjectIsolated(t *testing.T) {
	repository := &historyMemoryRepositoryStub{}
	provider := &conversationHistoryMemoryContextProvider{repository: repository}
	request := agentsdk.MemoryContextRequest{
		RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", ConversationID: "current", RunID: "run",
		Query: "继续检查 Agent 架构", Policy: agentsdk.AgentMemoryPolicy{Enabled: true, ReferenceHistory: true, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid},
	}
	result, err := provider.ResolveMemoryContext(t.Context(), request)
	if err != nil || len(repository.queries) == 0 || len(result.Items) != 2 {
		t.Fatalf("queries=%v result=%+v err=%v", repository.queries, result, err)
	}
	var item agentsdk.MemoryContextItem
	for _, candidate := range result.Items {
		if candidate.Source.SourceID == "older-message" {
			item = candidate
		}
	}
	if item.ProviderKey != agentsdk.AgentMemoryProviderHistory || item.ID != "history_"+hashText("older\x00older-message")[:32] || item.Source.URI != "conversation://older/messages/older-message" {
		t.Fatalf("history provenance was not preserved: %+v", item)
	}
	request.Preference = agentsdk.AgentMemoryPreference{ProjectOnly: true, ProjectID: "project-a"}
	isolated, err := provider.ResolveMemoryContext(t.Context(), request)
	if err != nil || len(isolated.Items) != 0 {
		t.Fatalf("workspace history entered project-only memory: %+v err=%v", isolated, err)
	}
}

func TestConversationHistoryProviderAddsRecentCandidatesForSemanticRanking(t *testing.T) {
	repository := &historyMemoryRepositoryStub{}
	provider := &conversationHistoryMemoryContextProvider{repository: repository, recent: repository, semantic: true}
	request := agentsdk.MemoryContextRequest{
		RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", ConversationID: "current", RunID: "run",
		Query: "Where is the Asian production region?", Policy: agentsdk.AgentMemoryPolicy{Enabled: true, ReferenceHistory: true, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid, MaxItems: 8, MaxBytes: 4096},
	}
	result, err := provider.ResolveMemoryContext(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	semanticID := ""
	for _, item := range result.Items {
		if item.Source.SourceID == "semantic-history" {
			semanticID = item.ID
		}
	}
	if semanticID == "" {
		t.Fatalf("recent semantic candidate was not added: %+v", result.Items)
	}
	ranker := &semanticMemoryRankerStub{scores: map[string]float64{semanticID: 0.95}}
	ranked, ranking, err := rankMemoryContextResults(t.Context(), request, []agentsdk.MemoryContextResult{result}, ranker, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if ranking == nil || ranking.ScoreCount == 0 || ranking.ScoresHash == "" {
		t.Fatalf("semantic history ranking evidence missing: %+v", ranking)
	}
	snapshot, err := selectTaskMemorySnapshot(request, ranked, time.Now().UTC())
	if err != nil || len(snapshot.Items) == 0 || snapshot.Items[0].ID != semanticID {
		t.Fatalf("semantic history was not recalled: %+v err=%v", snapshot, err)
	}
}

func TestLexicalMemoryRetrievalSkipsSemanticExpansionAndRanker(t *testing.T) {
	repository := &historyMemoryRepositoryStub{}
	provider := &conversationHistoryMemoryContextProvider{repository: repository, recent: repository, semantic: true}
	request := agentsdk.MemoryContextRequest{
		RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", ConversationID: "current", RunID: "run",
		Query:      "Where is the Asian production region?",
		Policy:     agentsdk.AgentMemoryPolicy{Enabled: true, ReferenceHistory: true, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid, MaxItems: 8, MaxBytes: 4096},
		Preference: agentsdk.AgentMemoryPreference{RetrievalMode: agentsdk.AgentMemoryRetrievalLexical},
	}
	request = effectiveMemoryContextRequest(request)
	if request.Policy.RetrievalMode != agentsdk.AgentMemoryRetrievalLexical {
		t.Fatalf("provider request was not normalized to lexical: %+v", request.Policy)
	}
	result, err := provider.ResolveMemoryContext(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.Source.SourceID == "semantic-history" {
			t.Fatalf("lexical retrieval added a semantic-only candidate: %+v", result.Items)
		}
	}
	ranker := &semanticMemoryRankerStub{scores: map[string]float64{}}
	ranked, ranking, err := rankMemoryContextResults(t.Context(), request, []agentsdk.MemoryContextResult{result}, ranker, time.Now().UTC())
	if err != nil || ranking != nil || len(ranker.seen) != 0 {
		t.Fatalf("lexical retrieval invoked semantic ranker: ranking=%+v seen=%+v err=%v", ranking, ranker.seen, err)
	}
	if !reflect.DeepEqual(ranked, []agentsdk.MemoryContextResult{result}) {
		t.Fatalf("lexical results changed while bypassing ranker: %+v", ranked)
	}
}

func TestConversationHistoryProviderFollowsLiteralSearchCursor(t *testing.T) {
	repository := &pagedHistoryMemoryRepositoryStub{}
	provider := &conversationHistoryMemoryContextProvider{repository: repository}
	request := agentsdk.MemoryContextRequest{RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", ConversationID: "current", RunID: "run", Query: "Agent architecture", Policy: agentsdk.AgentMemoryPolicy{Enabled: true, ReferenceHistory: true, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid}}
	result, err := provider.ResolveMemoryContext(t.Context(), request)
	if err != nil || len(repository.cursors) < 2 || len(result.Items) != 1 || result.Items[0].Source.SourceID != "deep-match" {
		t.Fatalf("cursors=%v result=%+v err=%v", repository.cursors, result, err)
	}
}

func TestProjectOnlyPersonalMemoryRecallsOnlyMatchingProject(t *testing.T) {
	now := time.Now().UTC()
	repository := &candidateMemoryRepositoryStub{items: []agentsdk.ConversationMemory{
		{ID: "workspace", Kind: agentsdk.ConversationMemoryKindProjectFact, Title: "Workspace", Content: "Global fact", Enabled: true, Status: agentsdk.AgentMemoryStatusActive, Scope: agentsdk.ConversationMemoryScope{Kind: agentsdk.ConversationMemoryScopeWorkspace}, Revision: 1, UpdatedAt: now},
		{ID: "project-a", Kind: agentsdk.ConversationMemoryKindProjectFact, Title: "Project A", Content: "Uses Go", Enabled: true, Status: agentsdk.AgentMemoryStatusActive, Scope: agentsdk.ConversationMemoryScope{Kind: agentsdk.ConversationMemoryScopeProject, ProjectID: "project-a"}, Revision: 1, UpdatedAt: now},
		{ID: "project-b", Kind: agentsdk.ConversationMemoryKindProjectFact, Title: "Project B", Content: "Uses Rust", Enabled: true, Status: agentsdk.AgentMemoryStatusActive, Scope: agentsdk.ConversationMemoryScope{Kind: agentsdk.ConversationMemoryScopeProject, ProjectID: "project-b"}, Revision: 1, UpdatedAt: now},
	}}
	provider := &personalMemoryContextProvider{repository: repository}
	request := agentsdk.MemoryContextRequest{RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", ProjectID: "project-a", RunID: "run", Policy: agentsdk.AgentMemoryPolicy{Enabled: true, ReferenceSaved: true, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid}, Preference: agentsdk.AgentMemoryPreference{ProjectOnly: true, ProjectID: "project-a"}}
	result, err := provider.ResolveMemoryContext(t.Context(), request)
	if err != nil || len(result.Items) != 1 || result.Items[0].ID != "project-a" || result.Items[0].ScopeID != "project-a" {
		t.Fatalf("project-only result=%+v err=%v", result, err)
	}
}

func TestMemorySelectorRejectsSuppressedExpiredAndContradictedItems(t *testing.T) {
	now := time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	item := func(id string) agentsdk.MemoryContextItem {
		return agentsdk.MemoryContextItem{
			ID: id, ProviderKey: "business", Revision: "1", Kind: "case_fact", Scope: agentsdk.ConversationMemoryScopeWorkspace,
			Title: "Agent architecture", Content: "Agent architecture uses Go", Status: agentsdk.AgentMemoryStatusActive,
			Source: agentsdk.AgentMemorySource{ProviderKey: "business", SourceID: id}, UpdatedAt: now,
		}
	}
	allowed := item("allowed")
	suppressed := item("suppressed")
	suppressed.DoNotMention = true
	stale := item("expired")
	stale.ValidUntil = &expired
	contradicted := item("contradicted")
	contradicted.Status = agentsdk.AgentMemoryStatusContradicted
	request := agentsdk.MemoryContextRequest{
		Consumer: agentsdk.AgentMemoryConsumerTask, WorkspaceID: "workspace", UserID: "user", RunID: "run", Query: "Agent architecture",
		Policy: agentsdk.AgentMemoryPolicy{Enabled: true, ProviderKeys: []string{"business"}, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid, MaxItems: 8, MaxBytes: 4096},
	}
	snapshot, err := selectTaskMemorySnapshot(request, []agentsdk.MemoryContextResult{{ProviderKey: "business", Items: []agentsdk.MemoryContextItem{allowed, suppressed, stale, contradicted}}}, now)
	if err != nil || len(snapshot.Items) != 1 || snapshot.Items[0].ID != allowed.ID || len(snapshot.Receipts) != 4 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	reasons := map[string]string{}
	for _, receipt := range snapshot.Receipts {
		if len(receipt.Reasons) != 0 {
			reasons[receipt.MemoryID] = receipt.Reasons[0]
		}
	}
	if reasons["suppressed"] != "mention_suppressed" || reasons["expired"] != "expired" || reasons["contradicted"] != "status_contradicted" {
		t.Fatalf("exclusion receipts lost their reasons: %+v", reasons)
	}
}

func TestMemoryCandidateReceiptsCannotClaimUnknownCandidates(t *testing.T) {
	candidates := map[string]bool{"candidate-a": true}
	valid := []agentsdk.AgentMemoryCandidateReceipt{{CandidateID: "candidate-a", Disposition: agentsdk.AgentMemoryCandidateReviewRequired, MemoryID: "memory-a", Revision: "1"}}
	if !validMemoryCandidateReceipts(valid, candidates) {
		t.Fatal("valid candidate receipt was rejected")
	}
	invalid := [][]agentsdk.AgentMemoryCandidateReceipt{
		{{CandidateID: "candidate-b", Disposition: agentsdk.AgentMemoryCandidateReviewRequired, MemoryID: "memory-b", Revision: "1"}},
		{{CandidateID: "candidate-a", Disposition: "invented", MemoryID: "memory-a", Revision: "1"}},
		{{CandidateID: "candidate-a", Disposition: agentsdk.AgentMemoryCandidateFailed}},
		{{CandidateID: "candidate-a", Disposition: agentsdk.AgentMemoryCandidateAutoApproved}},
	}
	for index, receipts := range invalid {
		if validMemoryCandidateReceipts(receipts, candidates) {
			t.Fatalf("invalid receipt set %d was accepted: %+v", index, receipts)
		}
	}
}

func TestProjectOnlyMemoryDropsWorkspaceCandidates(t *testing.T) {
	request := agentsdk.MemoryContextRequest{
		RunID: "run-a", TaskID: "task-a", ProjectID: "project-a",
		Preference: agentsdk.AgentMemoryPreference{ProjectOnly: true, ProjectID: "project-a"},
	}
	raw := `{"candidates":[{"kind":"user_preference","scope":"workspace","title":"Style","content":"Be concise","applies_to":[],"confidence":0.9},{"kind":"project_fact","scope":"project","title":"Runtime","content":"Uses Go","applies_to":["runtime"],"confidence":0.8}]}`
	candidates, err := decodeConversationMemoryCandidates(raw, request)
	if err != nil || len(candidates) != 1 || candidates[0].Scope != "project" || candidates[0].ScopeID != "project-a" {
		t.Fatalf("project-only candidate isolation failed: %+v err=%v", candidates, err)
	}

	workspaceCandidate := agentsdk.AgentMemoryCandidate{
		ID: "candidate-a", Kind: agentsdk.ConversationMemoryKindUserPreference, Scope: agentsdk.ConversationMemoryScopeWorkspace,
		Title: "Style", Content: "Be concise", Confidence: 0.9,
		Source: agentsdk.AgentMemorySource{ProviderKey: "agent_task", SourceID: "task-a", CapturedAt: time.Now().UTC()},
	}
	run := agentmodel.AgentTaskRun{ID: "task-a", MemoryPreference: request.Preference}
	policy := agentsdk.AgentMemoryPolicy{Enabled: true, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid, CandidateMode: agentsdk.AgentMemoryCandidatePersonal}
	if validateTaskMemoryCandidates([]agentsdk.AgentMemoryCandidate{workspaceCandidate}, run, policy) {
		t.Fatal("workspace candidate crossed a project-only task boundary")
	}
}

func TestMemorySelectorRejectsProviderIdentitySpoofing(t *testing.T) {
	now := time.Now().UTC()
	item := agentsdk.MemoryContextItem{
		ID: "fact-a", ProviderKey: "other", Revision: "1", Kind: "fact", Scope: agentsdk.ConversationMemoryScopeWorkspace,
		Title: "Runtime", Content: "Uses Go", Status: agentsdk.AgentMemoryStatusActive,
		Source: agentsdk.AgentMemorySource{ProviderKey: "other", SourceID: "fact-a"}, UpdatedAt: now,
	}
	request := agentsdk.MemoryContextRequest{
		Consumer: agentsdk.AgentMemoryConsumerTask, WorkspaceID: "workspace", UserID: "user", RunID: "run", Query: "runtime",
		Policy: agentsdk.AgentMemoryPolicy{Enabled: true, ProviderKeys: []string{"business", "other"}, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid},
	}
	if _, err := selectTaskMemorySnapshot(request, []agentsdk.MemoryContextResult{{ProviderKey: "business", Items: []agentsdk.MemoryContextItem{item}}}, now); err == nil {
		t.Fatal("provider was allowed to claim another provider identity")
	}
}

func TestSemanticRankerFindsMeaningWithoutSeeingSuppressedMemory(t *testing.T) {
	now := time.Now().UTC()
	item := func(id, content string) agentsdk.MemoryContextItem {
		return agentsdk.MemoryContextItem{
			ID: id, ProviderKey: agentsdk.AgentMemoryProviderPersonal, Revision: "1", Kind: agentsdk.ConversationMemoryKindProjectFact,
			Scope: agentsdk.ConversationMemoryScopeWorkspace, Title: "Infrastructure", Content: content, Status: agentsdk.AgentMemoryStatusActive,
			Source: agentsdk.AgentMemorySource{ProviderKey: agentsdk.AgentMemoryProviderPersonal, SourceID: id}, UpdatedAt: now,
		}
	}
	match := item("semantic-match", "Primary workloads execute in Tokyo")
	hidden := item("hidden", "Secret workloads execute in Singapore")
	hidden.DoNotMention = true
	request := agentsdk.MemoryContextRequest{
		Consumer: agentsdk.AgentMemoryConsumerTask, WorkspaceID: "workspace", UserID: "user", RunID: "run", Query: "Where is the Asian production region?",
		Policy: agentsdk.AgentMemoryPolicy{Enabled: true, ReferenceSaved: true, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid, MaxItems: 8, MaxBytes: 4096},
	}
	ranker := &semanticMemoryRankerStub{scores: map[string]float64{"semantic-match": 0.95}}
	ranked, ranking, err := rankMemoryContextResults(t.Context(), request, []agentsdk.MemoryContextResult{{ProviderKey: agentsdk.AgentMemoryProviderPersonal, Items: []agentsdk.MemoryContextItem{match, hidden}}}, ranker, now)
	if err != nil || len(ranker.seen) != 1 || ranker.seen[0].ID != match.ID {
		t.Fatalf("ranker saw wrong candidate set: seen=%+v err=%v", ranker.seen, err)
	}
	if ranking == nil || ranking.CandidateCount != 1 || ranking.ScoreCount != 1 || ranking.ScoresHash == "" {
		t.Fatalf("semantic ranking evidence missing: %+v", ranking)
	}
	snapshot, err := selectTaskMemorySnapshot(request, ranked, now)
	if err != nil || len(snapshot.Items) != 1 || snapshot.Items[0].ID != match.ID || snapshot.Items[0].RelevanceSource != "semantic_ranker" {
		t.Fatalf("semantic memory was not selected: %+v err=%v", snapshot, err)
	}
	foundReason := false
	for _, receipt := range snapshot.Receipts {
		if receipt.MemoryID == match.ID && slices.Contains(receipt.Reasons, "semantic_relevance") {
			foundReason = true
		}
	}
	if !foundReason {
		t.Fatalf("semantic selection receipt was not auditable: %+v", snapshot.Receipts)
	}
}

func TestPersonalMemoryCandidateSinkDeduplicatesAndRequiresConflictReview(t *testing.T) {
	repository := &candidateMemoryRepositoryStub{}
	provider := &personalMemoryContextProvider{repository: repository}
	now := time.Now().UTC()
	candidate := func(id, kind, title, content string, confidence float64) agentsdk.AgentMemoryCandidate {
		return agentsdk.AgentMemoryCandidate{
			ID: id, Kind: kind, Scope: agentsdk.ConversationMemoryScopeWorkspace, Title: title, Content: content, Confidence: confidence,
			Source: agentsdk.AgentMemorySource{ProviderKey: "agent_task", SourceID: "task-a", CapturedAt: now}, RequiresReview: kind != agentsdk.ConversationMemoryKindUserPreference,
		}
	}
	request := agentsdk.AgentMemoryCandidateRequest{
		Context: agentsdk.MemoryContextRequest{
			RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user",
			Policy: agentsdk.AgentMemoryPolicy{Enabled: true, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid, CandidateMode: agentsdk.AgentMemoryCandidatePersonal},
		},
		Candidates: []agentsdk.AgentMemoryCandidate{
			candidate("preference-a", agentsdk.ConversationMemoryKindUserPreference, "Response style", "Be concise", 0.95),
			candidate("preference-duplicate", agentsdk.ConversationMemoryKindUserPreference, "Brevity", "Be concise", 0.95),
			candidate("preference-conflict", agentsdk.ConversationMemoryKindUserPreference, "Response style", "Explain every detail", 0.95),
			candidate("project-a", agentsdk.ConversationMemoryKindProjectFact, "Runtime", "Uses Go", 0.9),
		},
	}
	receipts, err := provider.SubmitMemoryCandidates(t.Context(), request)
	if err != nil || len(receipts) != 4 || len(repository.items) != 3 {
		t.Fatalf("receipts=%+v memories=%+v err=%v", receipts, repository.items, err)
	}
	if receipts[0].Disposition != agentsdk.AgentMemoryCandidateAutoApproved || receipts[1].Disposition != agentsdk.AgentMemoryCandidateDuplicate || receipts[2].Disposition != agentsdk.AgentMemoryCandidateReviewRequired || receipts[2].Reason != "possible_conflict" || receipts[3].Disposition != agentsdk.AgentMemoryCandidateReviewRequired {
		t.Fatalf("candidate policy was not applied: %+v", receipts)
	}
	if repository.items[0].Status != agentsdk.AgentMemoryStatusActive || repository.items[1].Status != agentsdk.AgentMemoryStatusCandidate || repository.items[1].SupersedesID != repository.items[0].ID || repository.items[2].Status != agentsdk.AgentMemoryStatusCandidate || repository.items[2].Source == nil || repository.items[2].Source.Kind != "automatic_candidate" {
		t.Fatalf("candidate lifecycle/provenance was not persisted: %+v", repository.items)
	}
}

func TestPersonalMemoryCandidateSinkUsesSemanticConflictCheck(t *testing.T) {
	now := time.Now().UTC()
	repository := &candidateMemoryRepositoryStub{items: []agentsdk.ConversationMemory{{
		ID: "existing-preference", Kind: agentsdk.ConversationMemoryKindUserPreference, Title: "Meal choices", Content: "No animal products",
		Enabled: true, Status: agentsdk.AgentMemoryStatusActive, Scope: agentsdk.ConversationMemoryScope{Kind: agentsdk.ConversationMemoryScopeWorkspace}, Revision: 1, UpdatedAt: now,
	}}}
	ranker := &semanticMemoryRankerStub{scores: map[string]float64{"existing-preference": 0.92}}
	provider := &personalMemoryContextProvider{repository: repository, ranker: ranker}
	request := agentsdk.AgentMemoryCandidateRequest{
		Context: agentsdk.MemoryContextRequest{RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", Policy: agentsdk.AgentMemoryPolicy{Enabled: true, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid, CandidateMode: agentsdk.AgentMemoryCandidatePersonal}},
		Candidates: []agentsdk.AgentMemoryCandidate{{
			ID: "candidate-diet", Kind: agentsdk.ConversationMemoryKindUserPreference, Scope: agentsdk.ConversationMemoryScopeWorkspace,
			Title: "Diet", Content: "Vegan meals only", Confidence: 0.95,
			Source: agentsdk.AgentMemorySource{ProviderKey: "conversation", SourceID: "run-a", CapturedAt: now},
		}},
	}
	receipts, err := provider.SubmitMemoryCandidates(t.Context(), request)
	if err != nil || len(receipts) != 1 || receipts[0].Disposition != agentsdk.AgentMemoryCandidateReviewRequired || receipts[0].Reason != "possible_semantic_conflict" || len(repository.items) != 2 || repository.items[1].Status != agentsdk.AgentMemoryStatusCandidate || repository.items[1].SupersedesID != "existing-preference" {
		t.Fatalf("semantic conflict was not held for review: receipts=%+v items=%+v err=%v", receipts, repository.items, err)
	}
}

func TestLexicalMemoryCandidateSinkSkipsSemanticConflictCheck(t *testing.T) {
	now := time.Now().UTC()
	repository := &candidateMemoryRepositoryStub{items: []agentsdk.ConversationMemory{{
		ID: "existing-preference", Kind: agentsdk.ConversationMemoryKindUserPreference, Title: "Meal choices", Content: "No animal products",
		Enabled: true, Status: agentsdk.AgentMemoryStatusActive, Scope: agentsdk.ConversationMemoryScope{Kind: agentsdk.ConversationMemoryScopeWorkspace}, Revision: 1, UpdatedAt: now,
	}}}
	ranker := &semanticMemoryRankerStub{scores: map[string]float64{"existing-preference": 0.92}}
	provider := &personalMemoryContextProvider{repository: repository, ranker: ranker}
	request := agentsdk.AgentMemoryCandidateRequest{
		Context: agentsdk.MemoryContextRequest{
			RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user",
			Policy:     agentsdk.AgentMemoryPolicy{Enabled: true, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid, CandidateMode: agentsdk.AgentMemoryCandidatePersonal},
			Preference: agentsdk.AgentMemoryPreference{RetrievalMode: agentsdk.AgentMemoryRetrievalLexical},
		},
		Candidates: []agentsdk.AgentMemoryCandidate{{
			ID: "candidate-diet", Kind: agentsdk.ConversationMemoryKindUserPreference, Scope: agentsdk.ConversationMemoryScopeWorkspace,
			Title: "Diet", Content: "Vegan meals only", Confidence: 0.95,
			Source: agentsdk.AgentMemorySource{ProviderKey: "conversation", SourceID: "run-a", CapturedAt: now},
		}},
	}
	receipts, err := provider.SubmitMemoryCandidates(t.Context(), request)
	if err != nil || len(receipts) != 1 || receipts[0].Disposition != agentsdk.AgentMemoryCandidateReviewRequired || receipts[0].Reason != "semantic_conflict_check_disabled" || len(ranker.seen) != 0 || len(repository.items) != 2 || repository.items[1].Enabled {
		t.Fatalf("lexical candidate path used semantic conflict check: receipts=%+v seen=%+v err=%v", receipts, ranker.seen, err)
	}
}

func TestMissingSemanticRankerKeepsComparablePreferenceForReview(t *testing.T) {
	now := time.Now().UTC()
	repository := &candidateMemoryRepositoryStub{items: []agentsdk.ConversationMemory{{
		ID: "existing-preference", Kind: agentsdk.ConversationMemoryKindUserPreference, Title: "Meal choices", Content: "No animal products",
		Enabled: true, Status: agentsdk.AgentMemoryStatusActive, Scope: agentsdk.ConversationMemoryScope{Kind: agentsdk.ConversationMemoryScopeWorkspace}, Revision: 1, UpdatedAt: now,
	}}}
	provider := &personalMemoryContextProvider{repository: repository}
	request := agentsdk.AgentMemoryCandidateRequest{
		Context: agentsdk.MemoryContextRequest{
			RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user",
			Policy: agentsdk.AgentMemoryPolicy{Enabled: true, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid, CandidateMode: agentsdk.AgentMemoryCandidatePersonal},
		},
		Candidates: []agentsdk.AgentMemoryCandidate{{
			ID: "candidate-diet", Kind: agentsdk.ConversationMemoryKindUserPreference, Scope: agentsdk.ConversationMemoryScopeWorkspace,
			Title: "Diet", Content: "Vegan meals only", Confidence: 0.95,
			Source: agentsdk.AgentMemorySource{ProviderKey: "conversation", SourceID: "run-a", CapturedAt: now},
		}},
	}
	receipts, err := provider.SubmitMemoryCandidates(t.Context(), request)
	if err != nil || len(receipts) != 1 || receipts[0].Disposition != agentsdk.AgentMemoryCandidateReviewRequired || receipts[0].Reason != "semantic_conflict_check_unavailable" || len(repository.items) != 2 || repository.items[1].Enabled {
		t.Fatalf("missing ranker allowed blind preference approval: receipts=%+v items=%+v err=%v", receipts, repository.items, err)
	}
}
