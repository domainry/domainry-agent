package execution

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestCompletionChecksUseDataAndActualReceiptSemantics(t *testing.T) {
	ref := sdk.ConversationResultReference{ConversationID: "c", RunID: "r", Step: 0, CallID: "write", SHA256: strings.Repeat("a", 64)}
	brief := sdk.ConversationTaskBrief{CompletionConditions: []string{"Positive total", "Operation actually completed"}, VerificationRules: []sdk.ConversationCompletionRule{
		{Condition: 0, Kind: "data", Schema: json.RawMessage(`{"type":"object","properties":{"total":{"type":"number","minimum":1}},"required":["total"]}`)},
		{Condition: 1, Kind: "receipt", Tool: "create_thing", ArgumentsSchema: json.RawMessage(`{"type":"object","properties":{"period":{"const":"Q1"}},"required":["period"]}`), ResultSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":1}},"required":["id"]}`)},
	}}
	delivery := sdk.ConversationDelegationDelivery{Data: json.RawMessage(`{"total":2}`), Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "I say it passed"}, {Condition: 1, Verdict: "met", Basis: "Original receipt", Receipts: []sdk.ConversationResultReference{ref}}}}
	good := persistence.ConversationToolExecution{State: "completed", IdempotencyKey: "original-operation", Call: sdk.ConversationToolCall{Name: "create_thing", Arguments: `{"period":"Q1"}`}, Result: &sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"saved"}`)}}
	for _, test := range []struct {
		name, state, status, completion, arguments string
		expected                                   string
	}{
		{"completed", "completed", "completed", "", `{"period":"Q1"}`, "met"},
		{"accepted_is_not_completed", "completed", "completed", "accepted", `{"period":"Q1"}`, "unmet"},
		{"unknown_is_not_failure", "uncertain", "uncertain", "", `{"period":"Q1"}`, "unknown"},
		{"definitive_failure", "completed", "failed", "", `{"period":"Q1"}`, "unmet"},
		{"wrong_period", "completed", "completed", "", `{"period":"Q2"}`, "unmet"},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := good
			result := *good.Result
			record.Result = &result
			record.State = test.state
			result.Status = test.status
			result.Completion = test.completion
			record.Call.Arguments = test.arguments
			checks, ready, err := EvaluateCompletion(brief, delivery, nil, "agent", func(sdk.ConversationResultReference) (persistence.ConversationToolExecution, error) {
				return record, nil
			})
			if err != nil || checks[1].Verdict != test.expected || ready != (test.expected == "met") {
				t.Fatalf("wrong business verdict: %+v %v %v", checks, ready, err)
			}
		})
	}
	delivery.Data = json.RawMessage(`{"total":0}`)
	checks, ready, err := EvaluateCompletion(brief, delivery, &sdk.ConversationDeliveryReview{Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Override the program"}}}, "user", func(sdk.ConversationResultReference) (persistence.ConversationToolExecution, error) { return good, nil })
	if err != nil || ready || checks[0].Verdict != "unmet" || checks[0].Method != "program" {
		t.Fatalf("claim overrode actual data: %+v %v", checks, err)
	}
	delivery.Data = json.RawMessage(`{"total":2}`)
	duplicate := ref
	duplicate.RunID = "another-assignment"
	delivery.Conditions[1].Receipts = append(delivery.Conditions[1].Receipts, duplicate)
	brief.VerificationRules[1].MinReceipts = 2
	checks, ready, err = EvaluateCompletion(brief, delivery, nil, "agent", func(sdk.ConversationResultReference) (persistence.ConversationToolExecution, error) { return good, nil })
	if err != nil || ready || checks[1].Verdict != "unmet" {
		t.Fatalf("two copies counted as two effects: %+v %v", checks, err)
	}
}

func TestCompletionSeparatesRecipientClaimsFromReviewAndUnresolvedWork(t *testing.T) {
	brief := sdk.ConversationTaskBrief{CompletionConditions: []string{"Readable for the audience"}}
	delivery := sdk.ConversationDelegationDelivery{Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "I wrote it"}}}
	checks, ready, err := EvaluateCompletion(brief, delivery, nil, "agent", nil)
	if err != nil || ready || checks[0].Method != "recipient" {
		t.Fatalf("recipient self-accepted: %+v %v", checks, err)
	}
	review := &sdk.ConversationDeliveryReview{Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Reviewed terminology and conclusions against the audience"}}}
	checks, ready, err = EvaluateCompletion(brief, delivery, review, "user", nil)
	if err != nil || !ready || checks[0].Method != "user" {
		t.Fatalf("user decision lost: %+v %v", checks, err)
	}
	delivery.Unresolved = []string{"One missing appendix"}
	_, ready, err = EvaluateCompletion(brief, delivery, review, "user", nil)
	if err != nil || ready {
		t.Fatal("unresolved work accepted", err)
	}
	review.Conditions[0].Basis = ""
	if _, _, err = EvaluateCompletion(brief, delivery, review, "user", nil); err == nil {
		t.Fatal("unexplained verdict accepted")
	}
}

func TestCompletionRejectsInvalidRulesAndDoesNotHideEvidenceErrors(t *testing.T) {
	brief := sdk.ConversationTaskBrief{CompletionConditions: []string{"One"}, VerificationRules: []sdk.ConversationCompletionRule{{Condition: 1, Kind: "data", Schema: json.RawMessage(`true`)}}}
	if ValidateCompletionRules(brief) == nil {
		t.Fatal("out-of-range rule accepted")
	}
	brief.VerificationRules[0].Condition = 0
	brief.VerificationRules[0].Schema = json.RawMessage(`{"$ref":"https://example.invalid/schema.json"}`)
	if ValidateCompletionRules(brief) == nil {
		t.Fatal("remote schema loaded")
	}
	brief.VerificationRules = nil
	ref := sdk.ConversationResultReference{ConversationID: "c", RunID: "r", CallID: "tool", SHA256: strings.Repeat("a", 64)}
	delivery := sdk.ConversationDelegationDelivery{Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Evidence", Receipts: []sdk.ConversationResultReference{ref}}}}
	want := errors.New("source revoked")
	if _, _, err := EvaluateCompletion(brief, delivery, nil, "user", func(sdk.ConversationResultReference) (persistence.ConversationToolExecution, error) {
		return persistence.ConversationToolExecution{}, want
	}); !errors.Is(err, want) {
		t.Fatal("evidence denial hidden", err)
	}
	legacy, _ := DependencyProjection(brief, nil, nil)
	if strings.Contains(string(legacy), "verification_rules") {
		t.Fatal("empty rules invalidated existing dependency digests")
	}
}
