package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type conversationLifecycleBinding struct {
	extension agentsdk.ConversationLifecycleExtension
	snapshot  agentsdk.ConversationLifecycleExtensionSnapshot
	stages    map[agentsdk.ConversationLifecycleStage]bool
}

type conversationLifecycleContextLimitKey struct{}

func conversationLifecycleDefinition(extension agentsdk.ConversationLifecycleExtension) (definition agentsdk.ConversationLifecycleExtensionDefinition, err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("conversation lifecycle definition panicked")
		}
	}()
	if extension == nil {
		return definition, fmt.Errorf("conversation lifecycle extension is required")
	}
	return extension.ConversationLifecycleDefinition(), nil
}

func validConversationLifecycleStage(stage agentsdk.ConversationLifecycleStage) bool {
	switch stage {
	case agentsdk.ConversationLifecycleInputAdmitting,
		agentsdk.ConversationLifecycleInputReceived,
		agentsdk.ConversationLifecycleContextAssembling,
		agentsdk.ConversationLifecycleContextCompacting,
		agentsdk.ConversationLifecycleContextAssembled,
		agentsdk.ConversationLifecycleModelRequest,
		agentsdk.ConversationLifecycleModelCompleted,
		agentsdk.ConversationLifecycleModelFailed,
		agentsdk.ConversationLifecycleModelRetry,
		agentsdk.ConversationLifecycleToolBefore,
		agentsdk.ConversationLifecycleToolCompleted,
		agentsdk.ConversationLifecycleToolFailed,
		agentsdk.ConversationLifecycleRunFinished,
		agentsdk.ConversationLifecycleTaskFinished:
		return true
	default:
		return false
	}
}

func conversationLifecyclePostEffectStage(stage agentsdk.ConversationLifecycleStage) bool {
	switch stage {
	case agentsdk.ConversationLifecycleInputReceived,
		agentsdk.ConversationLifecycleModelCompleted,
		agentsdk.ConversationLifecycleModelRetry,
		agentsdk.ConversationLifecycleToolCompleted,
		agentsdk.ConversationLifecycleToolFailed,
		agentsdk.ConversationLifecycleRunFinished,
		agentsdk.ConversationLifecycleTaskFinished:
		return true
	default:
		return false
	}
}

func prepareConversationLifecycle(extensions []agentsdk.ConversationLifecycleExtension) ([]conversationLifecycleBinding, *agentsdk.ConversationLifecycleManifest, error) {
	if len(extensions) == 0 {
		return nil, nil, nil
	}
	if len(extensions) > 64 {
		return nil, nil, fmt.Errorf("too many conversation lifecycle extensions")
	}
	bindings := make([]conversationLifecycleBinding, 0, len(extensions))
	keys := map[string]bool{}
	for _, extension := range append([]agentsdk.ConversationLifecycleExtension(nil), extensions...) {
		definition, err := conversationLifecycleDefinition(extension)
		if err != nil {
			return nil, nil, err
		}
		if !conversationKey(definition.Key) || !conversationText(definition.Version, 128, true) || strings.TrimSpace(definition.Version) != definition.Version || !conversationText(definition.ConfigurationVersion, 128, true) || strings.TrimSpace(definition.ConfigurationVersion) != definition.ConfigurationVersion || definition.Order < 0 || definition.Order > 1_000_000 || keys[definition.Key] {
			return nil, nil, fmt.Errorf("invalid conversation lifecycle extension definition")
		}
		if definition.Kind != agentsdk.ConversationLifecycleKindPolicy && definition.Kind != agentsdk.ConversationLifecycleKindObserver {
			return nil, nil, fmt.Errorf("invalid conversation lifecycle extension kind")
		}
		if definition.FailureMode != agentsdk.ConversationLifecycleFailureFailClosed && definition.FailureMode != agentsdk.ConversationLifecycleFailureContinue {
			return nil, nil, fmt.Errorf("invalid conversation lifecycle failure mode")
		}
		if definition.Kind == agentsdk.ConversationLifecycleKindObserver && definition.FailureMode != agentsdk.ConversationLifecycleFailureContinue {
			return nil, nil, fmt.Errorf("conversation lifecycle observers must continue on failure")
		}
		if len(definition.Stages) == 0 || len(definition.Stages) > 16 {
			return nil, nil, fmt.Errorf("conversation lifecycle stages are required")
		}
		stages := make(map[agentsdk.ConversationLifecycleStage]bool, len(definition.Stages))
		canonical := append([]agentsdk.ConversationLifecycleStage(nil), definition.Stages...)
		for _, stage := range canonical {
			if !validConversationLifecycleStage(stage) || stages[stage] {
				return nil, nil, fmt.Errorf("invalid conversation lifecycle stage")
			}
			if definition.FailureMode == agentsdk.ConversationLifecycleFailureFailClosed && conversationLifecyclePostEffectStage(stage) {
				return nil, nil, fmt.Errorf("post-effect conversation lifecycle stages must continue on failure")
			}
			stages[stage] = true
		}
		sort.Slice(canonical, func(i, j int) bool { return canonical[i] < canonical[j] })
		keys[definition.Key] = true
		bindings = append(bindings, conversationLifecycleBinding{extension: extension, stages: stages, snapshot: agentsdk.ConversationLifecycleExtensionSnapshot{
			Key: definition.Key, Version: definition.Version, ConfigurationVersion: definition.ConfigurationVersion,
			Order: definition.Order, Kind: definition.Kind, FailureMode: definition.FailureMode, Stages: canonical,
		}})
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].snapshot.Order != bindings[j].snapshot.Order {
			return bindings[i].snapshot.Order < bindings[j].snapshot.Order
		}
		return bindings[i].snapshot.Key < bindings[j].snapshot.Key
	})
	manifest := &agentsdk.ConversationLifecycleManifest{ContractVersion: agentsdk.ConversationLifecycleContractVersion, Extensions: make([]agentsdk.ConversationLifecycleExtensionSnapshot, len(bindings))}
	for i := range bindings {
		manifest.Extensions[i] = bindings[i].snapshot
	}
	manifest.Digest = conversationDigest(struct {
		ContractVersion string
		Extensions      []agentsdk.ConversationLifecycleExtensionSnapshot
	}{manifest.ContractVersion, manifest.Extensions})
	return bindings, manifest, nil
}

func cloneConversationLifecycleManifest(in *agentsdk.ConversationLifecycleManifest) *agentsdk.ConversationLifecycleManifest {
	if in == nil {
		return nil
	}
	raw, _ := json.Marshal(in)
	var out agentsdk.ConversationLifecycleManifest
	_ = json.Unmarshal(raw, &out)
	return &out
}

func cloneConversationLifecycleEvent(in agentsdk.ConversationLifecycleEvent) (agentsdk.ConversationLifecycleEvent, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return agentsdk.ConversationLifecycleEvent{}, fmt.Errorf("conversation lifecycle event is not serializable")
	}
	var out agentsdk.ConversationLifecycleEvent
	if err = json.Unmarshal(raw, &out); err != nil {
		return agentsdk.ConversationLifecycleEvent{}, fmt.Errorf("conversation lifecycle event is not serializable")
	}
	return out, nil
}

func emptyConversationLifecycleDecision(in agentsdk.ConversationLifecycleDecision) bool {
	return len(in.PlanningInstructions) == 0 && in.ContextLimitBytes == 0 && in.Retry == nil
}

func validateConversationLifecycleDecision(stage agentsdk.ConversationLifecycleStage, in agentsdk.ConversationLifecycleDecision) error {
	if len(in.PlanningInstructions) > 0 && stage != agentsdk.ConversationLifecycleContextAssembling || in.ContextLimitBytes != 0 && stage != agentsdk.ConversationLifecycleContextCompacting || in.Retry != nil && stage != agentsdk.ConversationLifecycleModelFailed {
		return fmt.Errorf("conversation lifecycle decision is not valid for stage")
	}
	if len(in.PlanningInstructions) > 8 {
		return fmt.Errorf("too many conversation lifecycle planning instructions")
	}
	total := 0
	for _, instruction := range in.PlanningInstructions {
		if !conversationText(instruction, 2048, true) {
			return fmt.Errorf("invalid conversation lifecycle planning instruction")
		}
		total += len(instruction)
	}
	if total > 8192 || in.ContextLimitBytes != 0 && in.ContextLimitBytes < 4096 || in.ContextLimitBytes > 64*1024*1024 {
		return fmt.Errorf("invalid conversation lifecycle context decision")
	}
	if retry := in.Retry; retry != nil {
		if retry.MaxAttempts < 0 || retry.MaxAttempts > 8 || retry.DelayMilliseconds < 0 || retry.DelayMilliseconds > (5*time.Minute).Milliseconds() {
			return fmt.Errorf("invalid conversation lifecycle retry decision")
		}
	}
	return nil
}

func mergeConversationLifecycleDecision(total *agentsdk.ConversationLifecycleDecision, next agentsdk.ConversationLifecycleDecision) {
	total.PlanningInstructions = append(total.PlanningInstructions, next.PlanningInstructions...)
	if next.ContextLimitBytes > 0 && (total.ContextLimitBytes == 0 || next.ContextLimitBytes < total.ContextLimitBytes) {
		total.ContextLimitBytes = next.ContextLimitBytes
	}
	if next.Retry != nil {
		if total.Retry == nil {
			total.Retry = &agentsdk.ConversationLifecycleRetryDecision{Retry: true}
		}
		if !next.Retry.Retry {
			total.Retry.Retry = false
		}
		if next.Retry.MaxAttempts > 0 && (total.Retry.MaxAttempts == 0 || next.Retry.MaxAttempts < total.Retry.MaxAttempts) {
			total.Retry.MaxAttempts = next.Retry.MaxAttempts
		}
		if next.Retry.DelayMilliseconds > total.Retry.DelayMilliseconds {
			total.Retry.DelayMilliseconds = next.Retry.DelayMilliseconds
		}
	}
}

func invokeConversationLifecycle(extension agentsdk.ConversationLifecycleExtension, ctx context.Context, event agentsdk.ConversationLifecycleEvent) (decision agentsdk.ConversationLifecycleDecision, err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("conversation lifecycle extension panicked")
		}
	}()
	return extension.HandleConversationLifecycle(ctx, event)
}

func (s *ConversationService) dispatchConversationLifecycle(ctx context.Context, event agentsdk.ConversationLifecycleEvent) (agentsdk.ConversationLifecycleDecision, error) {
	var out agentsdk.ConversationLifecycleDecision
	if len(s.lifecycleExtensions) == 0 {
		return out, nil
	}
	event.ContractVersion = agentsdk.ConversationLifecycleContractVersion
	event.Manifest = cloneConversationLifecycleManifest(s.lifecycleManifest)
	if event.EventID == "" {
		event.EventID = "lifecycle_" + conversationDigest([]any{event.Stage, event.Authority.RuntimeID, event.Authority.WorkspaceID, event.Authority.UserID, event.ConversationID, event.RunID, event.TaskID, event.RunAttempt, event.Step, event.ModelAttempt, event.ToolCallID, event.Input})[:32]
	}
	event.OccurredAt = time.Now().UTC()
	for _, binding := range s.lifecycleExtensions {
		if !binding.stages[event.Stage] {
			continue
		}
		owned, err := cloneConversationLifecycleEvent(event)
		var decision agentsdk.ConversationLifecycleDecision
		if err == nil {
			decision, err = invokeConversationLifecycle(binding.extension, ctx, owned)
		}
		if err == nil && binding.snapshot.Kind == agentsdk.ConversationLifecycleKindObserver && !emptyConversationLifecycleDecision(decision) {
			err = fmt.Errorf("conversation lifecycle observer returned a decision")
		}
		if err == nil {
			err = validateConversationLifecycleDecision(event.Stage, decision)
		}
		if err != nil {
			if binding.snapshot.FailureMode == agentsdk.ConversationLifecycleFailureFailClosed && !conversationLifecyclePostEffectStage(event.Stage) {
				return out, conversationFailure("unavailable", "lifecycle_extension_failed")
			}
			slog.Warn("conversation lifecycle extension failed", "extension", binding.snapshot.Key, "stage", event.Stage)
			continue
		}
		mergeConversationLifecycleDecision(&out, decision)
	}
	return out, nil
}

func (s *ConversationService) selectConversationLifecycle(frozen *agentsdk.ConversationLifecycleManifest) error {
	if frozen == nil && s.lifecycleManifest == nil {
		return nil
	}
	if frozen == nil || s.lifecycleManifest == nil || frozen.ContractVersion != agentsdk.ConversationLifecycleContractVersion || frozen.Digest == "" || frozen.Digest != s.lifecycleManifest.Digest || conversationDigest(frozen.Extensions) != conversationDigest(s.lifecycleManifest.Extensions) {
		return conversationFailure("conflict", "lifecycle_changed")
	}
	return nil
}

func conversationLifecycleEventForClaim(claim persistence.ConversationClaim, stage agentsdk.ConversationLifecycleStage) agentsdk.ConversationLifecycleEvent {
	event := agentsdk.ConversationLifecycleEvent{
		Stage: stage, Authority: claim.Authority, ConversationID: claim.Run.ConversationID,
		RunID: claim.Run.ID, RunAttempt: claim.Run.Attempt, Step: -1,
	}
	if claim.Run.BackgroundTask != nil {
		event.TaskID = claim.Run.BackgroundTask.TaskID
	}
	return event
}

func conversationLifecycleModelPurpose(step int) string {
	if step == -1 {
		return "reply"
	}
	if step < -1 {
		return "summary"
	}
	return "execution"
}

func (s *ConversationService) conversationLifecycleModelKey(ctx context.Context) string {
	descriptor, err := s.currentConversationModelDescriptor(ctx)
	if err != nil {
		return ""
	}
	return descriptor.Key
}

func (s *ConversationService) dispatchConversationModelRequestLifecycle(ctx context.Context, claim persistence.ConversationClaim, attempt agentsdk.ConversationModelAttempt, request *agentsdk.ConversationModelRequest, stepRequest *agentsdk.ConversationStepRequest) error {
	event := conversationLifecycleEventForClaim(claim, agentsdk.ConversationLifecycleModelRequest)
	event.Step = attempt.Step
	event.ModelAttempt = attempt.Number
	event.Purpose = conversationLifecycleModelPurpose(attempt.Step)
	event.ModelRequest = request
	event.StepRequest = stepRequest
	event.ModelKey = s.conversationLifecycleModelKey(ctx)
	_, err := s.dispatchConversationLifecycle(ctx, event)
	return err
}

func (s *ConversationService) dispatchConversationModelCompletedLifecycle(ctx context.Context, claim persistence.ConversationClaim, attempt agentsdk.ConversationModelAttempt, model string, usage map[string]any) {
	event := conversationLifecycleEventForClaim(claim, agentsdk.ConversationLifecycleModelCompleted)
	event.Step = attempt.Step
	event.ModelAttempt = attempt.Number
	event.Purpose = conversationLifecycleModelPurpose(attempt.Step)
	event.ModelKey = s.conversationLifecycleModelKey(ctx)
	event.Outcome = &agentsdk.ConversationLifecycleOutcome{Status: "completed", Model: model, Usage: usage}
	_, _ = s.dispatchConversationLifecycle(ctx, event)
}

func (s *ConversationService) dispatchConversationInputReceivedLifecycle(ctx context.Context, run agentsdk.ConversationRun, authority agentsdk.ConversationAuthority, input agentsdk.ConversationLifecycleInput) {
	claim := persistence.ConversationClaim{Authority: authority, Run: run}
	event := conversationLifecycleEventForClaim(claim, agentsdk.ConversationLifecycleInputReceived)
	event.Input = &input
	_, _ = s.dispatchConversationLifecycle(ctx, event)
}

func (s *ConversationService) dispatchConversationToolBeforeLifecycle(ctx context.Context, claim persistence.ConversationClaim, step persistence.ConversationExecutionStep, call agentsdk.ConversationToolCall, definition agentsdk.ConversationToolDefinition) error {
	event := conversationLifecycleEventForClaim(claim, agentsdk.ConversationLifecycleToolBefore)
	event.Step = step.Number
	event.ToolCallID = call.ID
	event.ToolKey = call.Name
	event.ToolCall = &call
	event.ToolDefinition = &definition
	event.StepRequest = &step.Input
	_, err := s.dispatchConversationLifecycle(ctx, event)
	return err
}

func (s *ConversationService) dispatchConversationToolResultLifecycle(ctx context.Context, claim persistence.ConversationClaim, step persistence.ConversationExecutionStep, call agentsdk.ConversationToolCall, definition agentsdk.ConversationToolDefinition, result agentsdk.ConversationToolResult) {
	stage := agentsdk.ConversationLifecycleToolCompleted
	if result.Status != "completed" {
		stage = agentsdk.ConversationLifecycleToolFailed
	}
	event := conversationLifecycleEventForClaim(claim, stage)
	event.Step = step.Number
	event.ToolCallID = call.ID
	event.ToolKey = call.Name
	event.ToolCall = &call
	event.ToolDefinition = &definition
	event.ToolResult = &result
	event.Outcome = &agentsdk.ConversationLifecycleOutcome{Status: result.Status, ErrorCode: result.ErrorCode}
	_, _ = s.dispatchConversationLifecycle(ctx, event)
}

func (s *ConversationService) dispatchConversationTerminalLifecycle(ctx context.Context, run agentsdk.ConversationRun, authority agentsdk.ConversationAuthority) {
	if s.selectConversationLifecycle(run.Lifecycle) != nil {
		return
	}
	claim := persistence.ConversationClaim{Authority: authority, Run: run}
	event := conversationLifecycleEventForClaim(claim, agentsdk.ConversationLifecycleRunFinished)
	event.Outcome = &agentsdk.ConversationLifecycleOutcome{Status: run.Status, ErrorCode: run.ErrorCode, Model: run.Model, Usage: run.Usage}
	_, _ = s.dispatchConversationLifecycle(ctx, event)
	if event.TaskID == "" {
		return
	}
	event.Stage = agentsdk.ConversationLifecycleTaskFinished
	event.EventID = ""
	if tasks, ok := s.repo.(persistence.ConversationTaskReadRepository); ok {
		if task, err := tasks.ConversationTask(ctx, event.TaskID, authority); err == nil {
			event.Outcome = &agentsdk.ConversationLifecycleOutcome{Status: task.Status, ErrorCode: task.ErrorCode, Model: run.Model, Usage: run.Usage}
		}
	}
	_, _ = s.dispatchConversationLifecycle(ctx, event)
}

func (s *ConversationService) dispatchConversationQueuedTaskFinishedLifecycle(ctx context.Context, task agentsdk.ConversationTask, authority agentsdk.ConversationAuthority) {
	if s.selectConversationLifecycle(task.Lifecycle) != nil {
		return
	}
	event := agentsdk.ConversationLifecycleEvent{
		Stage: agentsdk.ConversationLifecycleTaskFinished, Authority: authority, ConversationID: conversationTaskExecutionConversation(task),
		TaskID: task.ID, Step: -1, Outcome: &agentsdk.ConversationLifecycleOutcome{Status: task.Status, ErrorCode: task.ErrorCode},
	}
	_, _ = s.dispatchConversationLifecycle(ctx, event)
}

func (s *ConversationService) applyConversationContextLifecycle(ctx context.Context, claim persistence.ConversationClaim, input agentsdk.ConversationModelRequest) (agentsdk.ConversationModelRequest, error) {
	event := conversationLifecycleEventForClaim(claim, agentsdk.ConversationLifecycleContextAssembling)
	event.Purpose = input.Purpose
	event.ModelKey = s.conversationLifecycleModelKey(ctx)
	event.ModelRequest = &input
	decision, err := s.dispatchConversationLifecycle(ctx, event)
	if err != nil {
		return input, err
	}
	if len(decision.PlanningInstructions) > 0 {
		guidance := make([]string, 0, len(decision.PlanningInstructions))
		for _, item := range decision.PlanningInstructions {
			guidance = append(guidance, strings.TrimSpace(item))
		}
		input.Messages = append([]agentsdk.ConversationModelMessage(nil), input.Messages...)
		if len(input.Messages) == 0 || input.Messages[0].Role != "system" {
			return input, conversationFailure("unavailable", "lifecycle_extension_failed")
		}
		// Keep every context-source MessageIndex stable. Planning can extend the
		// trusted root instruction but cannot insert or reorder source messages.
		input.Messages[0].Content += "\n\nTrusted lifecycle planning guidance (does not grant tool access or authorize effects):\n" + conversationJSONText(guidance)
		input.IdempotencyKey += ":lifecycle:" + conversationDigest(guidance)[:16]
		finalizeConversationModelContext(&input, s.conversationContextLimit(ctx))
		if conversationContextSize(input.Messages) > s.conversationContextLimit(ctx) {
			return input, conversationFailure("rate_limited", "execution_context_exceeded")
		}
	}
	event = conversationLifecycleEventForClaim(claim, agentsdk.ConversationLifecycleContextAssembled)
	event.Purpose = input.Purpose
	event.ModelKey = s.conversationLifecycleModelKey(ctx)
	event.ModelRequest = &input
	if _, err = s.dispatchConversationLifecycle(ctx, event); err != nil {
		return input, err
	}
	return input, nil
}

func (s *ConversationService) applyConversationContextLimitLifecycle(ctx context.Context, claim persistence.ConversationClaim) (context.Context, error) {
	event := conversationLifecycleEventForClaim(claim, agentsdk.ConversationLifecycleContextCompacting)
	event.ModelKey = s.conversationLifecycleModelKey(ctx)
	decision, err := s.dispatchConversationLifecycle(ctx, event)
	if err != nil {
		return ctx, err
	}
	if decision.ContextLimitBytes == 0 {
		return ctx, nil
	}
	return context.WithValue(ctx, conversationLifecycleContextLimitKey{}, min(s.conversationContextLimit(ctx), decision.ContextLimitBytes)), nil
}
