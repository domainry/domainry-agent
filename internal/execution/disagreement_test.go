package execution

import (
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func TestDisagreementDecisionRequiresEvidenceComparisonAndConcreteResponsibility(t *testing.T) {
	d := sdk.ConversationDelegation{FromAgentID: "issuer", ToAgentID: "receiver", Brief: sdk.ConversationTaskBrief{Version: 1}, AgreementRevision: 2}
	valid := sdk.ConversationDisagreementDecision{Outcome: "inspect", OwnerAgentID: "receiver", NextAction: "Read the original quarterly rows", Basis: "The totals cover different periods", BriefVersion: 1, AgreementRevision: 2, Comparison: sdk.ConversationEvidenceComparison{DataScope: "Same account", Period: "Q1 versus Q2", SourceVersion: "Report v1", Calculation: "Sum of quarterly rows"}}
	cases := []struct {
		name   string
		change func(*sdk.ConversationDisagreementDecision)
	}{
		{"missing scope", func(v *sdk.ConversationDisagreementDecision) { v.Comparison.DataScope = "" }},
		{"missing period", func(v *sdk.ConversationDisagreementDecision) { v.Comparison.Period = "" }},
		{"missing version", func(v *sdk.ConversationDisagreementDecision) { v.Comparison.SourceVersion = "" }},
		{"missing calculation", func(v *sdk.ConversationDisagreementDecision) { v.Comparison.Calculation = "" }},
		{"unspecified work", func(v *sdk.ConversationDisagreementDecision) { v.NextAction = "  " }},
		{"unrelated owner", func(v *sdk.ConversationDisagreementDecision) { v.OwnerAgentID = "unrelated" }},
		{"user wait assigned to Agent", func(v *sdk.ConversationDisagreementDecision) { v.Outcome = "ask_user" }},
		{"adopt without exact claim", func(v *sdk.ConversationDisagreementDecision) {
			v.Outcome = "adopt"
			v.OwnerAgentID = ""
			v.NextAction = ""
		}},
	}
	change := func(v *sdk.ConversationDisagreementDecision) *sdk.ConversationDisagreementChange {
		return &sdk.ConversationDisagreementChange{Operation: "decide", ID: "issue", ExpectedRevision: 1, Decision: v}
	}
	if err := ValidateDisagreementChange(d, change(&valid)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := valid
			tc.change(&v)
			if err := ValidateDisagreementChange(d, change(&v)); err == nil {
				t.Fatal("incomplete evidence decision accepted")
			}
		})
	}
	resolved := sdk.ConversationDisagreementSummary{Status: "resolved", BriefVersion: 1, AgreementRevision: 2, DeliveryDigest: "original"}
	if DisagreementBlocks(resolved, d, "original") {
		t.Fatal("current resolution blocked")
	}
	if !DisagreementBlocks(resolved, d, "replacement") {
		t.Fatal("old decision accepted a replacement delivery")
	}
	d.AgreementRevision++
	if !DisagreementBlocks(resolved, d, "original") {
		t.Fatal("old decision accepted a replacement agreement")
	}
}
