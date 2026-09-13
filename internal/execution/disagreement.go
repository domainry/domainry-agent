package execution

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func disagreementText(value string, limit int, required bool) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0) && len(value) <= limit && (!required || strings.TrimSpace(value) != "")
}

func ValidateDisagreementChange(d sdk.ConversationDelegation, in *sdk.ConversationDisagreementChange) error {
	invalid := func() error { return fmt.Errorf("disagreement_invalid") }
	if in == nil {
		return invalid()
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > 16384 {
		return invalid()
	}
	switch in.Operation {
	case "raise":
		if in.ID != "" || in.ExpectedRevision != 0 || !disagreementText(in.Title, 512, true) || len(in.Claims) < 2 || len(in.Claims) > 4 || in.Decision != nil {
			return invalid()
		}
		if in.Condition != nil && (*in.Condition < 0 || *in.Condition >= len(d.Brief.CompletionConditions)) {
			return invalid()
		}
	case "add_claim":
		if in.ID == "" || in.ExpectedRevision < 1 || in.Title != "" || in.Condition != nil || len(in.Claims) != 1 || in.Decision != nil {
			return invalid()
		}
	case "decide":
		if in.ID == "" || in.ExpectedRevision < 1 || in.Title != "" || in.Condition != nil || len(in.Claims) != 0 || in.Decision == nil {
			return invalid()
		}
		v := in.Decision
		if !disagreementText(v.Basis, 2048, true) || !disagreementText(v.NextAction, 1024, v.Outcome != "adopt") || v.BriefVersion < 1 || v.AgreementRevision < 1 {
			return invalid()
		}
		for _, text := range []string{v.Comparison.DataScope, v.Comparison.Period, v.Comparison.SourceVersion, v.Comparison.Calculation} {
			if !disagreementText(text, 1024, true) {
				return invalid()
			}
		}
		switch v.Outcome {
		case "adopt":
			if v.AdoptClaimID == "" || v.OwnerAgentID != "" || v.NextAction != "" {
				return invalid()
			}
		case "inspect", "revise":
			if v.AdoptClaimID != "" || (v.OwnerAgentID != d.FromAgentID && v.OwnerAgentID != d.ToAgentID) {
				return invalid()
			}
		case "ask_user":
			if v.AdoptClaimID != "" || v.OwnerAgentID != "" {
				return invalid()
			}
		default:
			return invalid()
		}
	default:
		return invalid()
	}
	for _, claim := range in.Claims {
		if !disagreementText(claim.Conclusion, 2048, true) || !disagreementText(claim.Calculation, 2048, true) {
			return invalid()
		}
		for _, value := range []string{claim.DataScope, claim.Period, claim.SourceVersion} {
			if !disagreementText(value, 512, true) {
				return invalid()
			}
		}
		if len(claim.Receipts) > 4 {
			return invalid()
		}
		if err := ValidateConditionAssessments(sdk.ConversationTaskBrief{CompletionConditions: []string{"evidence"}}, []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "unknown", Receipts: claim.Receipts}}); err != nil {
			return invalid()
		}
	}
	return nil
}

func DisagreementBlocks(v sdk.ConversationDisagreementSummary, d sdk.ConversationDelegation, deliveryDigest string) bool {
	return v.Status != "resolved" || v.BriefVersion != d.Brief.Version || v.AgreementRevision != d.AgreementRevision || v.DeliveryDigest != deliveryDigest
}
