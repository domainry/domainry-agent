package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type contextSourceTestRepository struct {
	persistence.ConversationRepository
	input    string
	snapshot *persistence.ConversationSourceSnapshot
}

func (r *contextSourceTestRepository) History(_ context.Context, id string, after, through int64, limit int, _ sdk.ConversationAuthority) ([]sdk.ConversationMessage, error) {
	if id != "conversation" || after != 6 || through != 7 || limit != 1 {
		return nil, errors.New("unexpected context source history scope")
	}
	return []sdk.ConversationMessage{{ConversationID: id, Seq: 7, Role: "user", Content: r.input}}, nil
}

func (r *contextSourceTestRepository) ConversationSourceSnapshot(_ context.Context, ref sdk.ConversationRunReference, authority sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	if r.snapshot == nil || ref.ConversationID != r.snapshot.Run.ConversationID || ref.RunID != r.snapshot.Run.ID || ref.BeforeStep != 0 || authority != r.snapshot.Authority {
		return persistence.ConversationSourceSnapshot{}, errors.New("unexpected registered context source snapshot scope")
	}
	return *r.snapshot, nil
}

type contextSourceTestValue struct {
	definition sdk.ConversationContextSourceDefinition
	content    sdk.ConversationContextSourceContent
	reads      int
	authorizes int
	revoked    error
}

func (s *contextSourceTestValue) ConversationContextSourceDefinition() sdk.ConversationContextSourceDefinition {
	return s.definition
}

func (s *contextSourceTestValue) ReadConversationContext(_ context.Context, request sdk.ConversationContextSourceRequest) (sdk.ConversationContextSourceContent, error) {
	if request.CurrentInput != "use the corrected quarter" || request.ConversationID != "conversation" || request.RunID != "run" {
		return sdk.ConversationContextSourceContent{}, errors.New("source received the wrong task input")
	}
	s.reads++
	return s.content, nil
}

func (s *contextSourceTestValue) AuthorizeConversationContext(_ context.Context, request sdk.ConversationContextSourceRequest, reference sdk.ConversationContextSourceReference) error {
	if request.CurrentInput != "use the corrected quarter" || reference.Key != s.definition.Key || reference.DefinitionHash != conversationDigest(s.definition) {
		return errors.New("source authorization was not bound to the frozen value")
	}
	s.authorizes++
	return s.revoked
}

func contextSourceDefinition(key, kind, refresh, trust string, order int, stable bool) sdk.ConversationContextSourceDefinition {
	return sdk.ConversationContextSourceDefinition{Key: key, Kind: kind, Scope: sdk.ConversationContextScopeTask, Refresh: refresh, Trust: trust, Order: order, MaxBytes: 1024, StablePrefix: stable}
}

func TestConversationContextSourceRegistryRejectsAmbiguousTrustAndFreezesOrder(t *testing.T) {
	stable := &contextSourceTestValue{definition: contextSourceDefinition("project", sdk.ConversationContextKindProjectInstructions, sdk.ConversationContextRefreshRun, sdk.ConversationContextTrustInstruction, 100, true)}
	dynamic := &contextSourceTestValue{definition: contextSourceDefinition("record", sdk.ConversationContextKindBusinessRecord, sdk.ConversationContextRefreshStep, sdk.ConversationContextTrustData, -100, false)}
	options := ConversationOptions{ContextBytes: 8192, ContextSources: []sdk.ConversationContextSource{dynamic, stable}}
	if err := prepareConversationContextSources(&options); err != nil || options.ContextSources[0].ConversationContextSourceDefinition() != stable.definition || options.ContextSources[1].ConversationContextSourceDefinition() != dynamic.definition {
		t.Fatal("stable source prefix and deterministic order were not frozen", err)
	}

	cases := []ConversationOptions{
		{ContextBytes: 8192, ContextSources: []sdk.ConversationContextSource{&contextSourceTestValue{definition: contextSourceDefinition("record", sdk.ConversationContextKindBusinessRecord, sdk.ConversationContextRefreshRun, sdk.ConversationContextTrustInstruction, 0, false)}}},
		{ContextBytes: 8192, ContextSources: []sdk.ConversationContextSource{&contextSourceTestValue{definition: contextSourceDefinition("file", sdk.ConversationContextKindFileReference, sdk.ConversationContextRefreshRun, sdk.ConversationContextTrustData, 0, false)}}},
		{ContextBytes: 8192, ContextSources: []sdk.ConversationContextSource{&contextSourceTestValue{definition: contextSourceDefinition("project", sdk.ConversationContextKindProjectInstructions, sdk.ConversationContextRefreshStep, sdk.ConversationContextTrustInstruction, 0, true)}}},
		{ContextBytes: 8192, ContextSources: []sdk.ConversationContextSource{stable, stable}},
		{ContextBytes: 4096, ContextSources: []sdk.ConversationContextSource{stable, dynamic, &contextSourceTestValue{definition: contextSourceDefinition("large", sdk.ConversationContextKindHostData, sdk.ConversationContextRefreshRun, sdk.ConversationContextTrustData, 0, false)}}},
	}
	for index := range cases {
		if err := prepareConversationContextSources(&cases[index]); err == nil {
			t.Fatalf("invalid source registry case %d was accepted", index)
		}
	}
}

func TestConversationContextWindowMeasuresRunsWithoutRegisteredSources(t *testing.T) {
	input := sdk.ConversationModelRequest{Messages: []sdk.ConversationModelMessage{{Role: "system", Content: "stable instructions"}, {Role: "user", Content: "current request"}}}
	finalizeConversationModelContext(&input, 4096)
	want := conversationContextSize(input.Messages)
	if input.Context != nil || input.ContextWindow == nil || input.ContextWindow.InputBytes != want || input.ContextWindow.LimitBytes != 4096 || input.ContextWindow.PressurePermille != want*1000/4096 || input.ContextWindow.ProviderSerialized {
		t.Fatal("ordinary unregistered context pressure was not measured", input.ContextWindow)
	}
}

func TestConversationContextSourcesRefreshTrackChangesAndReauthorizeFrozenInput(t *testing.T) {
	now := time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC)
	stable := &contextSourceTestValue{
		definition: contextSourceDefinition("project", sdk.ConversationContextKindProjectInstructions, sdk.ConversationContextRefreshRun, sdk.ConversationContextTrustInstruction, 0, true),
		content:    sdk.ConversationContextSourceContent{Version: "project-v1", Content: "Use the repository coding rules.", UpdatedAt: now},
	}
	dynamic := &contextSourceTestValue{
		definition: contextSourceDefinition("record", sdk.ConversationContextKindBusinessRecord, sdk.ConversationContextRefreshStep, sdk.ConversationContextTrustData, 10, false),
		content:    sdk.ConversationContextSourceContent{Version: "record-v1", Content: "quarter=Q3", UpdatedAt: now},
	}
	repo := &contextSourceTestRepository{input: "use the corrected quarter"}
	s := &ConversationService{repo: repo, options: ConversationOptions{ContextBytes: 8192, ContextSources: []sdk.ConversationContextSource{stable, dynamic}}}
	if err := prepareConversationContextSources(&s.options); err != nil {
		t.Fatal(err)
	}
	ordinary := persistence.ConversationClaim{Authority: sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}, Run: sdk.ConversationRun{ID: "run", ConversationID: "conversation", UserSeq: 7}}
	if messages, manifest, err := s.initialConversationContextSources(t.Context(), ordinary); err != nil || len(messages) != 0 || manifest != nil || stable.reads != 0 || dynamic.reads != 0 {
		t.Fatal("task-scoped sources leaked into an ordinary conversation", manifest, err)
	}
	claim := persistence.ConversationClaim{Authority: sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}, Run: sdk.ConversationRun{ID: "run", ConversationID: "conversation", UserSeq: 7, BackgroundTask: &sdk.ConversationTaskExecution{TaskID: "task"}}}
	messages, manifest, err := s.initialConversationContextSources(t.Context(), claim)
	if err != nil || len(messages) != 2 || manifest == nil {
		t.Fatal("initial registered context was not assembled", manifest, err)
	}
	if !strings.Contains(messages[0].Content, "Deployment-authorized project instructions") || !strings.Contains(messages[1].Content, "untrusted data") {
		t.Fatal("source trust was not explicitly framed", messages)
	}
	for index := range manifest.Sources {
		manifest.Sources[index].MessageIndex = index
	}
	input := sdk.ConversationStepRequest{
		Context:       manifest,
		Messages:      append(messages, sdk.ConversationStepMessage{Role: "user", Content: "keep this exact user correction"}),
		ModelIdentity: sdk.ConversationModelIdentity{Provider: "test", Protocol: "test", Model: "test", Fingerprint: "model-v1"},
	}
	updateConversationContextHashes(&input)
	stableHash, dynamicHash := input.Context.StablePrefixHash, input.Context.DynamicHash
	if len(stableHash) != 64 || len(dynamicHash) != 64 {
		t.Fatal("context partitions were not fingerprinted")
	}

	stepZero, err := s.refreshConversationContextSources(t.Context(), claim, input, 0)
	if err != nil || stable.reads != 1 || dynamic.reads != 1 {
		t.Fatal("step zero reread a run-frozen source", stable.reads, dynamic.reads, err)
	}
	dynamic.content = sdk.ConversationContextSourceContent{Version: "record-v1", Content: "quarter=Q2", UpdatedAt: now.Add(time.Minute)}
	if _, err = s.refreshConversationContextSources(t.Context(), claim, stepZero, 1); err == nil || !strings.Contains(err.Error(), "context_source_changed") {
		t.Fatal("one source version was allowed to identify different content", err)
	}
	dynamic.content = sdk.ConversationContextSourceContent{Version: "record-v2", Content: "quarter=Q2", UpdatedAt: now.Add(time.Minute)}
	stepOne, err := s.refreshConversationContextSources(t.Context(), claim, stepZero, 1)
	if err != nil || stable.reads != 1 || dynamic.reads != 3 || len(stepOne.Context.Changes) != 1 {
		t.Fatal("step source update was not recorded", stable.reads, dynamic.reads, stepOne.Context, err)
	}
	updateConversationContextHashes(&stepOne)
	if stepOne.Context.StablePrefixHash != stableHash || stepOne.Context.DynamicHash == dynamicHash || stepOne.Messages[len(stepOne.Messages)-1].Content != "keep this exact user correction" {
		t.Fatal("dynamic refresh changed the stable prefix or user correction")
	}
	if err = s.reauthorizeConversationContextSources(t.Context(), claim, stepOne.Context, stepOne.Messages, "step", 1); err != nil {
		t.Fatal("current frozen context was not reauthorized", err)
	}
	repo.snapshot = &persistence.ConversationSourceSnapshot{
		Authority: claim.Authority,
		Run:       claim.Run,
		Input:     &sdk.ConversationModelRequest{Sources: &sdk.ConversationSources{Version: 1}},
		StepContexts: []persistence.ConversationStepContext{{
			Step: 1, Context: stepOne.Context, Messages: stepOne.Messages,
		}},
	}
	ref := sdk.ConversationRunReference{ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID}
	if _, err = s.sourceAudit(claim.Authority, claim.Run.ConversationID).run(t.Context(), ref); err != nil {
		t.Fatal("historical reuse did not reauthorize its registered context", err)
	}

	tampered := stepOne
	tampered.Messages = append([]sdk.ConversationStepMessage(nil), stepOne.Messages...)
	tampered.Messages[stepOne.Context.Sources[1].MessageIndex].Content += " forged"
	if err = s.reauthorizeConversationContextSources(t.Context(), claim, tampered.Context, tampered.Messages, "step", 1); err == nil || !strings.Contains(err.Error(), "context_source_changed") {
		t.Fatal("tampered frozen context was accepted", err)
	}
	revoked := errors.New("record access revoked")
	dynamic.revoked = revoked
	if err = s.reauthorizeConversationContextSources(t.Context(), claim, stepOne.Context, stepOne.Messages, "step", 1); !errors.Is(err, revoked) {
		t.Fatal("current source revocation did not block model reuse", err)
	}
	if _, err = s.sourceAudit(claim.Authority, claim.Run.ConversationID).run(t.Context(), ref); !errors.Is(err, revoked) {
		t.Fatal("source revocation did not block historical reply reuse", err)
	}
}
