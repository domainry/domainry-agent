package application

import (
	"sort"
	"strconv"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func buildAgentMemorySummary(memories []agentsdk.ConversationMemory, projectID, conversationID, taskID string, projectOnly, includePending, includeSuppressed bool, now time.Time) agentsdk.AgentMemorySummary {
	eligible := make([]agentsdk.ConversationMemory, 0, len(memories))
	for _, memory := range memories {
		memory = normalizeConversationMemory(memory)
		if !memoryApplicable(memory, projectID, conversationID, taskID) || projectOnly && memory.Scope.Kind != agentsdk.ConversationMemoryScopeProject {
			continue
		}
		active := memory.Enabled && memory.Status == agentsdk.AgentMemoryStatusActive && !memory.DoNotMention && (memory.ValidFrom == nil || !now.Before(memory.ValidFrom.UTC())) && (memory.ValidUntil == nil || now.Before(memory.ValidUntil.UTC()))
		pending := memory.Status == agentsdk.AgentMemoryStatusCandidate
		if !active && !(pending && includePending) && !(!pending && includeSuppressed) && !(pending && includeSuppressed) {
			continue
		}
		eligible = append(eligible, memory)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].Importance != eligible[j].Importance {
			return eligible[i].Importance > eligible[j].Importance
		}
		if !eligible[i].UpdatedAt.Equal(eligible[j].UpdatedAt) {
			return eligible[i].UpdatedAt.After(eligible[j].UpdatedAt)
		}
		return eligible[i].ID < eligible[j].ID
	})
	omitted := max(0, len(eligible)-agentsdk.AgentMemorySummaryItems)
	if len(eligible) > agentsdk.AgentMemorySummaryItems {
		eligible = eligible[:agentsdk.AgentMemorySummaryItems]
	}
	grouped := map[string][]agentsdk.AgentMemorySummaryItem{}
	for _, memory := range eligible {
		scopeID := ""
		switch memory.Scope.Kind {
		case agentsdk.ConversationMemoryScopeProject:
			scopeID = memory.Scope.ProjectID
		case agentsdk.ConversationMemoryScopeConversation:
			scopeID = memory.Scope.ConversationID
		case agentsdk.ConversationMemoryScopeTask:
			scopeID = memory.Scope.TaskID
		}
		source := agentsdk.AgentMemorySource{ProviderKey: agentsdk.AgentMemoryProviderPersonal, SourceID: memory.ID, SourceRevision: strconv.FormatInt(memory.Revision, 10), Kind: "saved_memory"}
		if memory.Source != nil {
			source.Kind, source.URI, source.CapturedAt = strings.TrimSpace(memory.Source.Kind), strings.TrimSpace(memory.Source.URI), memory.Source.CapturedAt
		}
		grouped[memory.Kind] = append(grouped[memory.Kind], agentsdk.AgentMemorySummaryItem{
			MemoryID: memory.ID, Revision: memory.Revision, Title: memory.Title, Content: memory.Content,
			Scope: memory.Scope.Kind, ScopeID: scopeID, Status: memory.Status, DoNotMention: memory.DoNotMention,
			Importance: memory.Importance, Confidence: memory.Confidence, Source: source, LastUpdatedAt: memory.UpdatedAt,
		})
	}
	groups := make([]agentsdk.AgentMemorySummaryGroup, 0, 3)
	for _, kind := range []string{agentsdk.ConversationMemoryKindUserPreference, agentsdk.ConversationMemoryKindProjectFact, agentsdk.ConversationMemoryKindTaskContext} {
		if items := grouped[kind]; len(items) != 0 {
			groups = append(groups, agentsdk.AgentMemorySummaryGroup{Kind: kind, Items: items})
		}
	}
	return agentsdk.AgentMemorySummary{Revision: conversationDigest(groups), Groups: groups, ItemCount: len(eligible), Omitted: omitted, GeneratedAt: now.UTC()}
}
