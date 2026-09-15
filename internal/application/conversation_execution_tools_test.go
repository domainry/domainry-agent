package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type executionToolSourceGraph struct {
	publisherRoleSourceGraph
	persistence.ConversationExecutionSharingRepository
	reader       sdk.ConversationAuthority
	publications []persistence.ConversationSourceRelease
	results      map[string]persistence.ConversationToolExecution
}

func (r *executionToolSourceGraph) ConversationDelegation(_ context.Context, id string, _ sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	return sdk.ConversationDelegation{ID: id, OwnerUserID: r.reader.UserID}, nil
}

func (r *executionToolSourceGraph) ConversationDelegationExecutions(context.Context, string, sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	return r.publications, nil
}

func (r *executionToolSourceGraph) ConversationSourceReleases(context.Context, sdk.ConversationRunReference, sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	return r.publications, nil
}

func (r *executionToolSourceGraph) Run(_ context.Context, cid, id string, a sdk.ConversationAuthority) (sdk.ConversationRun, error) {
	snapshot := r.snapshots[id]
	if snapshot.Authority != a || snapshot.Run.ConversationID != cid {
		return sdk.ConversationRun{}, conversationFailure("not_found", "run_not_found")
	}
	return snapshot.Run, nil
}

func (r *executionToolSourceGraph) ConversationResult(_ context.Context, ref sdk.ConversationResultReference, a sdk.ConversationAuthority) (persistence.ConversationToolExecution, error) {
	record := r.results[ref.RunID]
	if r.snapshots[ref.RunID].Authority != a || record.Step != ref.Step || record.Call.ID != ref.CallID || conversationDigest(record.Result) != ref.SHA256 {
		return persistence.ConversationToolExecution{}, invalidPersonalReceipt()
	}
	return record, nil
}

func executionSourceGraphFixture() (*ConversationService, *executionToolSourceGraph, sdk.ConversationAuthority, sdk.ConversationAuthority) {
	reader := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader", RoleKey: "reader-role"}
	producer := reader
	producer.UserID, producer.RoleKey = "producer", "producer-role"
	repo := &executionToolSourceGraph{reader: reader, results: map[string]persistence.ConversationToolExecution{}}
	repo.snapshots, repo.reads = map[string]persistence.ConversationSourceSnapshot{}, map[string][]sdk.ConversationAuthority{}
	service := &ConversationService{runtimeID: reader.RuntimeID, repo: repo, options: ConversationOptions{CollaborationAuthorizer: &executionBindingTestPolicy{}, ContextBytes: 65536}}
	return service, repo, reader, producer
}

func TestSharedExecutionResultWrapperRetainsOriginalPageAcrossContextBudgets(t *testing.T) {
	s, repo, reader, producer := executionSourceGraphFixture()
	root := sdk.ConversationRunReference{ConversationID: "source", RunID: "original"}
	repo.publications = []persistence.ConversationSourceRelease{{DelegationID: "delegation", Purpose: "execution", Reference: root, Producer: producer, Publisher: &producer}}
	definition, _ := collaborationTool("delegation_executions")
	content, _ := json.Marshal(sdk.ConversationDelegationExecutionIndex{DelegationID: "delegation", Publications: []sdk.ConversationExecutionPublication{}})
	original := persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "index", Name: definition.Key, Arguments: `{"id":"delegation"}`}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: "delegation", Content: content}}
	repo.results[root.RunID] = original
	repo.snapshots[root.RunID] = persistence.ConversationSourceSnapshot{Authority: producer, Run: sdk.ConversationRun{ID: root.RunID, ConversationID: root.ConversationID, Status: "completed", BackgroundTask: &sdk.ConversationTaskExecution{DelegationID: "delegation"}}, Calls: []persistence.ConversationToolExecution{original}}
	input := sdk.ConversationResultRead{Reference: sdk.ConversationResultReference{ConversationID: root.ConversationID, RunID: root.RunID, CallID: original.Call.ID, SHA256: conversationDigest(original.Result)}, MaxBytes: 8192}
	page, err := s.ReadConversationDelegationExecutionResult(t.Context(), "delegation", input, reader)
	if err != nil || !page.Complete {
		t.Fatal("original page was not read", err)
	}
	args := sdk.ConversationDelegationExecutionResultRead{ID: "delegation", Read: input}
	data, _ := json.Marshal(sdk.ConversationDelegationExecutionResult{DelegationID: args.ID, Result: page})
	definition, _ = collaborationTool("delegation_execution_result_read")
	wrapper := persistence.ConversationToolExecution{Step: 1, State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "read-page", Name: definition.Key, Arguments: conversationJSONText(args)}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: args.ID, Content: data}}
	s.options.ContextBytes = 512
	if _, err := s.conversationResultSlice(input, *original.Result); err == nil {
		t.Fatal("smaller context did not actually change fresh pagination")
	}
	owner := sdk.ConversationRunReference{ConversationID: "consumer", RunID: "reading-run"}
	if _, err := s.sourceAudit(reader).record(t.Context(), owner, wrapper); err != nil {
		t.Fatal("new context budget discarded the original authorized page", err)
	}
	page.JSONText = "invented private page"
	wrapper.Result.Content, _ = json.Marshal(sdk.ConversationDelegationExecutionResult{DelegationID: args.ID, Result: page})
	if _, err := s.sourceAudit(reader).record(t.Context(), owner, wrapper); err == nil {
		t.Fatal("wrapper accepted invented original bytes")
	}
}

func TestSavedSharedExecutionWrapperRestoresOnlyItsExactEarlierFlattenedSource(t *testing.T) {
	s, repo, reader, _ := executionSourceGraphFixture()
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "execution_read"}
	definition := sdk.ConversationToolDefinition{Key: "specialist_write", Version: "1", ActionKey: "specialist.write", Effect: "write", TimeoutMillis: 1000}
	host := &deliveryReadTestHost{}
	s.options.ToolHost, s.options.ToolDefinitions = host, []sdk.ConversationToolDefinition{definition}
	root := sdk.ConversationRunReference{ConversationID: "source", RunID: "original"}
	repo.publications = []persistence.ConversationSourceRelease{{DelegationID: "delegation", Purpose: "execution", Reference: root, Producer: reader, Publisher: &reader}}
	original := persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "write", Name: definition.Key, Arguments: `{}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"original":9007199254740993}`)}}
	originalRun := sdk.ConversationRun{ID: root.RunID, ConversationID: root.ConversationID, Status: "completed", Agent: &sdk.ConversationAgentSnapshot{Profile: sdk.AgentSchema{Tools: []string{definition.Key}}}, BackgroundTask: &sdk.ConversationTaskExecution{DelegationID: "delegation"}}
	repo.snapshots[root.RunID] = persistence.ConversationSourceSnapshot{Authority: reader, Run: originalRun, Calls: []persistence.ConversationToolExecution{original}}
	if _, err := s.sourceAudit(reader).run(t.Context(), root); err == nil {
		t.Fatal("raw execution unexpectedly acquired independent source read permission")
	}
	read := sdk.ConversationDelegationExecutionRead{ID: "delegation", Reference: root}
	saved, err := s.ReadConversationDelegationExecution(t.Context(), read.ID, root, reader)
	if err != nil {
		t.Fatal("actual scoped source fixture unreadable", err)
	}
	definition, _ = collaborationTool("delegation_execution_read")
	owner := sdk.ConversationRunReference{ConversationID: "consumer", RunID: "reading-run"}
	wrapper := persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "read", Name: definition.Key, Arguments: conversationJSONText(read)}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: read.ID, Content: []byte(conversationJSONText(sdk.ConversationDelegationExecutionView{DelegationID: read.ID, Run: saved}))}}
	snapshot := persistence.ConversationSourceSnapshot{Authority: reader, Run: sdk.ConversationRun{ID: owner.RunID, ConversationID: owner.ConversationID}, Calls: []persistence.ConversationToolExecution{wrapper}}
	repo.snapshots[owner.RunID] = snapshot
	if roots, err := s.sourceAudit(reader).run(t.Context(), owner); err != nil || len(roots) != 1 || roots[0] != (sdk.ConversationRunReference{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: 2}) {
		t.Fatal("new provenance lost the typed wrapper", roots, err)
	}
	// Old inputs kept the naked underlying run. Restore its scope from the
	// exact successful earlier read without changing that immutable snapshot.
	snapshot.StepSources = []persistence.ConversationStepSources{{Step: 1, Sources: []sdk.ConversationRunReference{root}}}
	repo.snapshots[owner.RunID] = snapshot
	readSaved := func() error { _, err := s.sourceAudit(reader).run(t.Context(), owner); return err }
	if err := readSaved(); err != nil {
		t.Fatal("old flattened source forgot its verified execution read purpose", err)
	}
	for _, boundary := range []int{-1, 258} {
		invalid := root
		invalid.BeforeStep = boundary
		snapshot.StepSources[0].Sources = []sdk.ConversationRunReference{invalid}
		repo.snapshots[owner.RunID] = snapshot
		if err := readSaved(); err == nil || !strings.Contains(err.Error(), "source_reference_invalid") {
			t.Fatal("a shared read recovered an invalid source boundary", boundary, err)
		}
	}
	snapshot.StepSources[0].Sources = []sdk.ConversationRunReference{root}
	repo.snapshots[owner.RunID] = snapshot
	host.err = conversationFailure("forbidden", "current_source_denied")
	if err := readSaved(); !collaborationDenied(err) {
		t.Fatal("old wrapper bypassed current professional source denial", err)
	}
	host.err = nil
	repo.publications = nil
	if err := readSaved(); !collaborationDenied(err) {
		t.Fatal("old wrapper restored withdrawn execution publication", err)
	}
	repo.publications = []persistence.ConversationSourceRelease{{DelegationID: read.ID, Purpose: "execution", Reference: root, Producer: reader, Publisher: &reader}}
	// A read completed in this same/future step cannot explain a saved input.
	snapshot.Calls[0].Step = 1
	repo.snapshots[owner.RunID] = snapshot
	if err := readSaved(); err == nil {
		t.Fatal("same-step wrapper retrospectively authorized an earlier input")
	}
	snapshot.Calls[0].Step = 0
	other := root
	other.RunID = "unread-original"
	repo.snapshots[other.RunID] = repo.snapshots[root.RunID]
	snapshot.StepSources[0].Sources = []sdk.ConversationRunReference{other}
	repo.snapshots[owner.RunID] = snapshot
	if err := readSaved(); err == nil {
		t.Fatal("a shared read authorized another naked run")
	}
	// Result pages explain only the admitted prefix, never the whole run or
	// later calls, even when the caller can read another shared observation.
	repo.results[root.RunID] = original
	pageInput := sdk.ConversationResultRead{Reference: sdk.ConversationResultReference{ConversationID: root.ConversationID, RunID: root.RunID, CallID: original.Call.ID, SHA256: conversationDigest(original.Result)}, MaxBytes: 8192}
	page, err := s.ReadConversationDelegationExecutionResult(t.Context(), read.ID, pageInput, reader)
	if err != nil {
		t.Fatal("original result page fixture unreadable", err)
	}
	definition, _ = collaborationTool("delegation_execution_result_read")
	snapshot.Calls[0] = persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "page", Name: definition.Key, Arguments: conversationJSONText(sdk.ConversationDelegationExecutionResultRead{ID: read.ID, Read: pageInput})}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: read.ID, Content: []byte(conversationJSONText(sdk.ConversationDelegationExecutionResult{DelegationID: read.ID, Result: page}))}}
	root.BeforeStep = 2
	snapshot.StepSources[0].Sources = []sdk.ConversationRunReference{root}
	repo.snapshots[owner.RunID] = snapshot
	if err := readSaved(); err != nil {
		t.Fatal("old exact result prefix lost its page read purpose", err)
	}
	for _, boundary := range []int{0, 3} {
		root.BeforeStep = boundary
		snapshot.StepSources[0].Sources = []sdk.ConversationRunReference{root}
		repo.snapshots[owner.RunID] = snapshot
		if err := readSaved(); err == nil {
			t.Fatal("a result page explained an unadmitted source prefix", boundary)
		}
	}
}

func TestSavedDelegationTaskResultSeparatesExecutionReadFromWriteAndCommunication(t *testing.T) {
	s, repo, reader, _ := executionSourceGraphFixture()
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "execution_read"}
	definition := sdk.ConversationToolDefinition{Key: "specialist_write", Version: "1", ActionKey: "specialist.write", Effect: "write", TimeoutMillis: 1000}
	host := &deliveryReadTestHost{}
	s.options.ToolHost, s.options.ToolDefinitions = host, []sdk.ConversationToolDefinition{definition}
	root := sdk.ConversationRunReference{ConversationID: "source", RunID: "original"}
	original := persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "write", Name: definition.Key, Arguments: `{}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"original":9007199254740993}`)}}
	repo.snapshots[root.RunID] = persistence.ConversationSourceSnapshot{Authority: reader, Run: sdk.ConversationRun{ID: root.RunID, ConversationID: root.ConversationID, Agent: &sdk.ConversationAgentSnapshot{Profile: sdk.AgentSchema{Tools: []string{definition.Key}}}}, Calls: []persistence.ConversationToolExecution{original}}
	detail := sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "delegation", ConversationID: root.ConversationID}, Task: &sdk.ConversationTaskDetail{ConversationTaskSummary: sdk.ConversationTaskSummary{ExecutionRunID: root.RunID, Result: &sdk.ConversationTaskResult{Preview: "Original completed result"}}}}
	definition, _ = collaborationTool("delegation_get")
	owner := sdk.ConversationRunReference{ConversationID: "consumer", RunID: "reading-run"}
	wrapper := persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "get", Name: definition.Key, Arguments: `{"id":"delegation"}`}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: detail.ID}}
	snapshot := persistence.ConversationSourceSnapshot{Authority: reader, Run: sdk.ConversationRun{ID: owner.RunID, ConversationID: owner.ConversationID}, StepSources: []persistence.ConversationStepSources{{Step: 1, Sources: []sdk.ConversationRunReference{root}}}}
	read := func() error {
		wrapper.Result.Content = []byte(conversationJSONText(detail))
		snapshot.Calls = []persistence.ConversationToolExecution{wrapper}
		repo.snapshots[owner.RunID] = snapshot
		_, err := s.sourceAudit(reader).run(t.Context(), owner)
		return err
	}
	if err := read(); err != nil || host.reads == 0 || host.executionChecks != 0 {
		t.Fatal("saved task result required professional write or delivery permission", err)
	}
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "delivery_read"}
	if err := read(); !collaborationDenied(err) {
		t.Fatal("delivery permission exposed a raw saved task", err)
	}
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "execution_read"}
	detail.Messages = []sdk.ConversationAgentMessage{{ID: "private", Content: "Private correspondence"}}
	if err := read(); !collaborationDenied(err) {
		t.Fatal("task execution data granted communication access", err)
	}
	detail.Messages = nil
	old := repo.snapshots[root.RunID]
	old.Authority.RoleKey = "older-proof-role"
	repo.snapshots[root.RunID] = old
	if err := read(); !collaborationDenied(err) {
		t.Fatal("task ownership introduced an unpublished older execution role", err)
	}
}

func TestSharedExecutionReadWrappersCannotResetReferenceCycleGuards(t *testing.T) {
	s, repo, reader, producer := executionSourceGraphFixture()
	definition, _ := collaborationTool("delegation_execution_read")
	for _, id := range []string{"first", "second"} {
		other := "first"
		if id == "first" {
			other = "second"
		}
		root := sdk.ConversationRunReference{ConversationID: "source", RunID: id}
		repo.publications = append(repo.publications, persistence.ConversationSourceRelease{DelegationID: "delegation", Purpose: "execution", Reference: root, Producer: producer, Publisher: &producer})
		run := sdk.ConversationRun{ID: id, ConversationID: "source", Status: "running", BackgroundTask: &sdk.ConversationTaskExecution{DelegationID: "delegation"}}
		observed := run
		observed.ID = other
		data, _ := json.Marshal(sdk.ConversationDelegationExecutionView{DelegationID: "delegation", Run: observed})
		args := sdk.ConversationDelegationExecutionRead{ID: "delegation", Reference: sdk.ConversationRunReference{ConversationID: "source", RunID: other}}
		record := persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "read-" + other, Name: definition.Key, Arguments: conversationJSONText(args)}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: args.ID, Content: data}}
		repo.snapshots[id] = persistence.ConversationSourceSnapshot{Authority: producer, Run: run, Calls: []persistence.ConversationToolExecution{record}}
	}
	second := repo.snapshots["second"]
	withoutCycle := second
	withoutCycle.Calls = nil
	repo.snapshots["second"] = withoutCycle
	ref := sdk.ConversationRunReference{ConversationID: "source", RunID: "first"}
	if _, err := s.ReadConversationDelegationExecution(t.Context(), "delegation", ref, reader); err != nil {
		t.Fatal("acyclic shared wrapper fixture was not readable", err)
	}
	repo.snapshots["second"] = second
	if _, err := s.ReadConversationDelegationExecution(t.Context(), "delegation", ref, reader); err == nil || !strings.Contains(err.Error(), "source_reference_invalid") {
		t.Fatal("two shared read wrappers bypassed reference cycle guards", err)
	}
}

func TestSharedLiveExecutionObservationRetainsOldCompletedCallsButRejectsInventedPayloads(t *testing.T) {
	clone := func(run sdk.ConversationRun) sdk.ConversationRun {
		var copy sdk.ConversationRun
		data, _ := json.Marshal(run)
		_ = json.Unmarshal(data, &copy)
		return copy
	}
	old := sdk.ConversationRun{ID: "original", ConversationID: "producer", Status: "running", Attempt: 1, DraftText: "Verified", Steps: []sdk.ConversationStepView{{Number: 0, Attempt: 1, Text: "Verified", Calls: []sdk.ConversationToolView{{ID: "clock", Name: "time_now", Status: "completed", Arguments: "{}", ResultPreview: "actual-clock-result", ResultReference: &sdk.ConversationResultReference{ConversationID: "producer", RunID: "original", CallID: "clock", SHA256: "original-sha"}}}}}}
	current := clone(old)
	current.Status, current.DraftText = "completed", "Verified final reply"
	current.Steps = append(current.Steps, sdk.ConversationStepView{Number: 1, Attempt: 1, Text: "Final"})
	if !verifiedExecutionObservation(old, current) {
		t.Fatal("ledger progress discarded an exact prior live observation")
	}
	for name, change := range map[string]func(*sdk.ConversationRun){
		"result":    func(run *sdk.ConversationRun) { run.Steps[0].Calls[0].ResultPreview = "invented-sensitive-result" },
		"arguments": func(run *sdk.ConversationRun) { run.Steps[0].Calls[0].Arguments = "invented-sensitive-arguments" },
		"reply":     func(run *sdk.ConversationRun) { run.DraftText = "invented-sensitive-reply" },
		"run":       func(run *sdk.ConversationRun) { run.ID = "other-run" },
		"controls":  func(run *sdk.ConversationRun) { run.WriteScope = &sdk.ConversationWriteScope{} },
	} {
		t.Run(name, func(t *testing.T) {
			forged := clone(old)
			change(&forged)
			if verifiedExecutionObservation(forged, current) {
				t.Fatal("invented shared execution contents were accepted")
			}
		})
	}
	pending := clone(old)
	pending.DraftText, pending.Steps[0].Text = "", ""
	pending.Steps[0].Calls = []sdk.ConversationToolView{{ID: "clock", Name: "time_now", Status: "running", AccessError: "execution_result_not_readable"}}
	if !verifiedExecutionObservation(pending, current) {
		t.Fatal("safe earlier pending status was discarded after completion")
	}
	pending.Steps[0].Calls[0].Completion = "unproved-private-data"
	if verifiedExecutionObservation(pending, current) {
		t.Fatal("an unproved pending field escaped the public whitelist")
	}
}
