package application

import (
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationMemoryRetrievalPreferenceSupportsLexicalSwitch(t *testing.T) {
	if !validConversationMemoryPreference(true, sdk.AgentMemoryPreference{RetrievalMode: sdk.AgentMemoryRetrievalLexical}) {
		t.Fatal("enabled conversation rejected lexical retrieval")
	}
	if validConversationMemoryPreference(false, sdk.AgentMemoryPreference{RetrievalMode: sdk.AgentMemoryRetrievalLexical}) {
		t.Fatal("disabled conversation accepted a retrieval preference")
	}
	if validConversationMemoryPreference(true, sdk.AgentMemoryPreference{Mode: sdk.AgentMemoryPreferenceDisabled, RetrievalMode: sdk.AgentMemoryRetrievalHybrid}) {
		t.Fatal("disabled memory preference accepted a retrieval mode")
	}
}

func TestMemorySnapshotRespectsLifecycleScopeQueryAndBounds(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	memory := func(id, kind, scope, scopeID, title, content string, topics ...string) sdk.MemoryContextItem {
		return sdk.MemoryContextItem{
			ID: id, ProviderKey: sdk.AgentMemoryProviderPersonal, Revision: "1", Kind: kind, Scope: scope, ScopeID: scopeID,
			Title: title, Content: content, AppliesTo: topics, Status: sdk.AgentMemoryStatusActive,
			Source: sdk.AgentMemorySource{ProviderKey: sdk.AgentMemoryProviderPersonal, SourceID: id}, UpdatedAt: now,
		}
	}
	items := []sdk.MemoryContextItem{
		memory("global-style", sdk.ConversationMemoryKindUserPreference, sdk.ConversationMemoryScopeWorkspace, "", "表达偏好", "先写结论"),
		memory("project-go", sdk.ConversationMemoryKindProjectFact, sdk.ConversationMemoryScopeWorkspace, "", "Agent 技术栈", "服务使用 Go", "Agent 架构"),
		memory("project-finance", sdk.ConversationMemoryKindProjectFact, sdk.ConversationMemoryScopeWorkspace, "", "财务系统", "使用 Java", "财务"),
		memory("conversation-current", sdk.ConversationMemoryKindTaskContext, sdk.ConversationMemoryScopeConversation, "conversation-a", "本次约束", "检查 Agent 架构时不要修改 API"),
		memory("conversation-other", sdk.ConversationMemoryKindTaskContext, sdk.ConversationMemoryScopeConversation, "conversation-b", "其他会话", "私有资料"),
		memory("task-current", sdk.ConversationMemoryKindTaskContext, sdk.ConversationMemoryScopeTask, "task-a", "交付范围", "Agent 架构只生成报告"),
		memory("task-other", sdk.ConversationMemoryKindTaskContext, sdk.ConversationMemoryScopeTask, "task-b", "其他任务", "私有资料"),
	}
	items[1].UpdatedAt = now.Add(time.Minute)
	items[6].Status = sdk.AgentMemoryStatusRetracted
	request := sdk.MemoryContextRequest{
		Consumer: sdk.AgentMemoryConsumerConversation, WorkspaceID: "workspace-a", UserID: "user-a", ConversationID: "conversation-a", TaskID: "task-a", RunID: "run-a",
		Query: "继续检查 Agent 架构并生成报告", Policy: sdk.AgentMemoryPolicy{Enabled: true, ReferenceSaved: true, RetrievalMode: sdk.AgentMemoryRetrievalHybrid, MaxItems: 8, MaxBytes: 4096},
	}
	snapshot, err := selectTaskMemorySnapshot(request, []sdk.MemoryContextResult{{ProviderKey: sdk.AgentMemoryProviderPersonal, Items: items}}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"task-current", "conversation-current", "project-go", "global-style"}
	if len(snapshot.Items) != len(want) {
		t.Fatalf("selected %d memories: %+v", len(snapshot.Items), snapshot.Items)
	}
	for index, id := range want {
		if snapshot.Items[index].ID != id {
			t.Fatalf("memory %d=%s want %s", index, snapshot.Items[index].ID, id)
		}
	}
	request.Policy.MaxItems, request.Policy.MaxBytes = 1, 1024
	bounded, err := selectTaskMemorySnapshot(request, []sdk.MemoryContextResult{{ProviderKey: sdk.AgentMemoryProviderPersonal, Items: items}}, now)
	if err != nil || len(bounded.Items) != 1 {
		t.Fatalf("memory context budget was not enforced: items=%d err=%v", len(bounded.Items), err)
	}
}

func TestMemoryDefaultsAndLiteralSearch(t *testing.T) {
	memory := normalizeConversationMemory(sdk.ConversationMemory{ID: "preference", Title: "周报格式", Content: "按项目组织", Enabled: true})
	if memory.Kind != sdk.ConversationMemoryKindUserPreference || memory.Scope.Kind != sdk.ConversationMemoryScopeWorkspace || memory.AppliesTo == nil || !memoryLiteralMatch(memory, "项目") {
		t.Fatalf("memory was not normalized: %+v", memory)
	}
}

func TestMemorySummaryRespectsProjectOnlyAndManagementVisibility(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	memories := []sdk.ConversationMemory{
		{ID: "workspace", Kind: sdk.ConversationMemoryKindUserPreference, Title: "Style", Content: "Be concise", Enabled: true, Status: sdk.AgentMemoryStatusActive, Scope: sdk.ConversationMemoryScope{Kind: sdk.ConversationMemoryScopeWorkspace}, Revision: 1, UpdatedAt: now},
		{ID: "project", Kind: sdk.ConversationMemoryKindProjectFact, Title: "Runtime", Content: "Uses Go", Enabled: true, Status: sdk.AgentMemoryStatusActive, Scope: sdk.ConversationMemoryScope{Kind: sdk.ConversationMemoryScopeProject, ProjectID: "project-a"}, Revision: 2, UpdatedAt: now},
		{ID: "hidden", Kind: sdk.ConversationMemoryKindProjectFact, Title: "Hidden", Content: "Do not mention", Enabled: true, Status: sdk.AgentMemoryStatusActive, DoNotMention: true, Scope: sdk.ConversationMemoryScope{Kind: sdk.ConversationMemoryScopeProject, ProjectID: "project-a"}, Revision: 1, UpdatedAt: now},
	}
	summary := buildAgentMemorySummary(memories, "project-a", "conversation", "", true, false, false, now)
	if summary.ItemCount != 1 || len(summary.Groups) != 1 || summary.Groups[0].Items[0].MemoryID != "project" || summary.Groups[0].Items[0].ScopeID != "project-a" {
		t.Fatalf("project summary=%+v", summary)
	}
	managed := buildAgentMemorySummary(memories, "project-a", "conversation", "", true, false, true, now)
	if managed.ItemCount != 2 || managed.Revision == summary.Revision {
		t.Fatalf("managed project summary=%+v", managed)
	}
}
