package application

import (
	"context"
	"errors"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/entityid"
)

func TestRunIDGeneratorsUseCanonical128BitIdentities(t *testing.T) {
	for _, generator := range []struct {
		prefix string
		newID  func() (string, error)
	}{{"irun", newInteractiveRunID}, {"trun", newAgentExecutionID}} {
		seen := map[string]bool{}
		for range 256 {
			id, err := generator.newID()
			if err != nil || entityid.Validate(generator.prefix, id) != nil || seen[id] {
				t.Fatalf("prefix=%s id=%s duplicate=%v err=%v", generator.prefix, id, seen[id], err)
			}
			seen[id] = true
		}
	}
}

func TestInteractiveRunDoesNotPersistOnIDGenerationFailure(t *testing.T) {
	repository := &interactiveStateRepositoryStub{}
	failure := errors.New("entropy unavailable")
	service := NewInteractiveStateServiceWithRuntime(repository, nil, func() (string, error) { return "", failure })
	authority := agentpersistence.AgentInteractiveAuthority{Known: true, WorkspaceID: "workspace", UserID: "user", RoleKey: "role"}
	run := agentmodel.AgentInteractiveRun{Context: agentsdk.GlobalContext{Principal: agentsdk.PrincipalReference{WorkspaceID: "workspace", UserID: "user", RoleKey: "role"}}}
	if _, _, err := service.Create(t.Context(), run, authority); !errors.Is(err, failure) {
		t.Fatalf("ID generation error was lost: %v", err)
	}
	if repository.run.ID != "" {
		t.Fatalf("persisted a fallback identity: %s", repository.run.ID)
	}
}

type failedIDExecutionState struct {
	agentpersistence.AgentInteractiveStateService
	failed bool
}

func (s *failedIDExecutionState) Complete(_ context.Context, run agentmodel.AgentInteractiveRun, status agentmodel.AgentInteractiveRunStatus, _ map[string]any, _ string) (agentmodel.AgentInteractiveRun, error) {
	s.failed = status == agentmodel.AgentInteractiveRunFailed
	return run, nil
}

func TestTaskHandoffFailsClosedOnIDGenerationFailure(t *testing.T) {
	failure := errors.New("entropy unavailable")
	state := &failedIDExecutionState{}
	service := NewInteractiveExecutionService(InteractiveExecutionDependencies{
		State: state, Host: &interactiveExecutionHostStub{}, NewID: func() (string, error) { return "", failure },
	})
	_, err := service.handoffTask(t.Context(), agentmodel.AgentInteractiveRun{}, agentsdk.InteractiveResult{}, agentsdk.RouteResult{}, modulehost.InteractiveAuthorization{})
	if !errors.Is(err, failure) || !state.failed {
		t.Fatalf("failed handoff did not retain failure: err=%v state=%+v", err, state)
	}
}

func TestDirectTaskRunIDUsesStableScopedBusinessIdentity(t *testing.T) {
	principal := modulehost.Principal{WorkspaceID: "workspace", UserID: "user"}
	first, err := directTaskRunID(principal, "command")
	if err != nil || entityid.Validate("trun", first) != nil {
		t.Fatalf("id=%s err=%v", first, err)
	}
	second, err := directTaskRunID(principal, "command")
	if err != nil || first != second {
		t.Fatal("same command did not retain its identity")
	}
	principal.UserID = "another-user"
	third, err := directTaskRunID(principal, "command")
	if err != nil || first == third {
		t.Fatal("different actors shared one task identity")
	}
	if _, err := directTaskRunID(principal, ""); err == nil {
		t.Fatal("blank idempotency key was accepted")
	}
}

type identityProposalState struct {
	agentsdk.AgentDialogStateService
}

func (*identityProposalState) StoreProposal(_ context.Context, proposal agentsdk.AgentProposal) (agentsdk.AgentProposal, error) {
	return proposal, nil
}

func TestProposalIdentityIsOwnedByApplicationNotTimestampTransport(t *testing.T) {
	service := NewProposalService(&identityProposalState{}, nil, nil, nil)
	principal := modulehost.Principal{WorkspaceID: "workspace", UserID: "user", RoleKey: "role"}
	proposal, err := service.Create(t.Context(), CreateProposalRequest{Principal: principal})
	if err != nil || entityid.Validate("proposal", proposal.ProposalID) != nil || proposal.Metadata["proposal_id"] != proposal.ProposalID {
		t.Fatalf("proposal=%+v err=%v", proposal, err)
	}
	draft := map[string]any{"proposed": map[string]any{"action_binding": map[string]any{"action_key": "create"}}}
	first, err := service.CreateHostDraft(t.Context(), draft, principal)
	if err != nil || entityid.Validate("proposal", first.ProposalID) != nil {
		t.Fatalf("host draft=%+v err=%v", first, err)
	}
	second, err := service.CreateHostDraft(t.Context(), draft, principal)
	if err != nil || first.ProposalID != second.ProposalID {
		t.Fatal("host draft identity is not deterministic")
	}
	principal.UserID = "another-user"
	third, err := service.CreateHostDraft(t.Context(), draft, principal)
	if err != nil || first.ProposalID == third.ProposalID {
		t.Fatal("different actors shared one proposal identity")
	}
}
