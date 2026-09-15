package execution

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func ValidateCompletionRules(brief sdk.ConversationTaskBrief) error {
	if len(brief.VerificationRules) > len(brief.CompletionConditions) {
		return fmt.Errorf("completion_rules_invalid")
	}
	seen := map[int]bool{}
	for _, rule := range brief.VerificationRules {
		if rule.Condition < 0 || rule.Condition >= len(brief.CompletionConditions) || seen[rule.Condition] {
			return fmt.Errorf("completion_rules_invalid")
		}
		seen[rule.Condition] = true
		switch rule.Kind {
		case "data":
			if len(rule.Schema) == 0 || rule.Tool != "" || len(rule.ArgumentsSchema) != 0 || len(rule.ResultSchema) != 0 || rule.MinReceipts != 0 || rule.Completion != "" {
				return fmt.Errorf("completion_rules_invalid")
			}
		case "receipt":
			if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(rule.Tool) || len(rule.Schema) != 0 || rule.MinReceipts < 0 || rule.MinReceipts > 16 || rule.Completion != "" && rule.Completion != "completed" && rule.Completion != "accepted" {
				return fmt.Errorf("completion_rules_invalid")
			}
		default:
			return fmt.Errorf("completion_rules_invalid")
		}
		for _, raw := range []json.RawMessage{rule.Schema, rule.ArgumentsSchema, rule.ResultSchema} {
			if len(raw) == 0 {
				continue
			}
			if len(raw) > 8192 {
				return fmt.Errorf("completion_rules_invalid")
			}
			if _, err := CompileSchema(raw); err != nil {
				return fmt.Errorf("completion_rules_invalid: %w", err)
			}
		}
	}
	return nil
}

func ValidateConditionAssessments(brief sdk.ConversationTaskBrief, entries []sdk.ConversationConditionAssessment) error {
	if len(entries) > len(brief.CompletionConditions) {
		return fmt.Errorf("completion_assessment_invalid")
	}
	seen := map[int]bool{}
	for _, entry := range entries {
		if entry.Condition < 0 || entry.Condition >= len(brief.CompletionConditions) || seen[entry.Condition] || entry.Verdict != "met" && entry.Verdict != "unmet" && entry.Verdict != "unknown" || !utf8.ValidString(entry.Basis) || strings.ContainsRune(entry.Basis, 0) || len(entry.Basis) > 4096 || entry.Verdict != "unknown" && strings.TrimSpace(entry.Basis) == "" || len(entry.Receipts) > 16 {
			return fmt.Errorf("completion_assessment_invalid")
		}
		seen[entry.Condition] = true
		refs := map[sdk.ConversationResultReference]bool{}
		for _, ref := range entry.Receipts {
			if ref.ConversationID == "" || ref.RunID == "" || ref.CallID == "" || ref.Step < 0 || ref.Step > 255 || len(ref.SHA256) != 64 || refs[ref] {
				return fmt.Errorf("completion_evidence_invalid")
			}
			refs[ref] = true
		}
	}
	return nil
}

// ReceiptLookup supplies server-read, currently authorized immutable receipts.
// Missing/changed references are errors; unknown outcomes remain unknown.
type ReceiptLookup func(sdk.ConversationResultReference) (persistence.ConversationToolExecution, error)

func EvaluateCompletion(brief sdk.ConversationTaskBrief, delivery sdk.ConversationDelegationDelivery, review *sdk.ConversationDeliveryReview, method string, lookup ReceiptLookup) ([]sdk.ConversationCompletionCheck, bool, error) {
	if err := ValidateCompletionRules(brief); err != nil {
		return nil, false, err
	}
	if err := ValidateConditionAssessments(brief, delivery.Conditions); err != nil {
		return nil, false, err
	}
	if review != nil {
		if err := ValidateConditionAssessments(brief, review.Conditions); err != nil {
			return nil, false, err
		}
	}
	claims, judgments := map[int]sdk.ConversationConditionAssessment{}, map[int]sdk.ConversationConditionAssessment{}
	for _, entry := range delivery.Conditions {
		claims[entry.Condition] = entry
	}
	if review != nil {
		for _, entry := range review.Conditions {
			judgments[entry.Condition] = entry
		}
	}
	rules := map[int]sdk.ConversationCompletionRule{}
	for _, rule := range brief.VerificationRules {
		rules[rule.Condition] = rule
	}
	checks := []sdk.ConversationCompletionCheck{}
	ready := len(brief.CompletionConditions) > 0 && len(delivery.Unresolved) == 0
	validate := func(raw json.RawMessage, value json.RawMessage) bool {
		if len(raw) == 0 {
			return true
		}
		schema, err := CompileSchema(raw)
		return err == nil && ValidateJSON(schema, value) == nil
	}
	for i, requirement := range brief.CompletionConditions {
		entry, supplied := claims[i]
		check := sdk.ConversationCompletionCheck{Condition: i, Requirement: requirement, Verdict: "unknown", Method: "pending", Basis: "等待逐项核对"}
		if judgment, ok := judgments[i]; ok {
			entry, supplied = judgment, true
		}
		if supplied {
			check.Basis, check.Receipts = entry.Basis, entry.Receipts
		}
		records := []persistence.ConversationToolExecution{}
		for _, ref := range check.Receipts {
			record, err := lookup(ref)
			if err != nil {
				return nil, false, err
			}
			records = append(records, record)
		}
		if rule, ok := rules[i]; ok {
			check.Method = "program"
			switch rule.Kind {
			case "data":
				check.Verdict, check.Basis = "unmet", "交付数据不符合该项约定检查"
				if len(delivery.Data) > 0 && validate(rule.Schema, delivery.Data) {
					check.Verdict, check.Basis = "met", "交付数据通过该项约定检查；实际业务事实以证据核对为准"
				}
			case "receipt":
				check.Verdict, check.Basis = "met", "原操作回执及参数／结果符合该项约定"
				if len(records) < max(1, rule.MinReceipts) {
					check.Verdict, check.Basis = "unknown", "尚未提供足够的原操作回执"
				}
				origins := map[string]bool{}
				for _, record := range records {
					// Reused copies cannot count as separate business effects.
					key := record.IdempotencyKey
					if key == "" {
						return nil, false, fmt.Errorf("completion_evidence_invalid")
					}
					if origins[key] {
						check.Verdict, check.Basis = "unmet", "同一原操作的回执不能重复计数"
						break
					}
					origins[key] = true
					if record.Call.Name != rule.Tool || !validate(rule.ArgumentsSchema, json.RawMessage(record.Call.Arguments)) {
						check.Verdict, check.Basis = "unmet", "原操作工具或参数与约定不符"
						break
					}
					if record.State != "completed" || record.Result == nil || record.Result.Status == "uncertain" {
						if check.Verdict != "unmet" {
							check.Verdict, check.Basis = "unknown", "原操作结果尚未明确"
						}
						continue
					}
					if record.Result.Status != "completed" || record.Result.Completion == "accepted" && rule.Completion != "accepted" || !validate(rule.ResultSchema, record.Result.Content) {
						check.Verdict, check.Basis = "unmet", "原回执尚未达到约定的完成状态或结果"
						break
					}
				}
			}
		} else if supplied {
			check.Verdict = entry.Verdict
			check.Method = "recipient"
			if _, reviewed := judgments[i]; reviewed {
				check.Method = method
			}
		}
		if check.Verdict != "met" || check.Method == "recipient" || check.Method == "pending" {
			ready = false
		}
		checks = append(checks, check)
	}
	return checks, ready, nil
}
