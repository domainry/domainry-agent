package reportmodulehost

import (
	"testing"

	record "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestSharedRecordScopeNegationPreservesUnknownAndValidatesWholeTrees(t *testing.T) {
	atom := func(operator string, values ...string) *record.RecordScopeExpression {
		return &record.RecordScopeExpression{Operator: operator, FieldKey: "name", Values: values}
	}
	not := func(child *record.RecordScopeExpression) *record.RecordScopeExpression {
		return &record.RecordScopeExpression{Operator: "not", Children: []record.RecordScopeExpression{*child}}
	}
	for _, tc := range []struct {
		name           string
		source, reader *record.RecordScopeExpression
		allow          bool
	}{
		// At NULL, NOT(IS NOT NULL) is TRUE, whereas NOT(name='Alpha') is
		// UNKNOWN. Reversing only true-row implication would leak this row.
		{"SQL unknown cannot become false", not(atom("exists")), not(atom("eq", "Alpha")), false},
		{"weaker exclusion", not(atom("in", "Gamma", "Hidden")), not(atom("eq", "Gamma")), true},
		{"narrower exclusion", not(atom("eq", "Gamma")), not(atom("in", "Gamma", "Hidden")), false},
		{"not empty IN includes null", nil, not(atom("in")), true},
		{"empty IN contains no rows", atom("in"), atom("not_exists"), true},
		{"literal inequality does not prove SQL disjointness", atom("eq", "Alpha"), not(atom("eq", "alpha")), false},
		{"source unsupported sibling", &record.RecordScopeExpression{Operator: "and", Children: []record.RecordScopeExpression{*atom("eq", "Alpha"), *atom("unknown")}}, atom("eq", "Alpha"), false},
		{"reader unsupported sibling", atom("eq", "Alpha"), &record.RecordScopeExpression{Operator: "or", Children: []record.RecordScopeExpression{*atom("eq", "Alpha"), *atom("unknown")}}, false},
		{"malformed NOT", not(atom("eq", "Alpha")), &record.RecordScopeExpression{Operator: "not"}, false},
		{"relation existence in unused branch", &record.RecordScopeExpression{Operator: "and", Children: []record.RecordScopeExpression{*atom("eq", "Alpha"), {Operator: "in", FieldKey: "id", Values: []string{"id"}, RelationExists: true}}}, atom("eq", "Alpha"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if actual := sharedRecordScopeCovered(tc.source, tc.reader, 0); actual != tc.allow {
				t.Fatal("scope coverage", actual, "want", tc.allow)
			}
		})
	}
}
