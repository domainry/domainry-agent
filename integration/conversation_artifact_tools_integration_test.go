package integration_test

import (
	"context"
	"encoding/json"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

type artifactExecutionPolicy struct{ artifactTestPolicy }

func (*artifactExecutionPolicy) AuthorizeConversationInteraction(_ context.Context, a agentsdk.ConversationAuthority, _ agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: a.Known}, nil
}

func TestArtifactPreparationFailureCanCorrectWithoutReconciliation(t *testing.T) {
	for _, operation := range []string{"artifact_export", "artifact_edit"} {
		t.Run(operation, func(t *testing.T) {
			repo, a := conversationRepository(t), conversationAuthority()
			storage, err := knowledgemodule.NewArtifactFiles(filepath.Join(t.TempDir(), "content"))
			if err != nil {
				t.Fatal(err)
			}
			defer storage.Close()
			policy := &artifactExecutionPolicy{}
			personal, err := application.NewPersonalConversationHost(repo, policy, "UTC")
			if err != nil {
				t.Fatal(err)
			}
			model := &sourceModel{step: func(in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				last := in.Messages[len(in.Messages)-1]
				args := func(id string) map[string]any {
					if operation == "artifact_export" {
						return map[string]any{"id": id, "version": 1, "format": "markdown"}
					}
					return map[string]any{"id": id, "expected_version": 1, "patch": map[string]string{"title": "Corrected report"}}
				}
				if last.Role == "user" {
					return resultToolCall(operation, "missing", args("art_missing")), nil
				}
				var result agentsdk.ConversationToolResult
				if json.Unmarshal([]byte(last.Content), &result) != nil {
					t.Fatal("invalid tool feedback")
				}
				switch last.ToolCallID {
				case "missing":
					if result.Status != "failed" || result.ErrorCode != "artifact_not_found" || !strings.Contains(string(result.Content), "actual artifact ID") {
						t.Error("missing ID incorrectly treated as unknown write")
					}
					return resultToolCall("artifact_list", "find", map[string]any{}), nil
				case "find":
					var page agentsdk.ConversationArtifactPage
					if json.Unmarshal(result.Content, &page) != nil || len(page.Items) != 1 {
						t.Fatal("stored artifact missing")
					}
					return resultToolCall(operation, "corrected", args(page.Items[0].ID)), nil
				case "corrected":
					if result.Status != "completed" {
						t.Errorf("corrected call did not complete: %s", last.Content)
					}
				}
				return sourceAnswer("已完成实际操作"), nil
			}}
			service, err := conversationassembly.NewService(repo, model, a.RuntimeID, application.ConversationOptions{PersonalAuthorizer: policy, ToolHost: personal, ArtifactStorage: storage})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			original, err := service.CreateArtifact(t.Context(), agentsdk.ConversationArtifactCreate{ClientID: "seed", Title: "Report", Content: agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: "Original report"}}, a)
			if err != nil {
				t.Fatal(err)
			}
			conversation, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "correction"}, a)
			run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "correct", Message: "Process the report", WriteScope: &agentsdk.ConversationWriteScope{PersonalArtifacts: true}}, a)
			if err != nil {
				t.Fatal(err)
			}
			final := waitConversation(t, service, conversation.ID, run.ID)
			if final.Status != "completed" || final.Interaction != nil {
				t.Fatalf("definite failure paused for reconciliation: %s", final.Status)
			}
			completed, failed := 0, 0
			for _, step := range final.Steps {
				for _, call := range step.Calls {
					if call.Name == operation {
						if call.Status == "completed" {
							completed++
						}
						if call.Status == "failed" {
							failed++
						}
					}
				}
			}
			if completed != 1 || failed != 1 {
				t.Fatal("correction did not preserve failed and successful calls separately")
			}
			current, err := service.Artifact(t.Context(), original.Artifact.ID, 0, a)
			if err != nil {
				t.Fatal(err)
			}
			wantVersion := int64(1)
			if operation == "artifact_edit" {
				wantVersion = 2
			}
			if current.Artifact.Version != wantVersion || current.Content.Markdown != "Original report" {
				t.Fatal("failed preparation changed artifact state")
			}
		})
	}
}

func TestArtifactConversationToolsEditExportAndConfirmationRestart(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	storage, err := knowledgemodule.NewArtifactFiles(filepath.Join(t.TempDir(), "content"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	policy := &artifactExecutionPolicy{}
	personal, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	const original = "# 周报\n\n## 第一节\n已完成。\n\n## 第二节\n待核对。\n"
	model := &sourceModel{step: func(in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "user" {
			switch last.Content {
			case "生成并修改周报":
				return resultToolCall("artifact_create", "create", map[string]any{"title": "周报", "content": agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: original}}), nil
			case "需要确认的周报":
				return resultToolCall("artifact_create", "confirmed", map[string]any{"title": "确认周报", "content": agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: "确认后保存"}}), nil
			case "修改刚才的周报标题":
				return resultToolCall("artifact_list", "find", map[string]any{"query": "周报"}), nil
			}
		}
		var result struct {
			Artifact agentsdk.ConversationArtifact `json:"artifact"`
		}
		var envelope agentsdk.ConversationToolResult
		if json.Unmarshal([]byte(last.Content), &envelope) != nil || envelope.Status != "completed" {
			t.Errorf("artifact tool failed: %s", last.Content)
		}
		_ = json.Unmarshal(envelope.Content, &result)
		switch last.ToolCallID {
		case "create":
			return resultToolCall("artifact_read", "read", map[string]any{"id": result.Artifact.ID, "version": 1}), nil
		case "read":
			if !strings.Contains(last.Content, "待核对") {
				t.Error("read lost exact original section")
			}
			return resultToolCall("artifact_edit", "edit", map[string]any{"id": result.Artifact.ID, "expected_version": result.Artifact.Version, "patch": map[string]any{"text": []any{map[string]string{"find": "待核对。", "replace": "已核对。"}}}}), nil
		case "edit":
			return resultToolCall("artifact_export", "export", map[string]any{"id": result.Artifact.ID, "version": 1, "format": "markdown"}), nil
		case "find":
			var page agentsdk.ConversationArtifactPage
			if json.Unmarshal(envelope.Content, &page) != nil || len(page.Items) != 1 {
				t.Error("artifact query did not locate original work")
				return sourceAnswer("查询失败"), nil
			}
			return resultToolCall("artifact_edit", "rename", map[string]any{"id": page.Items[0].ID, "expected_version": page.Items[0].Version, "patch": map[string]string{"title": "周报修订稿"}}), nil
		}
		return sourceAnswer("周报已保存，可按指定版本下载。"), nil
	}}
	options := application.ConversationOptions{PersonalAuthorizer: policy, ToolHost: personal, ArtifactStorage: storage}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if service != nil {
			service.Close()
		}
	}()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "artifact-workflow"}, a)
	if err != nil {
		t.Fatal(err)
	}
	send := func(key, message string, scope *agentsdk.ConversationWriteScope) agentsdk.ConversationRun {
		run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: key, Message: message, WriteScope: scope}, a)
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	run := send("workflow", "生成并修改周报", &agentsdk.ConversationWriteScope{PersonalArtifacts: true})
	done := waitConversation(t, service, c.ID, run.ID)
	if done.Status != "completed" || done.AccessError != "" {
		t.Fatalf("workflow failed: %s %s %s", done.Status, done.ErrorCode, done.AccessError)
	}
	page, err := service.Artifacts(t.Context(), agentsdk.ConversationArtifactQuery{SourceConversationID: c.ID}, a)
	if err != nil || len(page.Items) != 1 || page.Items[0].Version != 2 {
		t.Fatal("tool edits not saved", err)
	}
	id := page.Items[0].ID
	value, err := service.Artifact(t.Context(), id, 2, a)
	if err != nil || !strings.Contains(value.Content.Markdown, "已核对") {
		t.Fatal("edited version missing", err)
	}
	snapshot, err := repo.ConversationSourceSnapshot(t.Context(), agentsdk.ConversationRunReference{ConversationID: c.ID, RunID: run.ID}, a)
	if err != nil {
		t.Fatal(err)
	}
	var exported agentsdk.ConversationArtifactExport
	for _, call := range snapshot.Calls {
		if call.Result == nil || call.Result.Status != "completed" {
			t.Fatal("incomplete durable tool receipt")
		}
		if call.Call.Name == "artifact_export" {
			var result struct {
				Export agentsdk.ConversationArtifactExport `json:"export"`
			}
			_ = json.Unmarshal(call.Result.Content, &result)
			exported = result.Export
		}
	}
	if len(snapshot.Calls) != 4 || exported.ID == "" {
		t.Fatal("multi-step artifact execution missing")
	}
	download, err := service.DownloadArtifact(t.Context(), exported.ID, a)
	if err != nil || string(download.Data) != original {
		t.Fatal("export did not bind original version", err)
	}
	// A later run finds the previous artifact from stored IDs and edits it.
	second := send("rename", "修改刚才的周报标题", &agentsdk.ConversationWriteScope{PersonalArtifacts: true})
	if done = waitConversation(t, service, c.ID, second.ID); done.Status != "completed" || done.AccessError != "" {
		t.Fatalf("cross-run source cycle: %+v", done)
	}
	value, err = service.Artifact(t.Context(), id, 0, a)
	if err != nil || value.Artifact.Version != 3 || value.Artifact.Title != "周报修订稿" {
		t.Fatal("cross-run edit did not use existing artifact", err)
	}
	policy.denied.Store("artifact_read")
	masked, err := service.Run(t.Context(), c.ID, run.ID, a)
	if err != nil || masked.AccessError == "" || len(masked.Steps) != 0 {
		t.Fatal("stored artifact output bypassed current read permission", err)
	}
	policy.denied.Store("")
	// A memory-only scope cannot authorize an artifact write.
	pending := send("confirm", "需要确认的周报", &agentsdk.ConversationWriteScope{PersonalMemory: true})
	waiting := waitConversationState(t, service, c.ID, pending.ID, "waiting_confirmation")
	if waiting.Interaction == nil {
		t.Fatal("confirmation missing")
	}
	before, _ := repo.Artifacts(t.Context(), agentsdk.ConversationArtifactQuery{}, a)
	if len(before.Items) != 1 {
		t.Fatal("unconfirmed artifact was created")
	}
	service.Close()
	service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	restored := waitConversationState(t, service, c.ID, pending.ID, "waiting_confirmation")
	if restored.Interaction.ID != waiting.Interaction.ID || restored.Interaction.Arguments != waiting.Interaction.Arguments {
		t.Fatal("confirmation changed on restart")
	}
	response := agentsdk.ConversationInteractionResponse{InteractionID: restored.Interaction.ID, ClientID: "approve-artifact", ExpectedRevision: restored.Interaction.Revision, Decision: "approve"}
	if _, err = service.Respond(t.Context(), c.ID, pending.ID, response, a); err != nil {
		t.Fatal(err)
	}
	if done = waitConversation(t, service, c.ID, pending.ID); done.Status != "completed" || done.AccessError != "" {
		t.Fatalf("confirmed create failed: %s %s", done.ErrorCode, done.AccessError)
	}
	if _, err = service.Respond(t.Context(), c.ID, pending.ID, response, a); err != nil {
		t.Fatal("confirmation replay", err)
	}
	after, _ := repo.Artifacts(t.Context(), agentsdk.ConversationArtifactQuery{}, a)
	if len(after.Items) != 2 {
		t.Fatal("confirmation replay duplicated artifact")
	}
}

func TestArtifactSourcesStopAtTheGeneratingStep(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	storage, err := knowledgemodule.NewArtifactFiles(filepath.Join(t.TempDir(), "content"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	var visible atomic.Bool
	visible.Store(true)
	knowledge := sourceKnowledgeFixture(t, &visible)
	policy := &artifactExecutionPolicy{}
	personal, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	var artifactID string
	model := &sourceModel{step: func(in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "user" {
			return resultToolCall("artifact_create", "public", map[string]any{"title": "公开初稿", "content": agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: "公开内容"}}), nil
		}
		switch last.ToolCallID {
		case "public":
			var envelope agentsdk.ConversationToolResult
			_ = json.Unmarshal([]byte(last.Content), &envelope)
			var result struct {
				Artifact agentsdk.ConversationArtifact `json:"artifact"`
			}
			_ = json.Unmarshal(envelope.Content, &result)
			artifactID = result.Artifact.ID
			return resultToolCall("knowledge_search", "private", map[string]string{"query": "费用规则"}), nil
		case "private":
			return resultToolCall("artifact_edit", "derived", map[string]any{"id": artifactID, "expected_version": 1, "patch": map[string]any{"text": []any{map[string]string{"find": "公开内容", "replace": "PRIVATE-VALUE-97"}}}}), nil
		}
		return sourceAnswer("已补充资料"), nil
	}}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: personal, PersonalAuthorizer: policy, ArtifactStorage: storage, Knowledge: knowledge})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "source-cutoff"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "source-edit", Message: "先生成初稿，再根据资料更新", WriteScope: &agentsdk.ConversationWriteScope{PersonalArtifacts: true}}, a)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.Status != "completed" || done.AccessError != "" {
		t.Fatalf("source-aware artifact run failed: %s %s", done.ErrorCode, done.AccessError)
	}
	// Read IDs from durable records, after the worker has completed its writes.
	items, err := repo.Artifacts(t.Context(), agentsdk.ConversationArtifactQuery{}, a)
	if err != nil || len(items.Items) != 1 {
		t.Fatal(err)
	}
	id := items.Items[0].ID
	first, err := repo.ArtifactRecord(t.Context(), id, 1, a)
	if err != nil || first.Sources.Runs[0].BeforeStep != 1 {
		t.Fatal("initial step boundary missing", err)
	}
	second, err := repo.ArtifactRecord(t.Context(), id, 2, a)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range second.Sources.Runs {
		found = found || ref.BeforeStep == 3
	}
	if !found {
		t.Fatal("edit did not capture earlier knowledge step")
	}
	visible.Store(false)
	public, err := service.Artifact(t.Context(), id, 1, a)
	if err != nil || public.Content.Markdown != "公开内容" {
		t.Fatal("later private lookup retroactively hid original", err)
	}
	if _, err = service.Artifact(t.Context(), id, 2, a); err == nil {
		t.Fatal("derived private revision remained readable")
	}
	versions, err := service.ArtifactVersions(t.Context(), id, 0, 20, a)
	if err != nil || len(versions.Items) != 1 || versions.Items[0].Version != 1 || !versions.Omitted {
		t.Fatal("version access did not respect actual source steps", err)
	}
	visible.Store(true)
	if restored, err := service.Artifact(t.Context(), id, 2, a); err != nil || restored.Content.Markdown != "PRIVATE-VALUE-97" {
		t.Fatal("restored source did not restore derived content", err)
	}
}
