package application

import (
	"strings"
	"testing"
)

func TestConversationCalculationDecimalAndCalendarSemantics(t *testing.T) {
	for _, test := range []struct {
		name    string
		input   calculationInput
		want    string
		rounded bool
	}{
		{"decimal addition", calculationInput{Operation: "expression", Expression: "0.1+0.2"}, "0.30", false},
		{"exact large integer", calculationInput{Operation: "expression", Expression: "9007199254740993+1"}, "9007199254740994.00", false},
		{"precedence and percent", calculationInput{Operation: "expression", Expression: "(120-20)*15%"}, "15.00", false},
		{"bankers tie down", calculationInput{Operation: "expression", Expression: "2.345"}, "2.34", true},
		{"bankers tie up", calculationInput{Operation: "expression", Expression: "2.355"}, "2.36", true},
		{"negative half up", calculationInput{Operation: "expression", Expression: "-2.345", Rounding: "half_up"}, "-2.35", true},
		{"truncate toward zero", calculationInput{Operation: "expression", Expression: "-2.359", Rounding: "toward_zero"}, "-2.35", true},
		{"mean exact", calculationInput{Operation: "mean", Values: []string{"0.1", "0.2", "0.3"}}, "0.20", false},
		{"leap day", calculationInput{Operation: "date_interval", Start: "2024-02-28", End: "2024-03-01"}, "2.00", false},
		{"DST instants", calculationInput{Operation: "date_interval", Start: "2026-03-08T00:00:00-05:00", End: "2026-03-09T00:00:00-04:00"}, "82800.00", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := calculateConversation(test.input)
			if err != nil || out["value"] != test.want || out["rounded"] != test.rounded {
				t.Fatalf("calculation=%v err=%v", out, err)
			}
		})
	}
}

func TestConversationCalculationRejectsCodeAmbiguityAndUnboundedInputs(t *testing.T) {
	for _, expression := range []string{"1/0", "1e6", "1;time.Now()", "process.exit()", "1<<32", "NaN", "1 2", "1..2", strings.Repeat("(", 34) + "1" + strings.Repeat(")", 34), strings.Repeat("1+", 300) + "1", strings.Repeat("9", 129)} {
		if _, err := calculateConversation(calculationInput{Operation: "expression", Expression: expression}); err == nil {
			t.Errorf("accepted %q", expression)
		}
	}
	for _, in := range []calculationInput{{Operation: "expression", Expression: "1+2", Values: []string{"9"}}, {Operation: "date_interval", Start: "03/04/2026", End: "05/04/2026"}, {Operation: "date_interval", Start: "2026-02-01", End: "2026-02-02T00:00:00Z"}, {Operation: "date_interval", Start: "2026-02-01", End: "2026-02-02", Unit: "hours"}, {Operation: "mean", Values: nil}} {
		if _, err := calculateConversation(in); err == nil {
			t.Errorf("accepted ambiguous input %+v", in)
		}
	}
}
