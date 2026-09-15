package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func trajectoryMessage(message agentsdk.ConversationStepMessage) agentsdk.ConversationTrajectoryMessage {
	return agentsdk.ConversationTrajectoryMessage{Role: message.Role, Content: message.Content, ContentBlocks: cloneConversationContentBlocks(message.ContentBlocks), ToolCalls: append([]agentsdk.ConversationToolCall(nil), message.ToolCalls...), ToolCallID: message.ToolCallID, IsError: message.IsError}
}

func trajectoryContext(window *agentsdk.ConversationContextWindow, manifest *agentsdk.ConversationContextManifest, compaction *agentsdk.ConversationContextCompaction) *agentsdk.ConversationContextView {
	view := &agentsdk.ConversationContextView{Window: window, Compaction: compaction}
	if manifest != nil {
		for _, source := range manifest.Sources {
			view.Sources = append(view.Sources, agentsdk.ConversationContextSourceView{Key: source.Key, Kind: source.Kind, Scope: source.Scope, Refresh: source.Refresh, Version: source.Version, Order: source.Order, StablePrefix: source.StablePrefix, UpdatedAt: source.UpdatedAt})
		}
		view.Changes = append([]agentsdk.ConversationContextSourceChange(nil), manifest.Changes...)
	}
	if view.Window == nil && view.Compaction == nil && len(view.Sources) == 0 {
		return nil
	}
	return view
}

func trajectoryToolDefinition(definition agentsdk.ConversationToolDefinition) agentsdk.ConversationTrajectoryToolDefinition {
	return agentsdk.ConversationTrajectoryToolDefinition{Definition: definition, SHA256: conversationDigest(definition)}
}

func trajectoryRequest(index, step int, purpose string, identity agentsdk.ConversationModelIdentity, effort string, messages []agentsdk.ConversationStepMessage, definitions []agentsdk.ConversationToolDefinition, context *agentsdk.ConversationContextView) agentsdk.ConversationTrajectoryRequest {
	out := agentsdk.ConversationTrajectoryRequest{Index: index, Step: step, Purpose: purpose, Model: identity, ReasoningEffort: effort, Messages: make([]agentsdk.ConversationTrajectoryMessage, 0, len(messages)), Tools: make([]agentsdk.ConversationTrajectoryToolDefinition, 0, len(definitions)), Context: context}
	for _, message := range messages {
		out.Messages = append(out.Messages, trajectoryMessage(message))
	}
	for _, definition := range definitions {
		out.Tools = append(out.Tools, trajectoryToolDefinition(definition))
	}
	out.SHA256 = conversationDigest([]any{out.Step, out.Purpose, out.Model, out.ReasoningEffort, out.Messages, out.Tools, out.Context})
	return out
}

func trajectoryResponse(requestIndex, step int, result agentsdk.ConversationStepResult) agentsdk.ConversationTrajectoryResponse {
	out := agentsdk.ConversationTrajectoryResponse{RequestIndex: requestIndex, Step: step, FinishReason: result.FinishReason, Model: result.Model, Message: trajectoryMessage(result.Message), Usage: result.Usage}
	out.SHA256 = conversationDigest([]any{out.Step, out.FinishReason, out.Model, out.Message, out.Usage})
	return out
}

func (s *ConversationService) conversationTrajectorySnapshot(ctx context.Context, conversationID, runID, consumer string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTrajectory, persistence.ConversationSourceSnapshot, error) {
	var empty persistence.ConversationSourceSnapshot
	if err := s.authorizeCollaborationConversation(ctx, conversationID, "execution_read", a); err != nil {
		return agentsdk.ConversationTrajectory{}, empty, err
	}
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationTrajectory{}, empty, err
	}
	if !conversationKey(conversationID) || !conversationKey(runID) {
		return agentsdk.ConversationTrajectory{}, empty, conversationFailure("bad_request", "trajectory_reference_invalid")
	}
	repo, ok := s.repo.(persistence.ConversationSourceRepository)
	if !ok {
		return agentsdk.ConversationTrajectory{}, empty, conversationFailure("unavailable", "trajectory_unavailable")
	}
	ref := agentsdk.ConversationRunReference{ConversationID: conversationID, RunID: runID}
	readCtx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	if _, err := s.sourceAudit(a, consumer).run(readCtx, ref); err != nil {
		return agentsdk.ConversationTrajectory{}, empty, err
	}
	snapshot, err := repo.ConversationSourceSnapshot(readCtx, ref, a)
	if err != nil {
		return agentsdk.ConversationTrajectory{}, empty, err
	}
	if snapshot.Run.Status != "completed" || snapshot.Run.AssistantMessageID == "" || snapshot.Run.CompletedAt == nil || snapshot.FinalMessage == nil || snapshot.FinalMessage.ID != snapshot.Run.AssistantMessageID || snapshot.FinalMessage.RunID != runID {
		return agentsdk.ConversationTrajectory{}, empty, conversationFailure("conflict", "trajectory_boundary_unstable")
	}
	out := agentsdk.ConversationTrajectory{Version: agentsdk.ConversationTrajectoryVersion, Mode: agentsdk.ConversationReplayDisplay, Source: ref, BoundaryEventSeq: snapshot.Run.LastEventSeq, RunStatus: snapshot.Run.Status, Requests: []agentsdk.ConversationTrajectoryRequest{}, Responses: []agentsdk.ConversationTrajectoryResponse{}, Tools: []agentsdk.ConversationTrajectoryTool{}, RecordedAt: *snapshot.Run.CompletedAt}
	if len(snapshot.Steps) == 0 {
		if snapshot.Input == nil {
			return out, empty, conversationFailure("conflict", "trajectory_request_missing")
		}
		messages, err := conversationStepMessages(*snapshot.Input)
		if err != nil {
			return out, empty, err
		}
		out.Requests = append(out.Requests, trajectoryRequest(0, -1, snapshot.Input.Purpose, snapshot.Input.ModelIdentity, snapshot.Input.ReasoningEffort, messages, nil, trajectoryContext(snapshot.Input.ContextWindow, snapshot.Input.Context, nil)))
		result := agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: snapshot.FinalMessage.Content}, FinishReason: "stop", Model: snapshot.Run.Model, Usage: snapshot.Run.Usage}
		out.Responses = append(out.Responses, trajectoryResponse(0, -1, result))
	} else {
		callRecords := make(map[string]persistence.ConversationToolExecution, len(snapshot.Calls))
		children := map[string][]persistence.ConversationToolExecution{}
		for _, call := range snapshot.Calls {
			callRecords[fmt.Sprintf("%d:%s", call.Step, call.Call.ID)] = call
			if call.ParentCallID != "" {
				key := fmt.Sprintf("%d:%s", call.Step, call.ParentCallID)
				children[key] = append(children[key], call)
			}
		}
		for key := range children {
			sort.Slice(children[key], func(i, j int) bool { return children[key][i].DispatchIndex < children[key][j].DispatchIndex })
		}
		for _, step := range snapshot.Steps {
			requestIndex := len(out.Requests)
			out.Requests = append(out.Requests, trajectoryRequest(requestIndex, step.Number, "step", step.Input.ModelIdentity, step.Input.ReasoningEffort, step.Input.Messages, step.Input.Tools, trajectoryContext(step.Input.ContextWindow, step.Input.Context, step.Input.Compaction)))
			if step.Result == nil {
				return out, empty, conversationFailure("conflict", "trajectory_response_missing")
			}
			out.Responses = append(out.Responses, trajectoryResponse(requestIndex, step.Number, *step.Result))
			for _, call := range step.Result.Message.ToolCalls {
				record, exists := callRecords[fmt.Sprintf("%d:%s", step.Number, call.ID)]
				if !exists || record.Call != call {
					return out, empty, conversationFailure("conflict", "trajectory_tool_missing")
				}
				tool := agentsdk.ConversationTrajectoryTool{Step: step.Number, Call: call, Definition: trajectoryToolDefinition(record.Definition), State: record.State, Result: record.Result}
				tool.SHA256 = conversationDigest([]any{tool.Step, tool.Call, tool.Definition, tool.State, tool.Result})
				out.Tools = append(out.Tools, tool)
				for index, child := range children[fmt.Sprintf("%d:%s", step.Number, call.ID)] {
					if child.DispatchIndex != index {
						return out, empty, conversationFailure("conflict", "trajectory_tool_missing")
					}
					nested := agentsdk.ConversationTrajectoryTool{ParentCallID: child.ParentCallID, DispatchIndex: child.DispatchIndex, Step: child.Step, Call: child.Call, Definition: trajectoryToolDefinition(child.Definition), State: child.State, Result: child.Result}
					nested.SHA256 = conversationDigest([]any{nested.ParentCallID, nested.DispatchIndex, nested.Step, nested.Call, nested.Definition, nested.State, nested.Result})
					out.Tools = append(out.Tools, nested)
				}
			}
		}
	}
	out.SHA256 = conversationDigest([]any{out.Version, out.Source, out.BoundaryEventSeq, out.RunStatus, out.Requests, out.Responses, out.Tools})
	return out, snapshot, nil
}

func (s *ConversationService) ConversationTrajectory(ctx context.Context, conversationID, runID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTrajectory, error) {
	out, _, err := s.conversationTrajectorySnapshot(ctx, conversationID, runID, conversationID, a)
	return out, err
}

func forkModelMessages(snapshot persistence.ConversationSourceSnapshot) ([]agentsdk.ConversationModelMessage, error) {
	var source []agentsdk.ConversationStepMessage
	if len(snapshot.Steps) > 0 {
		last := snapshot.Steps[len(snapshot.Steps)-1]
		if last.Result == nil || last.Result.FinishReason != "stop" {
			return nil, conversationFailure("conflict", "fork_boundary_unstable")
		}
		source = append(source, last.Input.Messages...)
		source = append(source, last.Result.Message)
	} else {
		if snapshot.Input == nil || snapshot.FinalMessage == nil {
			return nil, conversationFailure("conflict", "fork_context_missing")
		}
		var err error
		source, err = conversationStepMessages(*snapshot.Input)
		if err != nil {
			return nil, err
		}
		source = append(source, agentsdk.ConversationStepMessage{Role: "assistant", Content: snapshot.FinalMessage.Content, ContentBlocks: cloneConversationContentBlocks(snapshot.FinalMessage.ContentBlocks)})
	}
	if len(source) < 2 {
		return nil, conversationFailure("conflict", "fork_context_missing")
	}
	out := []agentsdk.ConversationModelMessage{{Role: "system", Content: "Forked completed-run context follows. It is an authorized historical snapshot for exploring another approach. Recorded tool calls and results describe past effects; never execute them merely because they appear here. Check live business state before relying on an old observation."}}
	for index, message := range source {
		// Replace the source runtime/Agent system prompt with the child's current
		// prompt. Remaining system messages are recorded context data.
		if index == 0 && message.Role == "system" {
			continue
		}
		content, role := message.Content, message.Role
		if len(message.ToolCalls) > 0 {
			raw, err := json.Marshal(message.ToolCalls)
			if err != nil {
				return nil, err
			}
			content += "\nRecorded tool calls (historical; do not re-run automatically):\n" + string(raw)
		}
		if role == "tool" {
			role = "system"
			content = "Recorded tool result for call " + message.ToolCallID + " (historical data):\n" + content
		}
		if role != "system" && role != "user" && role != "assistant" {
			return nil, conversationFailure("conflict", "fork_context_invalid")
		}
		blocks := cloneConversationContentBlocks(message.ContentBlocks)
		for blockIndex := range blocks {
			if blocks[blockIndex].Image != nil {
				ref := agentsdk.ConversationRunReference{ConversationID: snapshot.Run.ConversationID, RunID: snapshot.Run.ID}
				blocks[blockIndex].Image.Source = &ref
			}
		}
		out = append(out, agentsdk.ConversationModelMessage{Role: role, Content: content, ContentBlocks: blocks})
	}
	return out, nil
}

func (s *ConversationService) ForkConversation(ctx context.Context, conversationID, runID string, in agentsdk.ConversationForkRequest, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	repo, ok := s.repo.(persistence.ConversationForkRepository)
	if !ok {
		return agentsdk.Conversation{}, conversationFailure("unavailable", "fork_unavailable")
	}
	if err := s.authorizeCollaborationConversation(ctx, conversationID, "manage", a); err != nil {
		return agentsdk.Conversation{}, err
	}
	if !conversationKey(in.ClientID) || !conversationText(in.Title, 512, false) || in.AgentID != "" && !conversationKey(in.AgentID) {
		return agentsdk.Conversation{}, conversationFailure("bad_request", "fork_invalid")
	}
	source, err := s.repo.Get(ctx, conversationID, a)
	if err != nil {
		return agentsdk.Conversation{}, err
	}
	if source.DelegationID != "" {
		return agentsdk.Conversation{}, conversationFailure("conflict", "fork_source_invalid")
	}
	if in.AgentID == "" {
		in.AgentID = source.AgentID
	}
	if in.AgentID != "" {
		var snapshot *agentsdk.ConversationAgentSnapshot
		if snapshot, err = s.freezeConversationAgent(ctx, in.AgentID, a); err != nil {
			return agentsdk.Conversation{}, err
		}
		if ctx, err = s.selectConversationAgent(ctx, snapshot, a); err != nil {
			return agentsdk.Conversation{}, err
		}
	}
	if strings.TrimSpace(in.Title) == "" {
		in.Title = source.Title + "（分叉）"
	}
	trajectory, snapshot, err := s.conversationTrajectorySnapshot(ctx, conversationID, runID, "fork:"+in.ClientID, a)
	if err != nil {
		return agentsdk.Conversation{}, err
	}
	if snapshot.Run.BackgroundTask != nil {
		return agentsdk.Conversation{}, conversationFailure("conflict", "fork_source_invalid")
	}
	messages, err := forkModelMessages(snapshot)
	if err != nil {
		return agentsdk.Conversation{}, err
	}
	if conversationModelMessagesHaveImages(messages) {
		descriptor, descriptorErr := s.currentConversationModelDescriptor(ctx)
		if descriptorErr != nil {
			return agentsdk.Conversation{}, descriptorErr
		}
		if !descriptor.Capabilities.ImageInput {
			return agentsdk.Conversation{}, conversationFailure("bad_request", "model_image_input_unsupported")
		}
	}
	createdAt := trajectory.RecordedAt
	seed := persistence.ConversationForkSeed{Version: agentsdk.ConversationTrajectoryVersion, Source: trajectory.Source, BoundaryEventSeq: trajectory.BoundaryEventSeq, SourceSHA256: trajectory.SHA256, Messages: messages, CreatedAt: createdAt}
	return repo.ForkConversation(ctx, in, seed, a)
}

func (s *ConversationService) conversationForkContext(ctx context.Context, claim persistence.ConversationClaim) ([]agentsdk.ConversationModelMessage, []agentsdk.ConversationRunReference, error) {
	repo, ok := s.repo.(persistence.ConversationForkRepository)
	if !ok {
		return nil, nil, nil
	}
	seed, found, err := repo.ConversationForkSeed(ctx, claim.Run.ConversationID, claim.Authority)
	if err != nil || !found {
		return nil, nil, err
	}
	trajectory, _, err := s.conversationTrajectorySnapshot(ctx, seed.Source.ConversationID, seed.Source.RunID, claim.Run.ConversationID, claim.Authority)
	if err != nil {
		return nil, nil, err
	}
	if trajectory.BoundaryEventSeq != seed.BoundaryEventSeq || trajectory.SHA256 != seed.SourceSHA256 {
		return nil, nil, conversationFailure("conflict", "fork_snapshot_changed")
	}
	return append([]agentsdk.ConversationModelMessage(nil), seed.Messages...), []agentsdk.ConversationRunReference{seed.Source}, nil
}

func (s *ConversationService) ExportConversationTrajectory(ctx context.Context, conversationID, runID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTrajectoryExport, error) {
	trajectory, err := s.ConversationTrajectory(ctx, conversationID, runID, a)
	if err != nil {
		return agentsdk.ConversationTrajectoryExport{}, err
	}
	data, err := json.MarshalIndent(trajectory, "", "  ")
	if err != nil {
		return agentsdk.ConversationTrajectoryExport{}, err
	}
	return agentsdk.ConversationTrajectoryExport{Filename: "conversation-trajectory-" + runID + ".json", ContentType: "application/json", SHA256: conversationDigest(json.RawMessage(data)), Data: data}, nil
}

func (s *ConversationService) ReplayConversationTrajectory(ctx context.Context, conversationID, runID string, in agentsdk.ConversationTrajectoryReplayRequest, a agentsdk.ConversationAuthority) (agentsdk.ConversationTrajectoryReplay, error) {
	if in.Mode != agentsdk.ConversationReplayDisplay && in.Mode != agentsdk.ConversationReplayModelFixture && in.Mode != agentsdk.ConversationReplayLiveRerun {
		return agentsdk.ConversationTrajectoryReplay{}, conversationFailure("bad_request", "trajectory_replay_mode_invalid")
	}
	if in.Mode == agentsdk.ConversationReplayLiveRerun {
		if in.Fork == nil {
			return agentsdk.ConversationTrajectoryReplay{}, conversationFailure("bad_request", "fork_invalid")
		}
		trajectory, err := s.ConversationTrajectory(ctx, conversationID, runID, a)
		if err != nil {
			return agentsdk.ConversationTrajectoryReplay{}, err
		}
		fork, err := s.ForkConversation(ctx, conversationID, runID, *in.Fork, a)
		if err != nil {
			return agentsdk.ConversationTrajectoryReplay{}, err
		}
		return agentsdk.ConversationTrajectoryReplay{Mode: in.Mode, Source: trajectory.Source, TrajectorySHA256: trajectory.SHA256, ConsumedRequests: len(trajectory.Requests), EffectsExecuted: false, ReadyForInput: true, Fork: &fork}, nil
	}
	if in.Fork != nil {
		return agentsdk.ConversationTrajectoryReplay{}, conversationFailure("bad_request", "trajectory_replay_invalid")
	}
	trajectory, err := s.ConversationTrajectory(ctx, conversationID, runID, a)
	if err != nil {
		return agentsdk.ConversationTrajectoryReplay{}, err
	}
	out := agentsdk.ConversationTrajectoryReplay{Mode: in.Mode, Source: trajectory.Source, TrajectorySHA256: trajectory.SHA256, ConsumedRequests: len(trajectory.Requests), EffectsExecuted: false}
	if in.Mode == agentsdk.ConversationReplayModelFixture {
		if len(trajectory.Requests) != len(trajectory.Responses) {
			return agentsdk.ConversationTrajectoryReplay{}, conversationFailure("conflict", "trajectory_fixture_incomplete")
		}
		for index, response := range trajectory.Responses {
			if response.RequestIndex != index {
				return agentsdk.ConversationTrajectoryReplay{}, conversationFailure("conflict", "trajectory_fixture_incomplete")
			}
		}
		out.RecordedResponses = append([]agentsdk.ConversationTrajectoryResponse(nil), trajectory.Responses...)
		out.RecordedTools = append([]agentsdk.ConversationTrajectoryTool(nil), trajectory.Tools...)
	}
	return out, nil
}

func trajectoryDifferences(kind string, left, right []string, out []agentsdk.ConversationTrajectoryDifference) []agentsdk.ConversationTrajectoryDifference {
	for index := 0; index < max(len(left), len(right)); index++ {
		var a, b string
		if index < len(left) {
			a = left[index]
		}
		if index < len(right) {
			b = right[index]
		}
		if a != b {
			out = append(out, agentsdk.ConversationTrajectoryDifference{Kind: kind, Index: index, LeftSHA256: a, RightSHA256: b})
		}
	}
	return out
}

func (s *ConversationService) CompareConversationTrajectories(ctx context.Context, conversationID, runID string, in agentsdk.ConversationTrajectoryCompareRequest, a agentsdk.ConversationAuthority) (agentsdk.ConversationTrajectoryComparison, error) {
	if !conversationKey(in.Other.ConversationID) || !conversationKey(in.Other.RunID) || in.Other.BeforeStep != 0 {
		return agentsdk.ConversationTrajectoryComparison{}, conversationFailure("bad_request", "trajectory_reference_invalid")
	}
	left, err := s.ConversationTrajectory(ctx, conversationID, runID, a)
	if err != nil {
		return agentsdk.ConversationTrajectoryComparison{}, err
	}
	right, err := s.ConversationTrajectory(ctx, in.Other.ConversationID, in.Other.RunID, a)
	if err != nil {
		return agentsdk.ConversationTrajectoryComparison{}, err
	}
	requests := func(items []agentsdk.ConversationTrajectoryRequest) []string {
		out := make([]string, len(items))
		for i := range items {
			out[i] = items[i].SHA256
		}
		return out
	}
	responses := func(items []agentsdk.ConversationTrajectoryResponse) []string {
		out := make([]string, len(items))
		for i := range items {
			out[i] = items[i].SHA256
		}
		return out
	}
	tools := func(items []agentsdk.ConversationTrajectoryTool) []string {
		out := make([]string, len(items))
		for i := range items {
			out[i] = items[i].SHA256
		}
		return out
	}
	differences := []agentsdk.ConversationTrajectoryDifference{}
	differences = trajectoryDifferences("request", requests(left.Requests), requests(right.Requests), differences)
	differences = trajectoryDifferences("response", responses(left.Responses), responses(right.Responses), differences)
	differences = trajectoryDifferences("tool", tools(left.Tools), tools(right.Tools), differences)
	return agentsdk.ConversationTrajectoryComparison{Left: left.Source, Right: right.Source, LeftSHA256: left.SHA256, RightSHA256: right.SHA256, Equal: left.SHA256 == right.SHA256, Differences: differences}, nil
}

var _ agentsdk.ConversationTrajectoryService = (*ConversationService)(nil)
