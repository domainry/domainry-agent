package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	knowledge "github.com/domainry/domainry-knowledge/module"
)

type taskReceiptRepository struct {
	*personalReceiptRepository
	persistence.ConversationTaskReadRepository
	persistence.ConversationHistoryRepository
	persistence.ConversationArtifactRepository
	artifact   persistence.ConversationArtifactRecord
	task       sdk.ConversationTask
	message    sdk.ConversationMessage
	snapshots  map[string]persistence.ConversationSourceSnapshot
	missing    bool
	wrongRun   bool
	sourceRefs []sdk.ConversationRunReference
}

func (r *taskReceiptRepository) ArtifactRecord(_ context.Context, id string, version int64, a sdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	if a != r.a || r.artifact.Artifact.ID != id || r.artifact.Artifact.Version != version {
		return persistence.ConversationArtifactRecord{}, invalidPersonalReceipt()
	}
	return r.artifact, nil
}

func (r *taskReceiptRepository) Get(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.Conversation, error) {
	c, err := r.personalReceiptRepository.Get(ctx, id, a)
	// Explicitly exercise the same-delegation raw-data permission boundary.
	c.DelegationID = "released"
	return c, err
}
func (r *taskReceiptRepository) ConversationTask(_ context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTask, error) {
	if a != r.a || id != r.task.ID || r.missing {
		return sdk.ConversationTask{}, conversationFailure("not_found", "task_not_found")
	}
	return r.task, nil
}
func (r *taskReceiptRepository) Run(_ context.Context, conversation, id string, a sdk.ConversationAuthority) (sdk.ConversationRun, error) {
	if a != r.a || conversation != "producer" || id != "execution-run" {
		return sdk.ConversationRun{}, invalidPersonalReceipt()
	}
	taskID := r.task.ID
	if r.wrongRun {
		taskID = "another-task"
	}
	return sdk.ConversationRun{ID: id, ConversationID: conversation, Status: "completed", Attempt: 1, Steps: []sdk.ConversationStepView{{Number: 0, Status: "completed"}}, BackgroundTask: &sdk.ConversationTaskExecution{TaskID: taskID}}, nil
}
func (r *taskReceiptRepository) ConversationSourceSnapshot(_ context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	if a != r.a || ref.ConversationID != "producer" {
		return persistence.ConversationSourceSnapshot{}, invalidPersonalReceipt()
	}
	r.sourceRefs = append(r.sourceRefs, ref)
	return r.snapshots[ref.RunID], nil
}
func (r *taskReceiptRepository) HistoryMessage(_ context.Context, conversation, id string, a sdk.ConversationAuthority) (sdk.ConversationMessage, error) {
	if a != r.a || conversation != "producer" || id != r.message.ID {
		return sdk.ConversationMessage{}, invalidPersonalReceipt()
	}
	return r.message, nil
}

func TestTaskDeliveryReceiptsSeparateControlsFromCurrentExecutionAndSources(t *testing.T) {
	for _, key := range []string{"task_start", "task_get", "task_list", "task_cancel", "task_resume", "task_update"} {
		t.Run(key, func(t *testing.T) {
			now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
			a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
			definition, _ := taskResultReadDefinition(key)
			budget := sdk.ConversationTaskBudget{MaxSteps: 2, MaxToolCalls: 2, MaxOutputBytes: 4096, TimeoutSeconds: 60}
			brief := sdk.DefaultConversationTaskBrief("Read sources")
			task := sdk.ConversationTask{ID: "task", Goal: brief.Goal, Input: "Original private input", Brief: &brief, AgreementRevision: 1, GoalProgress: sdk.ConversationGoalProgress{Revision: 2, Status: sdk.ConversationGoalStatusCompleted, Phase: "completed", CompletedItems: append([]string(nil), brief.CompletionConditions...), RemainingItems: []string{}, UpdatedAt: now}, SourceConversationID: "producer", SourceRunID: "run", ExecutionRunID: "execution-run", ResultMessageID: "answer", CompletionEventID: "task_event_original", Status: "completed", Budget: budget, CreatedAt: now, UpdatedAt: now}
			if key == "task_update" {
				brief.Version = 2
				brief.ExplicitFields = []string{"goal"}
				brief.InferredFields = []string{"assumptions", "audience", "completion_conditions", "constraints", "deliverable"}
				task.Brief, task.AgreementRevision = &brief, 2
			}
			view := sdk.ConversationTaskDetail{ConversationTaskSummary: sdk.ConversationTaskSummary{ID: task.ID, Goal: task.Goal, Brief: task.Brief, AgreementRevision: task.AgreementRevision, GoalProgress: task.GoalProgress, SourceConversationID: task.SourceConversationID, SourceRunID: task.SourceRunID, ExecutionRunID: task.ExecutionRunID, Status: task.Status, CreatedAt: now, Progress: sdk.ConversationTaskProgress{RunStatus: "completed", Attempt: 1, Steps: 1}, Result: &sdk.ConversationTaskResult{MessageID: "answer", Preview: "Original answer", Bytes: 15, Complete: true}, CompletionEventID: task.CompletionEventID}, Input: task.Input}
			var value any = map[string]any{"task": compactConversationTask(view)}
			args := `{"id":"task"}`
			result := sdk.ConversationToolResult{Status: "completed"}
			if key == "task_start" {
				args = conversationJSONText(sdk.ConversationTaskStart{Goal: task.Goal, Input: task.Input, AllowedTools: []string{}, Budget: budget})
				value = map[string]any{"task": sdk.ConversationTaskReceipt{ID: task.ID, Status: "queued", AllowedTools: []string{}, Budget: budget, SourceConversationID: task.SourceConversationID, SourceRunID: task.SourceRunID, CreatedAt: now}}
				result.ResourceID, result.Completion = task.ID, "accepted"
			} else if key == "task_list" {
				args, value = `{}`, sdk.ConversationTaskPage{Items: []sdk.ConversationTaskSummary{view.ConversationTaskSummary}, Complete: true}
			} else if key == "task_update" {
				args = conversationJSONText(conversationTaskAgreementToolInput{ID: task.ID, ClientID: "agreement-v2", ExpectedRevision: 1, Reason: "user changed scope", Brief: brief})
			}
			result.Content, _ = json.Marshal(value)
			record := persistence.ConversationToolExecution{Step: 3, State: "completed", Call: sdk.ConversationToolCall{ID: "call", Name: key, Arguments: args}, Definition: definition, Result: &result, IdempotencyKey: "original"}
			repo := &taskReceiptRepository{personalReceiptRepository: &personalReceiptRepository{a: a, record: record}, task: task, snapshots: map[string]persistence.ConversationSourceSnapshot{}, message: sdk.ConversationMessage{ID: "answer", ConversationID: "producer", RunID: "execution-run", Role: "assistant", BackgroundTaskID: task.ID, Content: "Original answer"}}
			policy := &deliveryArtifactPolicy{denied: map[string]bool{key: true}, disabled: map[string]bool{}}
			executor := &deliveryReadTestHost{}
			s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{KnowledgeFactory: knowledge.NewFactory(), ToolHost: &profileToolHost{base: executor, allowed: map[string]bool{}}, ToolAvailability: policy, PersonalAuthorizer: policy, CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read", "execution_read"}}}
			owner := sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}
			base := context.WithValue(t.Context(), conversationAgentContextKey{}, &sdk.ConversationAgentSnapshot{})
			ctx := deliverySourceContext(base, "released")
			read := func() error { _, err := s.sourceAudit(a).record(ctx, owner, record); return err }
			if _, err := s.sourceAudit(a).record(base, owner, record); err == nil {
				t.Fatal("raw task result bypassed execution policy")
			}
			checks := executor.executionChecks
			if err := read(); err != nil || executor.executionChecks != checks || len(policy.requests) != 0 {
				t.Fatal("task receipt required a control grant", err)
			}
			for _, ref := range repo.sourceRefs {
				if ref.RunID == owner.RunID && ref.BeforeStep != record.Step+1 {
					t.Fatal("task source recursed into its own receipt", ref)
				}
			}
			s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "delivery_read"}
			if err := read(); !collaborationDenied(err) {
				t.Fatal("same-delegation delivery exposed private task execution", err)
			}
			s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "execution_read"}
			if err := read(); !collaborationDenied(err) {
				t.Fatal("execution grant replaced delivery grant", err)
			}
			s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "execution_read", "delivery_read"}
			repo.missing = true
			if err := read(); err == nil {
				t.Fatal("missing task accepted")
			}
			repo.missing = false
			policy.disabled[key] = true
			if err := read(); err == nil {
				t.Fatal("disabled producing tool accepted")
			}
			policy.disabled[key] = false
			other := a
			other.UserID = "other"
			if _, err := s.sourceAudit(other).record(ctx, owner, record); err == nil {
				t.Fatal("foreign task accepted")
			}
			bad := result
			bad.Content = json.RawMessage(`{"task":{"id":"invented"}}`)
			changed := record
			changed.Result = &bad
			if _, err := s.sourceAudit(a).record(ctx, owner, changed); err == nil {
				t.Fatal("forged task receipt accepted")
			}
			if key != "task_start" {
				repo.wrongRun = true
				if err := read(); err == nil {
					t.Fatal("foreign task run accepted")
				}
				repo.wrongRun = false
				repo.message.Content = "Changed answer"
				if err := read(); err == nil {
					t.Fatal("changed original answer accepted")
				}
				repo.message.Content = "Original answer"
				attachment := sdk.AttachmentConversationTools()[0]
				repo.snapshots["execution-run"] = persistence.ConversationSourceSnapshot{Calls: []persistence.ConversationToolExecution{{Step: 0, Call: sdk.ConversationToolCall{ID: "private", Name: attachment.Key, Arguments: `{}`}, Definition: attachment, State: "completed", Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"text":"private"}`)}}}}
				if err := read(); err == nil {
					t.Fatal("task preview shared private attachment")
				}
				delete(repo.snapshots, "execution-run")
				meta := sdk.ConversationArtifact{ID: "art_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Version: 1, Kind: "markdown", Title: "Original task artifact", SourceConversationID: "producer", SourceRunID: "execution-run"}
				repo.artifact = persistence.ConversationArtifactRecord{Artifact: meta, Sources: &sdk.ConversationSources{Version: 1}}
				view.Artifacts = []sdk.ConversationArtifact{meta}
				var withArtifact any = map[string]any{"task": compactConversationTask(view)}
				if key == "task_list" {
					withArtifact = sdk.ConversationTaskPage{Items: []sdk.ConversationTaskSummary{view.ConversationTaskSummary}, Complete: true}
				}
				result.Content, _ = json.Marshal(withArtifact)
				if err := read(); err != nil {
					t.Fatal("authorized original artifact metadata hidden", err)
				}
				policy.denied["artifact_read"] = true
				if err := read(); err == nil {
					t.Fatal("revoked artifact metadata released in task")
				}
				delete(policy.denied, "artifact_read")
				policy.disabled["artifact_read"] = true
				if err := read(); err == nil {
					t.Fatal("disabled artifact metadata released in task")
				}
				delete(policy.disabled, "artifact_read")
				repo.artifact.Artifact.Title = "Changed metadata"
				if err := read(); err == nil {
					t.Fatal("changed artifact metadata accepted")
				}
				repo.artifact.Artifact = meta
				repo.artifact.Sources = &sdk.ConversationSources{Version: 1, Omitted: []sdk.ConversationRunReference{owner}}
				if err := read(); err == nil {
					t.Fatal("omitted artifact provenance accepted")
				}
				repo.artifact.Sources = &sdk.ConversationSources{Version: 1}
			}
			if err := read(); err != nil {
				t.Fatal("restoration changed the historical receipt", err)
			}
			if key == "task_update" {
				newer := *repo.task.Brief
				newer.Version++
				repo.task.Brief, repo.task.AgreementRevision = &newer, repo.task.AgreementRevision+1
				if err := read(); err != nil {
					t.Fatal("a later agreement hid the immutable task_update receipt", err)
				}
			}
			collaborationDef, _ := collaborationTool("delegation_get")
			privateContext, _ := json.Marshal(sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "released"}, Messages: []sdk.ConversationAgentMessage{{ID: "private-message", Content: "private communication"}}})
			repo.snapshots["run"] = persistence.ConversationSourceSnapshot{Calls: []persistence.ConversationToolExecution{{Step: 0, State: "completed", Definition: collaborationDef, Call: sdk.ConversationToolCall{ID: "working-context", Name: "delegation_get", Arguments: `{"id":"released"}`}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: "released", Content: privateContext}}}}
			if err := read(); !collaborationDenied(err) {
				t.Fatal("task text inherited the provenance-only communication exception", err)
			}
			delete(repo.snapshots, "run")
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := s.sourceAudit(a).record(cancelled, owner, record); err == nil {
				t.Fatal("cancelled task receipt read accepted")
			}
		})
	}
}
