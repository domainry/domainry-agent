package application

import (
	"context"
	"encoding/json"
	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"strings"
	"testing"
)

type executionPublicationMetadataRepo struct{ executionToolSourceGraph }

func (r *executionPublicationMetadataRepo) ConversationDelegationExecutionPublications(context.Context, string, sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	return r.publications, nil
}

func TestExecutionPublicationOwnerMetadataRequiresOnlyViewAndRejectsForeignProofs(t *testing.T) {
	s, graph, owner, _ := executionSourceGraphFixture()
	repo := &executionPublicationMetadataRepo{executionToolSourceGraph: *graph}
	s.repo = repo
	policy := &executionBindingTestPolicy{}
	s.options.CollaborationAuthorizer = policy
	proof := persistence.ConversationSourceRelease{DelegationID: "delegation", Purpose: "execution", Reference: sdk.ConversationRunReference{ConversationID: "original", RunID: "run"}, Producer: owner, Publisher: &owner}
	repo.publications = []persistence.ConversationSourceRelease{proof}
	values, err := s.ConversationDelegationExecutionPublications(t.Context(), "delegation", owner)
	if err != nil || len(values) != 1 || values[0].Publisher != owner {
		t.Fatal("current owner view did not return safe metadata", values, err)
	}
	for _, request := range policy.requests {
		for _, operation := range request.Operations {
			if operation != "view" {
				t.Fatal("withdrawal metadata required execution or share authority", request)
			}
		}
	}
	for _, mutation := range []func(*persistence.ConversationSourceRelease){
		func(p *persistence.ConversationSourceRelease) { p.DelegationID = "other" },
		func(p *persistence.ConversationSourceRelease) { p.Purpose = "delivery" },
		func(p *persistence.ConversationSourceRelease) { p.Reference.BeforeStep = 2 },
		func(p *persistence.ConversationSourceRelease) { p.Reference.RunID = "" },
		func(p *persistence.ConversationSourceRelease) { p.Producer.UserID = "other-user" },
		func(p *persistence.ConversationSourceRelease) { p.Producer.WorkspaceID = "other-workspace" },
		func(p *persistence.ConversationSourceRelease) { p.Publisher = nil },
		func(p *persistence.ConversationSourceRelease) {
			foreign := owner
			foreign.UserID = "other-user"
			p.Publisher = &foreign
		},
		func(p *persistence.ConversationSourceRelease) {
			foreign := owner
			foreign.RuntimeID = "other-runtime"
			p.Publisher = &foreign
		},
	} {
		invalid := proof
		mutation(&invalid)
		repo.publications = []persistence.ConversationSourceRelease{proof, invalid}
		if values, err := s.ConversationDelegationExecutionPublications(t.Context(), "delegation", owner); err == nil || len(values) != 0 {
			t.Fatal("foreign or malformed proof returned metadata", values, err)
		}
	}
	repo.publications = []persistence.ConversationSourceRelease{proof}
	policy.deniedRole = owner.RoleKey
	if values, err := s.ConversationDelegationExecutionPublications(t.Context(), "delegation", owner); err == nil || len(values) != 0 {
		t.Fatal("current view denial returned metadata", values, err)
	}
}

func TestExecutionSharingDoesNotExposeUnprovedPayloadsOrTheirGeneratedReply(t *testing.T) {
	secret := "unproved-private-business-value"
	run := sdk.ConversationRun{DraftText: secret, Steps: []sdk.ConversationStepView{{Number: 0, Text: secret, Calls: []sdk.ConversationToolView{{ID: "failed-call", Name: "business_action", Status: "failed", Arguments: secret, ResultPreview: secret, ResourceID: secret, ErrorCode: secret, ResultReference: &sdk.ConversationResultReference{RunID: secret}}, {ID: "pending-call", Name: "business_action", Status: "running", Arguments: secret}, {ID: "successful-call", Name: "clock", Status: "completed", Arguments: "{}", ResultPreview: "verified-clock-result"}}}}}
	records := []persistence.ConversationToolExecution{{Step: 0, Call: sdk.ConversationToolCall{ID: "failed-call"}, State: "completed", Result: &sdk.ConversationToolResult{Status: "failed", Content: []byte(`{"private":"unproved-private-business-value"}`)}}, {Step: 0, Call: sdk.ConversationToolCall{ID: "successful-call"}, State: "completed", Result: &sdk.ConversationToolResult{Status: "completed"}}}
	redactUnverifiedExecutionCalls(&run, records)
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || !strings.Contains(string(raw), "verified-clock-result") || run.Steps[0].Calls[0].Status != "failed" || run.Steps[0].Calls[1].Status != "running" {
		t.Fatal("unproved payload escaped or public progress was lost", string(raw))
	}
}
