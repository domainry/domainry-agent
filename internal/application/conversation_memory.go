package application

import (
	"context"
	"math"
	"strings"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

const (
	conversationMemoryContextItems = 8
	conversationMemoryContextBytes = 4096
)

func normalizeConversationMemory(memory sdk.ConversationMemory) sdk.ConversationMemory {
	if memory.Kind == "" {
		memory.Kind = sdk.ConversationMemoryKindUserPreference
	}
	if memory.Scope.Kind == "" {
		memory.Scope.Kind = sdk.ConversationMemoryScopeWorkspace
	}
	if memory.Status == "" {
		memory.Status = sdk.AgentMemoryStatusActive
	}
	memory.AppliesTo = normalizeMemoryTopics(memory.AppliesTo)
	if memory.Source != nil {
		source := *memory.Source
		source.CapturedAt = source.CapturedAt.UTC()
		memory.Source = &source
	}
	if memory.Correction != nil {
		correction := *memory.Correction
		memory.Correction = &correction
	}
	return memory
}

func normalizeMemoryTopics(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func conversationMemoryKind(kind string) bool {
	switch kind {
	case sdk.ConversationMemoryKindUserPreference, sdk.ConversationMemoryKindProjectFact, sdk.ConversationMemoryKindTaskContext:
		return true
	}
	return false
}

func conversationMemoryScopeKind(kind string) bool {
	switch kind {
	case sdk.ConversationMemoryScopeWorkspace, sdk.ConversationMemoryScopeProject, sdk.ConversationMemoryScopeConversation, sdk.ConversationMemoryScopeTask:
		return true
	}
	return false
}

func conversationMemoryStatus(status string) bool {
	switch status {
	case sdk.AgentMemoryStatusCandidate, sdk.AgentMemoryStatusActive, sdk.AgentMemoryStatusSuperseded, sdk.AgentMemoryStatusContradicted, sdk.AgentMemoryStatusExpired, sdk.AgentMemoryStatusRetracted:
		return true
	}
	return false
}

func (s *ConversationService) prepareConversationMemoryWrite(ctx context.Context, in sdk.ConversationMemoryWrite, a sdk.ConversationAuthority) (sdk.ConversationMemoryWrite, error) {
	preservedAutomaticSource := false
	in.ID = strings.TrimSpace(in.ID)
	in.Kind = strings.TrimSpace(in.Kind)
	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	in.Scope.Kind = strings.TrimSpace(in.Scope.Kind)
	in.Scope.ProjectID = strings.TrimSpace(in.Scope.ProjectID)
	in.Scope.ConversationID = strings.TrimSpace(in.Scope.ConversationID)
	in.Scope.TaskID = strings.TrimSpace(in.Scope.TaskID)
	in.Uncertainty = strings.TrimSpace(in.Uncertainty)
	in.CorrectionReason = strings.TrimSpace(in.CorrectionReason)
	in.Status = strings.TrimSpace(in.Status)
	in.SupersedesID = strings.TrimSpace(in.SupersedesID)
	in.AppliesTo = normalizeMemoryTopics(in.AppliesTo)
	if in.Kind == "" {
		in.Kind = sdk.ConversationMemoryKindUserPreference
	}
	if in.Scope.Kind == "" {
		in.Scope.Kind = sdk.ConversationMemoryScopeWorkspace
	}
	if in.Status == "" {
		in.Status = sdk.AgentMemoryStatusActive
	}
	if !conversationKey(in.ID) || !conversationText(in.Title, 128, true) || !conversationText(in.Content, 512, true) || in.ExpectedRevision < 0 || !conversationMemoryKind(in.Kind) || !conversationMemoryScopeKind(in.Scope.Kind) || len(in.AppliesTo) > 16 || !conversationText(in.Uncertainty, 512, false) || !conversationText(in.CorrectionReason, 512, false) || math.IsNaN(in.Importance) || math.IsInf(in.Importance, 0) || in.Importance < 0 || in.Importance > 1 || math.IsNaN(in.Confidence) || math.IsInf(in.Confidence, 0) || in.Confidence < 0 || in.Confidence > 1 || !conversationMemoryStatus(in.Status) || in.SupersedesID != "" && (!conversationKey(in.SupersedesID) || in.SupersedesID == in.ID) || in.ValidFrom != nil && in.ValidUntil != nil && !in.ValidUntil.After(*in.ValidFrom) {
		return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_invalid")
	}
	for _, topic := range in.AppliesTo {
		if !conversationText(topic, 64, true) {
			return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_invalid")
		}
	}
	if in.Kind == sdk.ConversationMemoryKindTaskContext && (in.Scope.Kind == sdk.ConversationMemoryScopeWorkspace || in.Scope.Kind == sdk.ConversationMemoryScopeProject) {
		return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_scope_invalid")
	}
	switch in.Scope.Kind {
	case sdk.ConversationMemoryScopeWorkspace:
		in.Scope.ProjectID = ""
		in.Scope.ConversationID = ""
		in.Scope.TaskID = ""
	case sdk.ConversationMemoryScopeProject:
		if in.Scope.ProjectID == "" {
			return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_scope_invalid")
		}
		in.Scope.ConversationID = ""
		in.Scope.TaskID = ""
	case sdk.ConversationMemoryScopeConversation:
		if in.Scope.ConversationID == "" {
			return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_scope_invalid")
		}
		if _, err := s.repo.Get(ctx, in.Scope.ConversationID, a); err != nil {
			return sdk.ConversationMemoryWrite{}, err
		}
		in.Scope.ProjectID = ""
		in.Scope.TaskID = ""
	case sdk.ConversationMemoryScopeTask:
		if in.Scope.TaskID == "" {
			return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_scope_invalid")
		}
		tasks, ok := s.repo.(persistence.ConversationTaskReadRepository)
		if !ok {
			return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_scope_invalid")
		}
		task, err := tasks.ConversationTask(ctx, in.Scope.TaskID, a)
		if err != nil {
			return sdk.ConversationMemoryWrite{}, err
		}
		in.Scope.ProjectID = ""
		in.Scope.ConversationID = task.SourceConversationID
	}
	if in.ExpectedRevision > 0 {
		memories, err := s.repo.Memories(ctx, a)
		if err != nil {
			return sdk.ConversationMemoryWrite{}, err
		}
		for _, memory := range memories {
			memory = normalizeConversationMemory(memory)
			if memory.ID != in.ID {
				continue
			}
			if in.Source == nil {
				in.Source = memory.Source
				preservedAutomaticSource = in.Source != nil && in.Source.Kind == "automatic_candidate"
			}
			break
		}
	}
	now := time.Now().UTC()
	if in.Source == nil {
		in.Source = &sdk.ConversationMemorySource{Kind: "manual", CapturedAt: now}
	} else {
		source := *in.Source
		source.Kind = strings.TrimSpace(source.Kind)
		source.ProviderKey = strings.TrimSpace(source.ProviderKey)
		source.SourceID = strings.TrimSpace(source.SourceID)
		source.SourceRevision = strings.TrimSpace(source.SourceRevision)
		source.URI = strings.TrimSpace(source.URI)
		source.ConversationID = strings.TrimSpace(source.ConversationID)
		source.MessageID = strings.TrimSpace(source.MessageID)
		source.RunID = strings.TrimSpace(source.RunID)
		source.TaskID = strings.TrimSpace(source.TaskID)
		source.ArtifactID = strings.TrimSpace(source.ArtifactID)
		source.FeedbackID = strings.TrimSpace(source.FeedbackID)
		if !preservedAutomaticSource {
			source.CapturedAt = now
		}
		in.Source = &source
	}
	if in.CorrectionReason != "" {
		in.Source.Kind = "user_correction"
		in.Source.ProviderKey = ""
		in.Source.SourceID = ""
		in.Source.SourceRevision = ""
		in.Source.URI = ""
		in.Source.CapturedAt = now
		preservedAutomaticSource = false
	}
	if !preservedAutomaticSource {
		if err := s.validateConversationMemorySource(ctx, *in.Source, a); err != nil {
			return sdk.ConversationMemoryWrite{}, err
		}
	} else if len(in.Source.ProviderKey) > 128 || len(in.Source.SourceID) > 512 || len(in.Source.SourceRevision) > 128 || len(in.Source.URI) > 2048 {
		return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_source_invalid")
	}
	return in, nil
}

func (s *ConversationService) validateConversationMemorySource(ctx context.Context, source sdk.ConversationMemorySource, a sdk.ConversationAuthority) error {
	if source.ProviderKey != "" || source.SourceID != "" || source.SourceRevision != "" || source.URI != "" {
		return conversationFailure("bad_request", "memory_source_invalid")
	}
	switch source.Kind {
	case "manual", "user_request", "user_correction", "task_feedback":
	default:
		return conversationFailure("bad_request", "memory_source_invalid")
	}
	if source.MessageID != "" && source.ConversationID == "" || source.RunID != "" && source.ConversationID == "" || source.ArtifactID == "" && source.ArtifactVersion != 0 || source.ArtifactID != "" && source.ArtifactVersion < 1 || source.Kind == "task_feedback" && (source.TaskID == "" || source.FeedbackID == "") || source.FeedbackID != "" && source.Kind != "task_feedback" {
		return conversationFailure("bad_request", "memory_source_invalid")
	}
	if source.ConversationID != "" {
		if _, err := s.repo.Get(ctx, source.ConversationID, a); err != nil {
			return err
		}
	}
	if source.MessageID != "" {
		history, ok := s.repo.(persistence.ConversationHistoryRepository)
		if !ok {
			return conversationFailure("bad_request", "memory_source_invalid")
		}
		message, err := history.HistoryMessage(ctx, source.ConversationID, source.MessageID, a)
		if err != nil {
			return err
		}
		if source.RunID != "" && message.RunID != source.RunID {
			return conversationFailure("bad_request", "memory_source_invalid")
		}
	}
	if source.RunID != "" {
		if _, err := s.repo.Run(ctx, source.ConversationID, source.RunID, a); err != nil {
			return err
		}
	}
	var sourceTask *sdk.ConversationTask
	if source.TaskID != "" {
		tasks, ok := s.repo.(persistence.ConversationTaskReadRepository)
		if !ok {
			return conversationFailure("bad_request", "memory_source_invalid")
		}
		task, err := tasks.ConversationTask(ctx, source.TaskID, a)
		if err != nil {
			return err
		}
		if source.ConversationID != "" && source.ConversationID != task.SourceConversationID && source.ConversationID != task.ExecutionConversationID || source.RunID != "" && source.RunID != task.SourceRunID && source.RunID != task.ExecutionRunID {
			return conversationFailure("bad_request", "memory_source_invalid")
		}
		sourceTask = &task
	}
	if source.ArtifactID != "" {
		artifact, err := s.Artifact(ctx, source.ArtifactID, source.ArtifactVersion, a)
		if err != nil {
			return err
		}
		if source.ConversationID != "" && artifact.Artifact.SourceConversationID != source.ConversationID || source.RunID != "" && artifact.Artifact.SourceRunID != source.RunID || sourceTask != nil && artifact.Artifact.SourceRunID != sourceTask.ExecutionRunID {
			return conversationFailure("bad_request", "memory_source_invalid")
		}
	}
	if source.FeedbackID != "" {
		repo, err := s.improvementRepository()
		if err != nil {
			return err
		}
		feedbacks, err := repo.ConversationCapabilityFeedbacks(ctx, []string{source.FeedbackID}, a)
		if err != nil {
			return err
		}
		if len(feedbacks) != 1 || feedbacks[0].ID != source.FeedbackID || feedbacks[0].TaskID != source.TaskID || source.RunID != "" && feedbacks[0].RunID != source.RunID || source.ArtifactID != "" && (feedbacks[0].ArtifactID != source.ArtifactID || feedbacks[0].ArtifactVersion != source.ArtifactVersion) {
			return conversationFailure("bad_request", "memory_source_invalid")
		}
	}
	return nil
}

func memoryApplicable(memory sdk.ConversationMemory, projectID, conversationID, taskID string) bool {
	switch memory.Scope.Kind {
	case sdk.ConversationMemoryScopeWorkspace:
		return true
	case sdk.ConversationMemoryScopeProject:
		return projectID != "" && memory.Scope.ProjectID == projectID
	case sdk.ConversationMemoryScopeConversation:
		return conversationID != "" && memory.Scope.ConversationID == conversationID
	case sdk.ConversationMemoryScopeTask:
		return taskID != "" && memory.Scope.TaskID == taskID
	}
	return false
}

func memoryLiteralMatch(memory sdk.ConversationMemory, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	parts := []string{memory.Title, memory.Content, memory.Kind, memory.Scope.Kind, memory.Uncertainty}
	parts = append(parts, memory.AppliesTo...)
	return strings.Contains(strings.ToLower(strings.Join(parts, "\n")), query)
}

func (s *ConversationService) conversationMemorySnapshot(ctx context.Context, claim persistence.ConversationClaim, conversation sdk.Conversation) (sdk.MemoryContextSnapshot, error) {
	query := ""
	if claim.Run.UserSeq > 0 {
		messages, historyErr := s.repo.History(ctx, claim.Run.ConversationID, claim.Run.UserSeq-1, claim.Run.UserSeq, 1, claim.Authority)
		if historyErr != nil {
			return sdk.MemoryContextSnapshot{}, historyErr
		}
		if len(messages) == 1 {
			query = messages[0].Content
		}
	}
	taskID := ""
	if claim.Run.BackgroundTask != nil {
		taskID = claim.Run.BackgroundTask.TaskID
	}
	policy := sdk.AgentMemoryPolicy{
		ContractVersion: sdk.AgentMemoryPolicyVersion, Enabled: true, ReferenceSaved: true, ReferenceHistory: true,
		RetrievalMode: sdk.AgentMemoryRetrievalHybrid,
		CandidateMode: sdk.AgentMemoryCandidateDisabled, MaxItems: conversationMemoryContextItems, MaxBytes: conversationMemoryContextBytes,
	}
	providers := NewDefaultMemoryContextProviders(s.repo, s.options.MemoryRanker)
	external := conversationExternalMemoryProviders(s.options)
	if len(external) != 0 {
		providers = append(providers, external...)
		policy.ProviderKeys = []string{"*"}
	}
	request := sdk.MemoryContextRequest{
		Consumer: sdk.AgentMemoryConsumerConversation, RuntimeID: claim.Authority.RuntimeID, WorkspaceID: claim.Authority.WorkspaceID, UserID: claim.Authority.UserID, RoleKey: claim.Authority.RoleKey,
		ProjectID: conversation.MemoryPreference.ProjectID, ConversationID: claim.Run.ConversationID, TaskID: taskID, RunID: claim.Run.ID,
		Query: query, Policy: policy, Preference: conversation.MemoryPreference,
	}
	request = effectiveMemoryContextRequest(request)
	results := make([]sdk.MemoryContextResult, 0, len(providers))
	for _, provider := range providers {
		result, err := provider.ResolveMemoryContext(ctx, request)
		if err != nil {
			return sdk.MemoryContextSnapshot{}, err
		}
		results = append(results, result)
	}
	now := time.Now().UTC()
	ranked, ranking, err := rankMemoryContextResults(ctx, request, results, s.options.MemoryRanker, now)
	if err != nil {
		return sdk.MemoryContextSnapshot{}, err
	}
	snapshot, err := selectTaskMemorySnapshot(request, ranked, now)
	if err != nil {
		return sdk.MemoryContextSnapshot{}, err
	}
	snapshot.Ranking = ranking
	return snapshot, nil
}

func conversationExternalMemoryProviders(options ConversationOptions) []sdk.MemoryContextProvider {
	providers := make([]sdk.MemoryContextProvider, 0, len(options.MemoryProviders)+1)
	appendProvider := func(provider sdk.MemoryContextProvider) {
		if provider == nil {
			return
		}
		providers = append(providers, provider)
	}
	for _, provider := range options.MemoryProviders {
		appendProvider(provider)
	}
	appendProvider(options.MemoryProvider)
	return providers
}

func memorySourceProjection(source *sdk.ConversationMemorySource) *sdk.ConversationMemorySource {
	if source == nil {
		return nil
	}
	out := *source
	return &out
}

func memoryContextProjection(items []sdk.ConversationMemory) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, memory := range items {
		out = append(out, map[string]any{
			"id": memory.ID, "kind": memory.Kind, "title": memory.Title, "content": memory.Content,
			"scope": memory.Scope, "applies_to": memory.AppliesTo, "source": memorySourceProjection(memory.Source),
			"correction": memory.Correction, "uncertainty": memory.Uncertainty, "updated_at": memory.UpdatedAt,
			"status": memory.Status, "importance": memory.Importance, "confidence": memory.Confidence,
			"valid_from": memory.ValidFrom, "valid_until": memory.ValidUntil, "last_verified_at": memory.LastVerifiedAt,
		})
	}
	return out
}

func memorySnapshotProjection(snapshot sdk.MemoryContextSnapshot) map[string]any {
	items := make([]map[string]any, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		items = append(items, memoryPromptProjection(item))
	}
	return map[string]any{"snapshot_id": snapshot.ID, "content_hash": snapshot.ContentHash, "items": items}
}
