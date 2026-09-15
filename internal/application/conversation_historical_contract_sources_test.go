package application

import (
	"context"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type historicalContractSourceRepository struct {
	*contractPublicationTestRepository
	persistence.ConversationDelegationTransferRepository
	assignments []sdk.ConversationDelegationAssignment
}

func (r *historicalContractSourceRepository) ConversationDelegationAssignments(context.Context, string, sdk.ConversationAuthority) ([]sdk.ConversationDelegationAssignment, error) {
	return r.assignments, nil
}

func TestHistoricalContractRunReadsItsOriginalRootsAfterRequirementsAndAssignmentChange(t *testing.T) {
	s, base, policy, reader, original := contractPublicationServiceFixture()
	root := base.original.Requirements.Sources[0]
	base.original.Agreement.InputSource = &root
	today := sdk.ConversationRunReference{ConversationID: "today", RunID: "today", BeforeStep: 1}
	base.d.ConversationID, base.d.TaskID = "new-assignment", "new-task"
	base.d.InputSource = &today
	base.d.Requirements = sdk.ConversationAgentRequirements{TaskType: "today", Sources: []sdk.ConversationRunReference{today}}
	old := sdk.ConversationRun{ID: "old-run", ConversationID: "old-assignment", BackgroundTask: &sdk.ConversationTaskExecution{TaskID: "old-task", DelegationID: base.d.ID, AgreementRevision: 1, BriefVersion: 1, Requirements: base.original.Requirements, InputSource: &root}}
	base.snapshots[old.ID] = persistence.ConversationSourceSnapshot{Authority: reader, Run: old}
	base.snapshots[today.RunID] = persistence.ConversationSourceSnapshot{Authority: reader}
	publisher := reader
	publisher.RoleKey = "explicit-original-publisher"
	base.publicationAuthority = &publisher
	r := &historicalContractSourceRepository{contractPublicationTestRepository: base, assignments: []sdk.ConversationDelegationAssignment{{Number: 1, ConversationID: old.ConversationID, TaskID: old.BackgroundTask.TaskID, AgreementRevision: 1}}}
	s.repo = r
	ref := sdk.ConversationRunReference{ConversationID: old.ConversationID, RunID: old.ID}
	ctx, err := s.delegationRunSourceContext(t.Context(), old, reader)
	if err != nil {
		t.Fatal(err)
	}
	scope, ok := ctx.Value(conversationPublishedSourceKey{}).(conversationPublishedSource)
	if !ok || !scope.contains(root) || scope.contains(today) {
		t.Fatal("historical run selected a different publication scope", scope)
	}
	roots, err := s.sourceAudit(reader).run(t.Context(), ref)
	if err != nil || len(base.reads[root.RunID]) == 0 || len(base.reads[today.RunID]) != 0 || base.publicationReads == 0 {
		t.Fatal("historical run used today's requirements, assignment, or source scope", roots, err, base.reads)
	}
	for _, revoked := range []string{publisher.RoleKey, original.RoleKey} {
		policy.deniedRole = revoked
		if _, err := s.sourceAudit(reader).run(t.Context(), ref); !collaborationDenied(err) {
			t.Fatal("historical source survived current publication/provider withdrawal", revoked, err)
		}
	}
	policy.deniedRole = "revoked"
	base.sourceErr = conversationFailure("forbidden", "current_field_denied")
	if _, err := s.sourceAudit(reader).run(t.Context(), ref); !collaborationDenied(err) {
		t.Fatal("historical agreement skipped current data policy", err)
	}
	base.sourceErr = nil
	for _, kind := range []string{"unassigned", "agreement", "brief", "requirements", "input", "negative-agreement", "negative-brief"} {
		t.Run(kind, func(t *testing.T) {
			changed := old
			task := *old.BackgroundTask
			changed.BackgroundTask = &task
			switch kind {
			case "unassigned":
				task.TaskID = "unassigned-task"
			case "agreement":
				task.AgreementRevision = 2
			case "brief":
				task.BriefVersion = 2
			case "requirements":
				task.Requirements = base.d.Requirements
			case "input":
				task.InputSource = &today
			case "negative-agreement":
				task.AgreementRevision = -1
			case "negative-brief":
				task.BriefVersion = -1
			}
			if _, err := s.delegationRunSourceContext(t.Context(), changed, reader); err == nil {
				t.Fatal("unverified historical task adopted today's contract", kind)
			}
		})
	}
}

func TestHistoricalRunWithoutImmutableContractPortCannotUseCurrentContract(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	root := sdk.ConversationRunReference{ConversationID: "root", RunID: "root", BeforeStep: 1}
	r := &delegationSourcesTestRepository{d: sdk.ConversationDelegation{ID: "delegation", ConversationID: "receiver", TaskID: "task", AgreementRevision: 2, Requirements: sdk.ConversationAgentRequirements{Sources: []sdk.ConversationRunReference{root}}}}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: r}
	run := sdk.ConversationRun{ConversationID: "receiver", BackgroundTask: &sdk.ConversationTaskExecution{TaskID: "task", DelegationID: r.d.ID, AgreementRevision: 1, Requirements: r.d.Requirements}}
	if _, err := s.delegationRunSourceContext(t.Context(), run, a); !collaborationDenied(err) {
		t.Fatal("historical run without original version evidence used current requirements", err)
	}
}

type resumeContractSourceRepository struct {
	*contractPublicationTestRepository
}

func (r *resumeContractSourceRepository) ConversationDelegationTask(context.Context, string, sdk.ConversationAuthority) (sdk.ConversationTask, error) {
	// Legacy delegation tasks may have no bound Agent snapshot. Their
	// repository-owned execution authority is still independently checked.
	return sdk.ConversationTask{Status: sdk.ConversationTaskStatusCancelled, Requirements: r.original.Requirements}, nil
}

func TestDelegationResumeChecksOriginalExecutorAndSourcesWithoutBoundSnapshot(t *testing.T) {
	s, base, policy, actor, _ := contractPublicationServiceFixture()
	base.d.AgreementRevision = 1
	base.d.Requirements = base.original.Requirements
	base.executor.RoleKey = "original-execution-role"
	s.repo = &resumeContractSourceRepository{contractPublicationTestRepository: base}
	policy.deniedRole = base.executor.RoleKey
	if err := s.authorizeDelegationResume(t.Context(), base.d, actor); !collaborationDenied(err) {
		t.Fatal("legacy/unbound task used the manager's current role after executor withdrawal", err)
	}
	policy.deniedRole = "revoked"
	base.sourceErr = conversationFailure("forbidden", "current_field_denied")
	if err := s.authorizeDelegationResume(t.Context(), base.d, actor); !collaborationDenied(err) {
		t.Fatal("legacy/unbound task resumed without current original source authorization", err)
	}
	base.sourceErr = nil
	if err := s.authorizeDelegationResume(t.Context(), base.d, actor); err != nil {
		t.Fatal("authorized original legacy executor could not resume", err)
	}
	checkedExecutor := false
	for _, request := range policy.requests {
		for _, operation := range request.Operations {
			if operation == "receive" {
				checkedExecutor = checkedExecutor || request.Authority == base.executor
				if request.Authority == actor {
					t.Fatal("manager substituted as the accepted executor", request)
				}
			}
		}
	}
	if !checkedExecutor || len(base.reads[base.original.Requirements.Sources[0].RunID]) == 0 {
		t.Fatal("resume skipped original execution identity or immutable requirements", policy.requests, base.reads)
	}
}
