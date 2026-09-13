package execution

import (
	"encoding/json"
	"fmt"
	"sort"

	sdk "github.com/domainry/domainry-agent-sdk"
)

var briefFields = []string{"goal", "deliverable", "audience", "constraints", "completion_conditions", "verification_rules", "assumptions", "due_at"}

func BriefProjection(brief sdk.ConversationTaskBrief, fields []string) (json.RawMessage, error) {
	if len(fields) == 0 {
		fields = currentBriefFields(brief)
	}
	if len(fields) > len(briefFields) {
		return nil, fmt.Errorf("too many agreement fields")
	}
	raw, err := json.Marshal(brief)
	if err != nil {
		return nil, err
	}
	var values map[string]json.RawMessage
	if err = json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	selected := map[string]json.RawMessage{}
	for _, key := range fields {
		valid := false
		for _, known := range briefFields {
			valid = valid || key == known
		}
		if !valid {
			return nil, fmt.Errorf("invalid agreement field")
		}
		if _, exists := selected[key]; exists {
			return nil, fmt.Errorf("repeated agreement field")
		}
		value := values[key]
		if value == nil {
			value = json.RawMessage("null")
		}
		selected[key] = value
	}
	return json.Marshal(selected)
}

func currentBriefFields(brief sdk.ConversationTaskBrief) []string {
	fields := []string{}
	for _, field := range briefFields {
		if field != "verification_rules" || len(brief.VerificationRules) > 0 {
			fields = append(fields, field)
		}
	}
	return fields
}

func ChangedBriefFields(before, after sdk.ConversationTaskBrief) []string {
	out := []string{}
	for _, key := range briefFields {
		a, _ := BriefProjection(before, []string{key})
		b, _ := BriefProjection(after, []string{key})
		if string(a) != string(b) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// Input is a separate dependency field, so a change to data does not invalidate
// a task that explicitly depends only on an unrelated brief field.
func DependencyUsesInput(fields []string) bool {
	if len(fields) == 0 {
		return true
	}
	for _, field := range fields {
		if field == "input" {
			return true
		}
	}
	return false
}
func DependencyProjection(brief sdk.ConversationTaskBrief, input *sdk.ConversationStructuredInput, fields []string) (json.RawMessage, error) {
	if len(fields) == 0 {
		fields = append(currentBriefFields(brief), "input")
	}
	other := []string{}
	inputCount := 0
	for _, field := range fields {
		if field == "input" {
			inputCount++
		} else {
			other = append(other, field)
		}
	}
	if inputCount > 1 {
		return nil, fmt.Errorf("repeated agreement field")
	}
	values := map[string]json.RawMessage{}
	if len(other) > 0 {
		raw, err := BriefProjection(brief, other)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &values); err != nil {
			return nil, err
		}
	}
	if inputCount == 1 {
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		values["input"] = raw
	}
	return json.Marshal(values)
}
