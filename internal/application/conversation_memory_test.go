package application

import (
	"fmt"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func TestRelevantConversationMemoriesRespectKindScopeQueryAndBounds(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	memory := func(id, kind, scope, conversationID, taskID, title, content string, topics ...string) sdk.ConversationMemory {
		return sdk.ConversationMemory{ID: id, Kind: kind, Title: title, Content: content, Enabled: true, Scope: sdk.ConversationMemoryScope{Kind: scope, ConversationID: conversationID, TaskID: taskID}, AppliesTo: topics, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	items := []sdk.ConversationMemory{
		memory("global-style", sdk.ConversationMemoryKindUserPreference, sdk.ConversationMemoryScopeWorkspace, "", "", "表达偏好", "先写结论"),
		memory("project-go", sdk.ConversationMemoryKindProjectFact, sdk.ConversationMemoryScopeWorkspace, "", "", "Agent 技术栈", "服务使用 Go", "Agent 架构"),
		memory("project-finance", sdk.ConversationMemoryKindProjectFact, sdk.ConversationMemoryScopeWorkspace, "", "", "财务系统", "使用 Java", "财务"),
		memory("conversation-current", sdk.ConversationMemoryKindTaskContext, sdk.ConversationMemoryScopeConversation, "conversation-a", "", "本次约束", "不要修改 API"),
		memory("conversation-other", sdk.ConversationMemoryKindTaskContext, sdk.ConversationMemoryScopeConversation, "conversation-b", "", "其他会话", "私有资料"),
		memory("task-current", sdk.ConversationMemoryKindTaskContext, sdk.ConversationMemoryScopeTask, "conversation-a", "task-a", "交付范围", "只生成报告"),
		memory("task-other", sdk.ConversationMemoryKindTaskContext, sdk.ConversationMemoryScopeTask, "conversation-a", "task-b", "其他任务", "私有资料"),
	}
	items[1].UpdatedAt = now.Add(time.Minute)
	items[6].Enabled = false
	got := relevantConversationMemories(items, "conversation-a", "task-a", "继续检查 Agent 架构并生成报告")
	want := []string{"task-current", "conversation-current", "project-go", "global-style"}
	if len(got) != len(want) {
		t.Fatalf("selected %d memories: %+v", len(got), got)
	}
	for index, id := range want {
		if got[index].ID != id {
			t.Fatalf("memory %d=%s want %s", index, got[index].ID, id)
		}
	}
	for index := 0; index < 20; index++ {
		items = append(items, memory(fmt.Sprintf("global-%02d", index), sdk.ConversationMemoryKindUserPreference, sdk.ConversationMemoryScopeWorkspace, "", "", "通用偏好", "保持简洁"))
	}
	if got = relevantConversationMemories(items, "conversation-a", "task-a", "继续检查 Agent 架构并生成报告"); len(got) > conversationMemoryContextItems || len(conversationJSONText(memoryContextProjection(got))) > conversationMemoryContextBytes+512 {
		t.Fatalf("unbounded memory context: items=%d bytes=%d", len(got), len(conversationJSONText(memoryContextProjection(got))))
	}
}

func TestMemoryLegacyDefaultsAndLiteralSearch(t *testing.T) {
	legacy := normalizeConversationMemory(sdk.ConversationMemory{ID: "legacy", Title: "周报格式", Content: "按项目组织", Enabled: true})
	if legacy.Kind != sdk.ConversationMemoryKindUserPreference || legacy.Scope.Kind != sdk.ConversationMemoryScopeWorkspace || legacy.AppliesTo == nil || !memoryLiteralMatch(legacy, "项目") {
		t.Fatalf("legacy memory was not normalized: %+v", legacy)
	}
}
