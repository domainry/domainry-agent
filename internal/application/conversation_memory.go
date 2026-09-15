package application

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

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
	case sdk.ConversationMemoryScopeWorkspace, sdk.ConversationMemoryScopeConversation, sdk.ConversationMemoryScopeTask:
		return true
	}
	return false
}

func (s *ConversationService) prepareConversationMemoryWrite(ctx context.Context, in sdk.ConversationMemoryWrite, a sdk.ConversationAuthority) (sdk.ConversationMemoryWrite, error) {
	in.ID = strings.TrimSpace(in.ID)
	in.Kind = strings.TrimSpace(in.Kind)
	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	in.Scope.Kind = strings.TrimSpace(in.Scope.Kind)
	in.Scope.ConversationID = strings.TrimSpace(in.Scope.ConversationID)
	in.Scope.TaskID = strings.TrimSpace(in.Scope.TaskID)
	in.Uncertainty = strings.TrimSpace(in.Uncertainty)
	in.CorrectionReason = strings.TrimSpace(in.CorrectionReason)
	in.AppliesTo = normalizeMemoryTopics(in.AppliesTo)
	if in.Kind == "" {
		in.Kind = sdk.ConversationMemoryKindUserPreference
	}
	if in.Scope.Kind == "" {
		in.Scope.Kind = sdk.ConversationMemoryScopeWorkspace
	}
	if !conversationKey(in.ID) || !conversationText(in.Title, 128, true) || !conversationText(in.Content, 512, true) || in.ExpectedRevision < 0 || !conversationMemoryKind(in.Kind) || !conversationMemoryScopeKind(in.Scope.Kind) || len(in.AppliesTo) > 16 || !conversationText(in.Uncertainty, 512, false) || !conversationText(in.CorrectionReason, 512, false) {
		return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_invalid")
	}
	for _, topic := range in.AppliesTo {
		if !conversationText(topic, 64, true) {
			return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_invalid")
		}
	}
	if in.Kind == sdk.ConversationMemoryKindTaskContext && in.Scope.Kind == sdk.ConversationMemoryScopeWorkspace {
		return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_scope_invalid")
	}
	switch in.Scope.Kind {
	case sdk.ConversationMemoryScopeWorkspace:
		in.Scope.ConversationID = ""
		in.Scope.TaskID = ""
	case sdk.ConversationMemoryScopeConversation:
		if in.Scope.ConversationID == "" {
			return sdk.ConversationMemoryWrite{}, conversationFailure("bad_request", "memory_scope_invalid")
		}
		if _, err := s.repo.Get(ctx, in.Scope.ConversationID, a); err != nil {
			return sdk.ConversationMemoryWrite{}, err
		}
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
		source.ConversationID = strings.TrimSpace(source.ConversationID)
		source.MessageID = strings.TrimSpace(source.MessageID)
		source.RunID = strings.TrimSpace(source.RunID)
		source.TaskID = strings.TrimSpace(source.TaskID)
		source.ArtifactID = strings.TrimSpace(source.ArtifactID)
		source.FeedbackID = strings.TrimSpace(source.FeedbackID)
		source.CapturedAt = now
		in.Source = &source
	}
	if in.CorrectionReason != "" {
		in.Source.Kind = "user_correction"
	}
	if err := s.validateConversationMemorySource(ctx, *in.Source, a); err != nil {
		return sdk.ConversationMemoryWrite{}, err
	}
	return in, nil
}

func (s *ConversationService) validateConversationMemorySource(ctx context.Context, source sdk.ConversationMemorySource, a sdk.ConversationAuthority) error {
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

func memoryApplicable(memory sdk.ConversationMemory, conversationID, taskID string) bool {
	switch memory.Scope.Kind {
	case sdk.ConversationMemoryScopeWorkspace:
		return true
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

func memoryRelevant(memory sdk.ConversationMemory, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return memory.Kind == sdk.ConversationMemoryKindUserPreference && len(memory.AppliesTo) == 0
	}
	for _, topic := range memory.AppliesTo {
		topic = strings.ToLower(topic)
		if strings.Contains(query, topic) || strings.Contains(topic, query) {
			return true
		}
	}
	if strings.Contains(query, strings.ToLower(memory.Title)) || strings.Contains(strings.ToLower(memory.Title), query) {
		return true
	}
	haystack := strings.ToLower(memory.Title + "\n" + memory.Content)
	word := []rune{}
	for _, r := range query + " " {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			word = append(word, r)
			continue
		}
		if len(word) >= 3 && strings.Contains(haystack, string(word)) {
			return true
		}
		word = word[:0]
	}
	runes := []rune(query)
	for index := 0; index+3 <= len(runes); index++ {
		fragment := string(runes[index : index+3])
		if strings.IndexFunc(fragment, unicode.IsLetter) >= 0 && strings.Contains(haystack, fragment) {
			return true
		}
	}
	return false
}

type scoredConversationMemory struct {
	memory sdk.ConversationMemory
	score  int
}

func relevantConversationMemories(items []sdk.ConversationMemory, conversationID, taskID, query string) []sdk.ConversationMemory {
	values := []scoredConversationMemory{}
	for _, item := range items {
		item = normalizeConversationMemory(item)
		if !item.Enabled || !memoryApplicable(item, conversationID, taskID) {
			continue
		}
		score := 0
		switch item.Scope.Kind {
		case sdk.ConversationMemoryScopeTask:
			score = 300
		case sdk.ConversationMemoryScopeConversation:
			score = 200
		case sdk.ConversationMemoryScopeWorkspace:
			if item.Kind != sdk.ConversationMemoryKindUserPreference || len(item.AppliesTo) != 0 {
				if !memoryRelevant(item, query) {
					continue
				}
			}
			score = 50
		}
		if memoryRelevant(item, query) {
			score += 50
		}
		if item.Kind == sdk.ConversationMemoryKindUserPreference && len(item.AppliesTo) == 0 {
			score += 10
		}
		values = append(values, scoredConversationMemory{memory: item, score: score})
	}
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].score != values[j].score {
			return values[i].score > values[j].score
		}
		if !values[i].memory.UpdatedAt.Equal(values[j].memory.UpdatedAt) {
			return values[i].memory.UpdatedAt.After(values[j].memory.UpdatedAt)
		}
		return values[i].memory.ID < values[j].memory.ID
	})
	out := []sdk.ConversationMemory{}
	used := 0
	for _, value := range values {
		if len(out) >= conversationMemoryContextItems {
			break
		}
		size := len(conversationJSONText(value.memory))
		if size > conversationMemoryContextBytes || used+size > conversationMemoryContextBytes {
			continue
		}
		used += size
		out = append(out, value.memory)
	}
	return out
}

func (s *ConversationService) conversationMemoryContext(ctx context.Context, claim persistence.ConversationClaim) ([]sdk.ConversationMemory, error) {
	items, err := s.repo.Memories(ctx, claim.Authority)
	if err != nil {
		return nil, err
	}
	query := ""
	if claim.Run.UserSeq > 0 {
		messages, historyErr := s.repo.History(ctx, claim.Run.ConversationID, claim.Run.UserSeq-1, claim.Run.UserSeq, 1, claim.Authority)
		if historyErr != nil {
			return nil, historyErr
		}
		if len(messages) == 1 {
			query = messages[0].Content
		}
	}
	taskID := ""
	if claim.Run.BackgroundTask != nil {
		taskID = claim.Run.BackgroundTask.TaskID
	}
	return relevantConversationMemories(items, claim.Run.ConversationID, taskID, query), nil
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
		})
	}
	return out
}
