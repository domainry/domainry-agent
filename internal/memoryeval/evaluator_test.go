package memoryeval

import (
	"math"
	"testing"
)

func score(value float64) *float64 { return &value }

func TestEvaluateMemoryQualityAndChatGPTGap(t *testing.T) {
	report, err := Evaluate(Benchmark{Cases: []Case{
		{
			ID: "preference", ExpectedMemoryIDs: []string{"preference"}, SelectedMemoryIDs: []string{"preference"}, ForbiddenMemoryIDs: []string{"other-user"},
			ExpectedCandidateFacts: []string{"prefers concise Chinese"}, ProposedCandidateFacts: []string{"prefers concise Chinese"},
			SourceChecks: []SourceCheck{{SourceID: "conversation-a", Correct: true}},
			AnswerScores: AnswerScores{NoMemory: score(0.4), Domainry: score(0.85), Oracle: score(0.9), ChatGPT: score(0.88)},
		},
	}})
	if err != nil || !report.Passed || report.RetrievalPrecision != 1 || report.RetrievalRecall != 1 || report.CandidatePrecision != 1 || report.SourceAccuracy != 1 || math.Abs(report.DomainryEffectiveness-0.9) > 1e-9 || report.EffectivenessGap <= 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestEvaluateMemoryQualityFailsClosedOnLeakAndWeakRecall(t *testing.T) {
	report, err := Evaluate(Benchmark{Cases: []Case{
		{
			ID: "isolation", ExpectedMemoryIDs: []string{"allowed"}, SelectedMemoryIDs: []string{"forbidden"}, ForbiddenMemoryIDs: []string{"forbidden"},
			ExpectedCandidateFacts: []string{"stable fact"}, ProposedCandidateFacts: []string{"transient guess"},
			SourceChecks: []SourceCheck{{SourceID: "wrong", Correct: false}},
			AnswerScores: AnswerScores{NoMemory: score(0.4), Domainry: score(0.5), Oracle: score(0.9)},
		},
	}})
	if err != nil || report.Passed || report.ForbiddenSelections != 1 || len(report.Failures) < 5 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}
