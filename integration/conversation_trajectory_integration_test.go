package integration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	application "github.com/domainry/domainry-agent/internal/application"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
)

func TestConversationTrajectoryReplayAndForkDoNotRepeatRecordedEffects(t *testing.T) {
	repo := conversationRepository(t)
	host := &executionHost{allowed: true}
	definitions, err := host.ConversationTools(t.Context(), conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	model := &executionModel{}
	model.step = func(number int, input agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		switch number {
		case 1:
			return model.callResult(`{"title":"original effect"}`), nil
		case 2:
			if len(input.Messages) == 0 || input.Messages[len(input.Messages)-1].Role != "tool" {
				t.Fatal("source result was not returned to the model")
			}
			return model.answerResult(), nil
		case 3:
			joined := ""
			for _, message := range input.Messages {
				joined += "\n" + message.Role + ":" + message.Content
			}
			if !strings.Contains(joined, "Forked completed-run context follows") || !strings.Contains(joined, "Recorded tool calls (historical; do not re-run automatically)") || !strings.Contains(joined, "Recorded tool result for call call-one") || !strings.Contains(joined, "Try another approach") {
				t.Fatalf("fork did not receive the copied stable context: %s", joined)
			}
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "Alternative answer"}, FinishReason: "stop", Model: "tool-model"}, nil
		default:
			t.Fatalf("unexpected live model call %d", number)
			return agentsdk.ConversationStepResult{}, nil
		}
	}
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, ToolDefinitions: definitions})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	trajectoryService, ok := any(service).(agentsdk.ConversationTrajectoryService)
	if !ok {
		t.Fatal("trajectory service is not exposed")
	}
	authority := conversationAuthority()
	source, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "trajectory-source", Title: "Original"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), source.ID, agentsdk.ConversationSend{ClientMessageID: "trajectory-message", Message: "Create the original item"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	completed := waitConversation(t, service, source.ID, run.ID)
	if completed.Status != "completed" {
		t.Fatalf("source run=%+v", completed)
	}

	trajectory, err := trajectoryService.ConversationTrajectory(t.Context(), source.ID, run.ID, authority)
	if err != nil {
		t.Fatal(err)
	}
	if trajectory.Mode != agentsdk.ConversationReplayDisplay || trajectory.BoundaryEventSeq != completed.LastEventSeq || trajectory.SHA256 == "" || len(trajectory.Requests) != 2 || len(trajectory.Responses) != 2 || len(trajectory.Tools) != 1 || trajectory.Tools[0].Call.ID != "call-one" || trajectory.Tools[0].Result == nil || trajectory.Tools[0].Definition.Definition.Key != "create_item" || len(trajectory.Tools[0].Definition.Definition.InputSchema) == 0 || trajectory.Tools[0].Definition.SHA256 == "" || len(trajectory.Requests[0].Tools) != 1 || strings.Contains(string(trajectoryJSON(t, trajectory)), "reasoning_content") {
		t.Fatalf("trajectory=%+v", trajectory)
	}
	exported, err := trajectoryService.ExportConversationTrajectory(t.Context(), source.ID, run.ID, authority)
	if err != nil || exported.ContentType != "application/json" || exported.Filename == "" || exported.SHA256 == "" || len(exported.Data) == 0 {
		t.Fatalf("export=%+v err=%v", exported, err)
	}
	var decoded agentsdk.ConversationTrajectory
	if json.Unmarshal(exported.Data, &decoded) != nil || decoded.SHA256 != trajectory.SHA256 {
		t.Fatal("export did not preserve the authorized trajectory")
	}

	for _, mode := range []string{agentsdk.ConversationReplayDisplay, agentsdk.ConversationReplayModelFixture} {
		replay, replayErr := trajectoryService.ReplayConversationTrajectory(t.Context(), source.ID, run.ID, agentsdk.ConversationTrajectoryReplayRequest{Mode: mode}, authority)
		if replayErr != nil || replay.EffectsExecuted || replay.ConsumedRequests != 2 || replay.TrajectorySHA256 != trajectory.SHA256 {
			t.Fatalf("mode=%s replay=%+v err=%v", mode, replay, replayErr)
		}
		if mode == agentsdk.ConversationReplayDisplay && (len(replay.RecordedResponses) != 0 || len(replay.RecordedTools) != 0) {
			t.Fatal("display replay unexpectedly materialized fixture streams")
		}
		if mode == agentsdk.ConversationReplayModelFixture && (len(replay.RecordedResponses) != 2 || len(replay.RecordedTools) != 1) {
			t.Fatal("model fixture did not return the recorded stream")
		}
	}
	model.mu.Lock()
	modelCalls := model.calls
	model.mu.Unlock()
	host.mu.Lock()
	toolCalls := host.invokes
	host.mu.Unlock()
	if modelCalls != 2 || toolCalls != 1 {
		t.Fatalf("read-only replay made live calls: model=%d tool=%d", modelCalls, toolCalls)
	}

	live, err := trajectoryService.ReplayConversationTrajectory(t.Context(), source.ID, run.ID, agentsdk.ConversationTrajectoryReplayRequest{Mode: agentsdk.ConversationReplayLiveRerun, Fork: &agentsdk.ConversationForkRequest{ClientID: "trajectory-fork", Title: "Alternative"}}, authority)
	if err != nil || live.EffectsExecuted || !live.ReadyForInput || live.Fork == nil || live.Fork.Fork == nil || live.Fork.Fork.ConversationID != source.ID || live.Fork.ActiveRunID != "" {
		t.Fatalf("live rerun=%+v err=%v", live, err)
	}
	childMessages, err := service.Messages(t.Context(), live.Fork.ID, agentsdk.ConversationMessageQuery{}, authority)
	if err != nil || len(childMessages.Items) != 0 {
		t.Fatalf("fork created visible or executable history: %+v err=%v", childMessages, err)
	}
	host.mu.Lock()
	toolCalls = host.invokes
	host.mu.Unlock()
	if toolCalls != 1 {
		t.Fatal("fork repeated a historical effect")
	}
	childRun, err := service.Send(t.Context(), live.Fork.ID, agentsdk.ConversationSend{ClientMessageID: "fork-message", Message: "Try another approach"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	childCompleted := waitConversation(t, service, live.Fork.ID, childRun.ID)
	if childCompleted.Status != "completed" {
		t.Fatalf("child run=%+v", childCompleted)
	}
	host.mu.Lock()
	toolCalls = host.invokes
	host.mu.Unlock()
	if toolCalls != 1 {
		t.Fatal("sending new input repeated a historical effect")
	}
	comparison, err := trajectoryService.CompareConversationTrajectories(t.Context(), source.ID, run.ID, agentsdk.ConversationTrajectoryCompareRequest{Other: agentsdk.ConversationRunReference{ConversationID: live.Fork.ID, RunID: childRun.ID}}, authority)
	if err != nil || comparison.Equal || comparison.LeftSHA256 == comparison.RightSHA256 || len(comparison.Differences) == 0 {
		t.Fatalf("comparison=%+v err=%v", comparison, err)
	}

	host.mu.Lock()
	host.allowed = false
	host.mu.Unlock()
	revoked := []struct {
		name string
		call func() error
	}{
		{"view", func() error {
			_, err := trajectoryService.ConversationTrajectory(t.Context(), source.ID, run.ID, authority)
			return err
		}},
		{"export", func() error {
			_, err := trajectoryService.ExportConversationTrajectory(t.Context(), source.ID, run.ID, authority)
			return err
		}},
		{"fixture", func() error {
			_, err := trajectoryService.ReplayConversationTrajectory(t.Context(), source.ID, run.ID, agentsdk.ConversationTrajectoryReplayRequest{Mode: agentsdk.ConversationReplayModelFixture}, authority)
			return err
		}},
		{"compare", func() error {
			_, err := trajectoryService.CompareConversationTrajectories(t.Context(), source.ID, run.ID, agentsdk.ConversationTrajectoryCompareRequest{Other: agentsdk.ConversationRunReference{ConversationID: live.Fork.ID, RunID: childRun.ID}}, authority)
			return err
		}},
		{"fork", func() error {
			_, err := trajectoryService.ForkConversation(t.Context(), source.ID, run.ID, agentsdk.ConversationForkRequest{ClientID: "revoked-fork", Title: "Must not exist"}, authority)
			return err
		}},
	}
	for _, check := range revoked {
		err = check.call()
		var denied *agentsdk.Error
		if !errors.As(err, &denied) || denied.Code != "agent.conversation.tool_access_denied" {
			t.Fatalf("%s after source revocation err=%v", check.name, err)
		}
	}
	page, err := service.List(t.Context(), agentsdk.ConversationQuery{IncludeArchived: true}, authority)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("revoked fork created a child: page=%+v err=%v", page, err)
	}
}

func trajectoryJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
