package application

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

type conversationTaskCatalogHost struct {
	definitions []agentsdk.ConversationToolDefinition
	denied      map[string]bool
}

type idleConversationTaskRepository struct{ calls atomic.Int32 }

func (r *idleConversationTaskRepository) LaunchConversationTask(context.Context, string) (persistence.ConversationTaskLaunch, bool, error) {
	r.calls.Add(1)
	return persistence.ConversationTaskLaunch{}, false, nil
}
func (*idleConversationTaskRepository) ConversationTask(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error) {
	return agentsdk.ConversationTask{}, nil
}

func (h *conversationTaskCatalogHost) ConversationTools(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	return h.definitions, nil
}
func (h *conversationTaskCatalogHost) AuthorizeConversationTool(_ context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: !h.denied[in.Definition.Key], Revision: "auth:" + in.Definition.Key}, nil
}
func (*conversationTaskCatalogHost) InvokeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return agentsdk.ConversationToolResult{}, nil
}
func (*conversationTaskCatalogHost) ReconcileConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return agentsdk.ConversationToolResult{}, nil
}

func taskDefinitions(t *testing.T, keys ...string) []agentsdk.ConversationToolDefinition {
	t.Helper()
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	var out []agentsdk.ConversationToolDefinition
	for _, definition := range agentsdk.PersonalConversationTools() {
		if wanted[definition.Key] {
			out = append(out, definition)
		}
	}
	if len(out) != len(keys) {
		t.Fatalf("missing task test definitions: %v", keys)
	}
	return out
}

func taskStartRequest(t *testing.T, allowed []string, budget agentsdk.ConversationTaskBudget) agentsdk.ConversationToolRequest {
	t.Helper()
	arguments, err := json.Marshal(agentsdk.ConversationTaskStart{Goal: "核对发布", Input: "build 42", AllowedTools: allowed, Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	return agentsdk.ConversationToolRequest{
		Authority:      agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"},
		ConversationID: "conversation", RunID: "run", Definition: agentsdk.BackgroundTaskConversationTool(),
		Call: agentsdk.ConversationToolCall{Name: "task_start", Arguments: string(arguments)},
	}
}

func TestPrepareConversationTaskFreezesGoalScopeAuthorizationAndBudget(t *testing.T) {
	host := &conversationTaskCatalogHost{definitions: taskDefinitions(t, "task_start", "time_now", "calculate")}
	s := &ConversationService{options: ConversationOptions{ToolHost: host, MaxInputBytes: 16 * 1024, MaxSteps: 12, MaxToolCalls: 8, MaxOutputBytes: 8192, RunTimeout: 5 * time.Minute}}
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 4, MaxToolCalls: 3, MaxOutputBytes: 2048, TimeoutSeconds: 30}
	task, err := s.prepareConversationTask(t.Context(), taskStartRequest(t, []string{"time_now", "calculate"}, budget))
	if err != nil {
		t.Fatal(err)
	}
	if task.SourceConversationID != "conversation" || task.SourceRunID != "run" || task.Goal != "核对发布" || task.Input != "build 42" || task.Budget != budget || len(task.ToolScope) != 2 {
		t.Fatalf("task fields were not frozen: %+v", task)
	}
	for index, key := range []string{"time_now", "calculate"} {
		scope := task.ToolScope[index]
		if scope.Key != key || scope.Version == "" || scope.ActionKey == "" || scope.DefinitionHash == "" || scope.AuthorizationRevision != "auth:"+key {
			t.Fatalf("scope[%d] incomplete: %+v", index, scope)
		}
	}
}

func TestPrepareConversationTaskRejectsRecursiveUnavailableAndDeploymentExceedingScope(t *testing.T) {
	host := &conversationTaskCatalogHost{definitions: taskDefinitions(t, "task_start", "time_now"), denied: map[string]bool{"time_now": true}}
	s := &ConversationService{options: ConversationOptions{ToolHost: host, MaxInputBytes: 16 * 1024, MaxSteps: 4, MaxToolCalls: 4, MaxOutputBytes: 2048, RunTimeout: time.Minute}}
	valid := agentsdk.ConversationTaskBudget{MaxSteps: 2, MaxToolCalls: 2, MaxOutputBytes: 1024, TimeoutSeconds: 30}
	for _, test := range []struct {
		name    string
		allowed []string
		budget  agentsdk.ConversationTaskBudget
		code    string
	}{
		{"recursive", []string{"task_start"}, valid, "agent.conversation.task_scope_invalid"},
		{"denied", []string{"time_now"}, valid, "agent.conversation.tool_access_denied"},
		{"step budget", nil, agentsdk.ConversationTaskBudget{MaxSteps: 5, MaxToolCalls: 2, MaxOutputBytes: 1024, TimeoutSeconds: 30}, "agent.conversation.task_start_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := s.prepareConversationTask(t.Context(), taskStartRequest(t, test.allowed, test.budget))
			var coded *agentsdk.Error
			if !errors.As(err, &coded) || coded.Code != test.code {
				t.Fatalf("error=%v, want %s", err, test.code)
			}
		})
	}
}

func TestConversationTaskRunCatalogAndBudgetsStayFrozen(t *testing.T) {
	definitions := taskDefinitions(t, "task_start", "time_now", "calculate")
	host := &conversationTaskCatalogHost{definitions: definitions}
	checked := []string{}
	s := &ConversationService{options: ConversationOptions{ToolHost: host, ToolAvailability: catalogAvailabilityFunc(func(_ context.Context, _ agentsdk.ConversationAuthority, key string) (bool, error) {
		checked = append(checked, key)
		if key != "time_now" {
			return false, errors.New("unselected connection must not be inspected")
		}
		return true, nil
	}), MaxSteps: 12, MaxToolCalls: 8, MaxOutputBytes: 8192, RunTimeout: 5 * time.Minute}}
	var selected agentsdk.ConversationToolDefinition
	for _, definition := range definitions {
		if definition.Key == "time_now" {
			selected = definition
		}
	}
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 2, MaxOutputBytes: 1024, TimeoutSeconds: 15}
	claim := persistence.ConversationClaim{Run: agentsdk.ConversationRun{BackgroundTask: &agentsdk.ConversationTaskExecution{
		TaskID: "task", Budget: budget, ToolScope: []agentsdk.ConversationTaskToolScope{{Key: selected.Key, Version: selected.Version, ActionKey: selected.ActionKey, DefinitionHash: conversationDigest(selected)}},
	}}}
	visible, compiled, err := s.executionCatalogForRun(t.Context(), claim)
	if err != nil || len(visible) != 1 || visible[0].Key != "time_now" || len(compiled) != 1 {
		t.Fatalf("task catalog=%+v compiled=%d err=%v", visible, len(compiled), err)
	}
	if len(checked) != 1 || checked[0] != "time_now" {
		t.Fatalf("task inspected tools outside its frozen scope: %v", checked)
	}
	steps, calls, output, timeout := s.conversationRunLimits(claim)
	if steps != 3 || calls != 2 || output != 1024 || timeout != 15*time.Second {
		t.Fatalf("task limits=%d %d %d %s", steps, calls, output, timeout)
	}
	for index := range host.definitions {
		if host.definitions[index].Key == "time_now" {
			host.definitions[index].Description += " changed without version"
		}
	}
	if _, _, err = s.executionCatalogForRun(t.Context(), claim); err == nil {
		t.Fatal("changed tool contract was accepted by the background run")
	}
}

func TestConversationTaskWorkerWaitsForStateTransitionWhenQueueIsEmpty(t *testing.T) {
	repo := &idleConversationTaskRepository{}
	s := &ConversationService{runtimeID: "runtime", options: ConversationOptions{Poll: 5 * time.Millisecond}, taskWake: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	s.wg.Add(1)
	go s.conversationTaskWorker(ctx, repo)
	deadline := time.Now().Add(time.Second)
	for repo.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if repo.calls.Load() != 1 {
		cancel()
		s.wg.Wait()
		t.Fatalf("startup recovery calls=%d", repo.calls.Load())
	}
	time.Sleep(50 * time.Millisecond)
	if repo.calls.Load() != 1 {
		cancel()
		s.wg.Wait()
		t.Fatalf("empty queue was polled: calls=%d", repo.calls.Load())
	}
	s.signalConversationTasks()
	deadline = time.Now().Add(time.Second)
	for repo.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	s.wg.Wait()
	if repo.calls.Load() != 2 {
		t.Fatalf("state transition did not wake worker: calls=%d", repo.calls.Load())
	}
}
