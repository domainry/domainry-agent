package application

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type delegationSourcesTestRepository struct {
	sameUserRoleSourceGraph
	persistence.ConversationCollaborationRepository
	persistence.ConversationDelegationExecutionRepository
	d            sdk.ConversationDelegation
	run          sdk.ConversationRun
	executor     sdk.ConversationAuthority
	original     persistence.ConversationToolExecution
	lastProducer sdk.ConversationAuthority
}

type historicalDelegationSourcesTestRepository struct {
	*delegationSourcesTestRepository
	persistence.ConversationDelegationTransferRepository
	assignments []sdk.ConversationDelegationAssignment
}

func (r *historicalDelegationSourcesTestRepository) ConversationDelegationAssignments(context.Context, string, sdk.ConversationAuthority) ([]sdk.ConversationDelegationAssignment, error) {
	return r.assignments, nil
}

func (r *delegationSourcesTestRepository) Run(_ context.Context, conversation, run string, a sdk.ConversationAuthority) (sdk.ConversationRun, error) {
	if conversation != r.run.ConversationID || run != r.run.ID || a.UserID != r.executor.UserID {
		return sdk.ConversationRun{}, conversationFailure("not_found", "run_not_found")
	}
	return r.run, nil
}

func (r *delegationSourcesTestRepository) ConversationDelegation(_ context.Context, id string, _ sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	if id != r.d.ID {
		return sdk.ConversationDelegation{}, conversationFailure("not_found", "delegation_not_found")
	}
	return r.d, nil
}

func (r *delegationSourcesTestRepository) ConversationDelegationAuthorities(context.Context, string, sdk.ConversationAuthority) (persistence.ConversationDelegationAuthorities, error) {
	return persistence.ConversationDelegationAuthorities{Issuer: r.executor, Executor: r.executor}, nil
}

func (r *delegationSourcesTestRepository) ConversationResult(_ context.Context, ref sdk.ConversationResultReference, a sdk.ConversationAuthority) (persistence.ConversationToolExecution, error) {
	r.lastProducer = a
	return r.original, nil
}

func TestDelegationSourceReadsAdmittedOriginalWithoutExecutionAndRechecksNestedPages(t *testing.T) {
	reader := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", RoleKey: "reader"}
	producer := reader
	producer.RoleKey = "old-professional"
	definition := sdk.ConversationToolDefinition{Key: "specialist_read", Version: "1", ActionKey: "specialist.execute", TimeoutMillis: 1000}
	original := persistence.ConversationToolExecution{State: "completed", Step: 0, Definition: definition, Call: sdk.ConversationToolCall{ID: "original", Name: definition.Key, Arguments: `{}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"amount":"9007199254740993","label":"原结果"}`)}}
	ref := sdk.ConversationResultReference{ConversationID: "old-source", RunID: "old-run", Step: 0, CallID: "original", SHA256: conversationDigest(original.Result)}
	root := sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: 2}
	r := &delegationSourcesTestRepository{executor: reader, original: original}
	r.d = sdk.ConversationDelegation{ID: "delegation", OwnerUserID: reader.UserID, ConversationID: "receiver", TaskID: "task", Status: "running", Requirements: sdk.ConversationAgentRequirements{Sources: []sdk.ConversationRunReference{root}}}
	r.run = sdk.ConversationRun{ID: "receiver-run", ConversationID: "receiver", Agent: &sdk.ConversationAgentSnapshot{DelegationRoleKey: reader.RoleKey}, BackgroundTask: &sdk.ConversationTaskExecution{TaskID: "task", DelegationID: r.d.ID, Requirements: r.d.Requirements}}
	r.snapshots = map[string]persistence.ConversationSourceSnapshot{root.RunID: {Authority: producer, Calls: []persistence.ConversationToolExecution{original}}, r.run.ID: {Authority: reader}}
	r.reads = map[string][]sdk.ConversationAuthority{}
	host := &deliveryReadTestHost{}
	policy := &executionBindingTestPolicy{deniedRole: "revoked"}
	s := &ConversationService{runtimeID: reader.RuntimeID, repo: r, options: ConversationOptions{ContextBytes: 32768, ToolHost: host, ToolDefinitions: []sdk.ConversationToolDefinition{definition}, CollaborationAuthorizer: policy}}
	args := sdk.ConversationDelegationSourceRead{ID: r.d.ID, ConversationResultRead: sdk.ConversationResultRead{Reference: ref, MaxBytes: 8192}}
	request := sdk.ConversationToolRequest{Authority: reader, ConversationID: r.run.ConversationID, RunID: r.run.ID}
	page, err := s.readDelegationSource(t.Context(), request, args)
	if err != nil || !page.Complete || r.lastProducer != producer || host.executionChecks != 0 || host.lastRequest.Authority != reader || host.lastRequest.ResultProducer == nil || *host.lastRequest.ResultProducer != producer {
		t.Fatal("admitted original proof became execution authorization", page, err, r.lastProducer, host.lastRequest)
	}
	definition, _ = collaborationTool("delegation_source_read")
	argJSON, _ := json.Marshal(args)
	pageJSON, _ := json.Marshal(page)
	wrapper := persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "read", Name: definition.Key, Arguments: string(argJSON)}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: r.d.ID, Content: pageJSON}}
	owner := sdk.ConversationRunReference{ConversationID: r.run.ConversationID, RunID: r.run.ID}
	if _, err := s.sourceAudit(reader).record(t.Context(), owner, wrapper); err != nil {
		t.Fatal("stored source page failed reauthorization", err)
	}
	// A later assignment changes the active execution role, while the old
	// page keeps its own run authority and remains read-only evidence.
	originalDelegation, originalExecutor := r.d, r.executor
	r.d.ConversationID, r.d.TaskID = "new-receiver", "new-task"
	r.executor.RoleKey = "new-binding-role"
	historical := &historicalDelegationSourcesTestRepository{delegationSourcesTestRepository: r, assignments: []sdk.ConversationDelegationAssignment{{Number: 1, ConversationID: r.run.ConversationID, TaskID: r.run.BackgroundTask.TaskID}}}
	s.repo = historical
	currentReader := r.executor
	if _, err := s.sourceAudit(currentReader).record(t.Context(), owner, wrapper); err != nil || host.lastRequest.Authority != currentReader || host.executionChecks != 0 {
		t.Fatal("current assignment role replaced immutable historical proof", err, host.lastRequest)
	}
	if _, err := s.readDelegationSource(t.Context(), sdk.ConversationToolRequest{Authority: currentReader, ConversationID: owner.ConversationID, RunID: owner.RunID}, args); !collaborationDenied(err) {
		t.Fatal("historical evidence granted a live invocation", err)
	}
	historical.assignments = nil
	if _, err := s.sourceAudit(currentReader).record(t.Context(), owner, wrapper); !collaborationDenied(err) {
		t.Fatal("unassigned historical run released a page", err)
	}
	historical.assignments = []sdk.ConversationDelegationAssignment{{Number: 1, ConversationID: r.run.ConversationID, TaskID: r.run.BackgroundTask.TaskID}}
	originalSnapshot := r.run.Agent
	r.run.Agent = &sdk.ConversationAgentSnapshot{DelegationRoleKey: "invented-role"}
	if _, err := s.sourceAudit(currentReader).record(t.Context(), owner, wrapper); !collaborationDenied(err) {
		t.Fatal("historical snapshot role contradicted immutable authority", err)
	}
	r.run.Agent = originalSnapshot
	r.d, r.executor, s.repo = originalDelegation, originalExecutor, r
	page.JSONText = "forged original value"
	wrapper.Result.Content, _ = json.Marshal(page)
	if _, err := s.sourceAudit(reader).record(t.Context(), owner, wrapper); err == nil {
		t.Fatal("a fabricated source page survived its immutable original")
	}
	wrapper.Result.Content = pageJSON
	host.err = conversationFailure("forbidden", "current_field_denied")
	if _, err := s.sourceAudit(reader).record(t.Context(), owner, wrapper); !collaborationDenied(err) {
		t.Fatal("wrapper laundered revoked source fields", err)
	}
	host.err = nil
	policy.deniedRole = producer.RoleKey
	if _, err := s.readDelegationSource(t.Context(), request, args); !collaborationDenied(err) {
		t.Fatal("old-role sharing revocation survived", err)
	}
	policy.deniedRole = "revoked"
	for _, change := range []string{"unlisted_run", "prefix", "wrong_delegation", "wrong_execution", "wrong_role"} {
		t.Run(change, func(t *testing.T) {
			changed, req := args, request
			switch change {
			case "unlisted_run":
				changed.Reference.RunID = "unlisted"
			case "prefix":
				changed.Reference.Step = 1
			case "wrong_delegation":
				changed.ID = "other"
			case "wrong_execution":
				req.RunID = "other-run"
			case "wrong_role":
				req.Authority.RoleKey = "other-reading-role"
			}
			if _, err := s.readDelegationSource(t.Context(), req, changed); err == nil {
				t.Fatal("source scope expanded", change)
			}
		})
	}
}
