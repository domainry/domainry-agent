package application

import (
	"context"
	"errors"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type applicationLifecycleExtension struct {
	definition agentsdk.ConversationLifecycleExtensionDefinition
	decision   agentsdk.ConversationLifecycleDecision
	err        error
	handle     func(agentsdk.ConversationLifecycleEvent)
}

func (e *applicationLifecycleExtension) ConversationLifecycleDefinition() agentsdk.ConversationLifecycleExtensionDefinition {
	return e.definition
}

func (e *applicationLifecycleExtension) HandleConversationLifecycle(_ context.Context, event agentsdk.ConversationLifecycleEvent) (agentsdk.ConversationLifecycleDecision, error) {
	if e.handle != nil {
		e.handle(event)
	}
	return e.decision, e.err
}

func applicationLifecycleDefinition(key string, order int, kind, failure string, stages ...agentsdk.ConversationLifecycleStage) agentsdk.ConversationLifecycleExtensionDefinition {
	return agentsdk.ConversationLifecycleExtensionDefinition{Key: key, Version: "1", ConfigurationVersion: "config-1", Order: order, Kind: kind, FailureMode: failure, Stages: stages}
}

func TestConversationLifecycleEmitsQueuedTaskTerminalEvent(t *testing.T) {
	var received agentsdk.ConversationLifecycleEvent
	observer := &applicationLifecycleExtension{
		definition: applicationLifecycleDefinition("task-observer", 1, agentsdk.ConversationLifecycleKindObserver, agentsdk.ConversationLifecycleFailureContinue, agentsdk.ConversationLifecycleTaskFinished),
		handle:     func(event agentsdk.ConversationLifecycleEvent) { received = event },
	}
	bindings, manifest, err := prepareConversationLifecycle([]agentsdk.ConversationLifecycleExtension{observer})
	if err != nil {
		t.Fatal(err)
	}
	service := &ConversationService{lifecycleExtensions: bindings, lifecycleManifest: manifest}
	task := agentsdk.ConversationTask{ID: "task-one", Status: agentsdk.ConversationTaskStatusCancelled, SourceConversationID: "conversation-one", Lifecycle: cloneConversationLifecycleManifest(manifest)}
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	service.dispatchConversationQueuedTaskFinishedLifecycle(t.Context(), task, authority)
	if received.Stage != agentsdk.ConversationLifecycleTaskFinished || received.TaskID != task.ID || received.ConversationID != task.SourceConversationID || received.Outcome == nil || received.Outcome.Status != agentsdk.ConversationTaskStatusCancelled || received.EventID == "" {
		t.Fatalf("task terminal event missing: %+v", received)
	}
}

func TestPrepareConversationLifecycleFreezesOrderAndConfiguration(t *testing.T) {
	late := &applicationLifecycleExtension{definition: applicationLifecycleDefinition("late", 20, agentsdk.ConversationLifecycleKindObserver, agentsdk.ConversationLifecycleFailureContinue, agentsdk.ConversationLifecycleModelRequest)}
	early := &applicationLifecycleExtension{definition: applicationLifecycleDefinition("early", 10, agentsdk.ConversationLifecycleKindPolicy, agentsdk.ConversationLifecycleFailureFailClosed, agentsdk.ConversationLifecycleContextAssembling)}
	bindings, manifest, err := prepareConversationLifecycle([]agentsdk.ConversationLifecycleExtension{late, early})
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || manifest == nil || manifest.ContractVersion != agentsdk.ConversationLifecycleContractVersion || manifest.Digest == "" || manifest.Extensions[0].Key != "early" || manifest.Extensions[1].Key != "late" || manifest.Extensions[0].ConfigurationVersion != "config-1" {
		t.Fatalf("lifecycle manifest was not canonical: %+v", manifest)
	}
	service := &ConversationService{lifecycleExtensions: bindings, lifecycleManifest: manifest}
	if err = service.selectConversationLifecycle(cloneConversationLifecycleManifest(manifest)); err != nil {
		t.Fatal(err)
	}
	changed := cloneConversationLifecycleManifest(manifest)
	changed.Extensions[0].ConfigurationVersion = "config-2"
	if err = service.selectConversationLifecycle(changed); err == nil {
		t.Fatal("changed lifecycle configuration was accepted")
	}
}

func TestConversationLifecycleValidatesFailureAndDecisionBoundaries(t *testing.T) {
	invalidTerminal := &applicationLifecycleExtension{definition: applicationLifecycleDefinition("terminal", 1, agentsdk.ConversationLifecycleKindPolicy, agentsdk.ConversationLifecycleFailureFailClosed, agentsdk.ConversationLifecycleRunFinished)}
	if _, _, err := prepareConversationLifecycle([]agentsdk.ConversationLifecycleExtension{invalidTerminal}); err == nil {
		t.Fatal("fail-closed post-effect extension was accepted")
	}
	observer := &applicationLifecycleExtension{
		definition: applicationLifecycleDefinition("observer", 1, agentsdk.ConversationLifecycleKindObserver, agentsdk.ConversationLifecycleFailureContinue, agentsdk.ConversationLifecycleContextAssembling),
		decision:   agentsdk.ConversationLifecycleDecision{PlanningInstructions: []string{"must be ignored"}},
	}
	bindings, manifest, err := prepareConversationLifecycle([]agentsdk.ConversationLifecycleExtension{observer})
	if err != nil {
		t.Fatal(err)
	}
	service := &ConversationService{lifecycleExtensions: bindings, lifecycleManifest: manifest}
	decision, err := service.dispatchConversationLifecycle(t.Context(), agentsdk.ConversationLifecycleEvent{Stage: agentsdk.ConversationLifecycleContextAssembling})
	if err != nil || !emptyConversationLifecycleDecision(decision) {
		t.Fatalf("observer changed policy result: decision=%+v err=%v", decision, err)
	}
	failing := &applicationLifecycleExtension{
		definition: applicationLifecycleDefinition("policy", 1, agentsdk.ConversationLifecycleKindPolicy, agentsdk.ConversationLifecycleFailureFailClosed, agentsdk.ConversationLifecycleInputAdmitting),
		err:        errors.New("secret policy detail"),
	}
	bindings, manifest, err = prepareConversationLifecycle([]agentsdk.ConversationLifecycleExtension{failing})
	if err != nil {
		t.Fatal(err)
	}
	service = &ConversationService{lifecycleExtensions: bindings, lifecycleManifest: manifest}
	if _, err = service.dispatchConversationLifecycle(t.Context(), agentsdk.ConversationLifecycleEvent{Stage: agentsdk.ConversationLifecycleInputAdmitting}); err == nil || err.Error() != "agent.conversation.lifecycle_extension_failed" {
		t.Fatalf("private extension error leaked or did not fail closed: %v", err)
	}
}
