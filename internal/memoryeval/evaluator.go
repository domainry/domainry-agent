package memoryeval

import (
	"fmt"
	"math"
	"strings"
)

type Benchmark struct {
	Cases []Case `json:"cases"`
}

type Case struct {
	ID                     string        `json:"id"`
	ExpectedMemoryIDs      []string      `json:"expected_memory_ids,omitempty"`
	SelectedMemoryIDs      []string      `json:"selected_memory_ids,omitempty"`
	ForbiddenMemoryIDs     []string      `json:"forbidden_memory_ids,omitempty"`
	ExpectedCandidateFacts []string      `json:"expected_candidate_facts,omitempty"`
	ProposedCandidateFacts []string      `json:"proposed_candidate_facts,omitempty"`
	SourceChecks           []SourceCheck `json:"source_checks,omitempty"`
	AnswerScores           AnswerScores  `json:"answer_scores"`
}

type SourceCheck struct {
	SourceID string `json:"source_id"`
	Correct  bool   `json:"correct"`
}

type AnswerScores struct {
	NoMemory *float64 `json:"no_memory,omitempty"`
	Domainry *float64 `json:"domainry,omitempty"`
	Oracle   *float64 `json:"oracle,omitempty"`
	ChatGPT  *float64 `json:"chatgpt,omitempty"`
}

type Report struct {
	CaseCount             int      `json:"case_count"`
	RetrievalPrecision    float64  `json:"retrieval_precision"`
	RetrievalRecall       float64  `json:"retrieval_recall"`
	CandidatePrecision    float64  `json:"candidate_precision"`
	CandidateRecall       float64  `json:"candidate_recall"`
	SourceAccuracy        float64  `json:"source_accuracy"`
	ForbiddenSelections   int      `json:"forbidden_selections"`
	DomainryEffectiveness float64  `json:"domainry_effectiveness"`
	ChatGPTEffectiveness  float64  `json:"chatgpt_effectiveness,omitempty"`
	EffectivenessGap      float64  `json:"effectiveness_gap,omitempty"`
	Passed                bool     `json:"passed"`
	Failures              []string `json:"failures,omitempty"`
}

type counts struct {
	retrievalTrue, retrievalSelected, retrievalExpected int
	candidateTrue, candidateProposed, candidateExpected int
	sourceCorrect, sourceTotal                          int
	forbidden                                           int
	domainryEffect, chatgptEffect                       float64
	domainryEffectCases, chatgptEffectCases             int
}

func Evaluate(benchmark Benchmark) (Report, error) {
	if len(benchmark.Cases) == 0 {
		return Report{}, fmt.Errorf("memory benchmark requires at least one case")
	}
	var total counts
	seenCases := map[string]bool{}
	for _, test := range benchmark.Cases {
		test.ID = strings.TrimSpace(test.ID)
		if test.ID == "" || seenCases[test.ID] {
			return Report{}, fmt.Errorf("memory benchmark case ID is empty or duplicated")
		}
		seenCases[test.ID] = true
		expected, err := stringSet(test.ExpectedMemoryIDs)
		if err != nil {
			return Report{}, fmt.Errorf("case %s expected memory IDs: %w", test.ID, err)
		}
		selected, err := stringSet(test.SelectedMemoryIDs)
		if err != nil {
			return Report{}, fmt.Errorf("case %s selected memory IDs: %w", test.ID, err)
		}
		forbidden, err := stringSet(test.ForbiddenMemoryIDs)
		if err != nil {
			return Report{}, fmt.Errorf("case %s forbidden memory IDs: %w", test.ID, err)
		}
		total.retrievalExpected += len(expected)
		total.retrievalSelected += len(selected)
		for id := range selected {
			if expected[id] {
				total.retrievalTrue++
			}
			if forbidden[id] {
				total.forbidden++
			}
		}

		expectedCandidates, err := stringSet(test.ExpectedCandidateFacts)
		if err != nil {
			return Report{}, fmt.Errorf("case %s expected candidates: %w", test.ID, err)
		}
		proposedCandidates, err := stringSet(test.ProposedCandidateFacts)
		if err != nil {
			return Report{}, fmt.Errorf("case %s proposed candidates: %w", test.ID, err)
		}
		total.candidateExpected += len(expectedCandidates)
		total.candidateProposed += len(proposedCandidates)
		for fact := range proposedCandidates {
			if expectedCandidates[fact] {
				total.candidateTrue++
			}
		}
		for _, source := range test.SourceChecks {
			if strings.TrimSpace(source.SourceID) == "" {
				return Report{}, fmt.Errorf("case %s has an empty source check", test.ID)
			}
			total.sourceTotal++
			if source.Correct {
				total.sourceCorrect++
			}
		}
		if err := validateAnswerScores(test.AnswerScores); err != nil {
			return Report{}, fmt.Errorf("case %s answer scores: %w", test.ID, err)
		}
		if score, ok := normalizedEffectiveness(test.AnswerScores.NoMemory, test.AnswerScores.Domainry, test.AnswerScores.Oracle); ok {
			total.domainryEffect += score
			total.domainryEffectCases++
		}
		if score, ok := normalizedEffectiveness(test.AnswerScores.NoMemory, test.AnswerScores.ChatGPT, test.AnswerScores.Oracle); ok {
			total.chatgptEffect += score
			total.chatgptEffectCases++
		}
	}
	report := Report{
		CaseCount:             len(benchmark.Cases),
		RetrievalPrecision:    ratio(total.retrievalTrue, total.retrievalSelected),
		RetrievalRecall:       ratio(total.retrievalTrue, total.retrievalExpected),
		CandidatePrecision:    ratio(total.candidateTrue, total.candidateProposed),
		CandidateRecall:       ratio(total.candidateTrue, total.candidateExpected),
		SourceAccuracy:        ratio(total.sourceCorrect, total.sourceTotal),
		ForbiddenSelections:   total.forbidden,
		DomainryEffectiveness: average(total.domainryEffect, total.domainryEffectCases),
		ChatGPTEffectiveness:  average(total.chatgptEffect, total.chatgptEffectCases),
	}
	if total.domainryEffectCases != 0 && total.chatgptEffectCases != 0 {
		report.EffectivenessGap = report.ChatGPTEffectiveness - report.DomainryEffectiveness
	}
	report.Failures = qualityFailures(report, total)
	report.Passed = len(report.Failures) == 0
	return report, nil
}

func qualityFailures(report Report, total counts) []string {
	failures := []string{}
	if report.ForbiddenSelections != 0 {
		failures = append(failures, "forbidden memory selection must be zero")
	}
	if total.retrievalExpected == 0 || report.RetrievalRecall < 0.85 {
		failures = append(failures, "retrieval recall must be at least 0.85")
	}
	if total.retrievalSelected == 0 || report.RetrievalPrecision < 0.75 {
		failures = append(failures, "retrieval precision must be at least 0.75")
	}
	if total.candidateExpected == 0 || report.CandidateRecall < 0.80 {
		failures = append(failures, "candidate recall must be at least 0.80")
	}
	if total.candidateProposed == 0 || report.CandidatePrecision < 0.95 {
		failures = append(failures, "candidate precision must be at least 0.95")
	}
	if total.sourceTotal == 0 || report.SourceAccuracy < 0.98 {
		failures = append(failures, "source accuracy must be at least 0.98")
	}
	if total.domainryEffectCases == 0 || report.DomainryEffectiveness < 0.75 {
		failures = append(failures, "normalized answer effectiveness must be at least 0.75")
	}
	return failures
}

func stringSet(values []string) (map[string]bool, error) {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || out[value] {
			return nil, fmt.Errorf("value is empty or duplicated")
		}
		out[value] = true
	}
	return out, nil
}

func validateAnswerScores(scores AnswerScores) error {
	for _, score := range []*float64{scores.NoMemory, scores.Domainry, scores.Oracle, scores.ChatGPT} {
		if score != nil && (math.IsNaN(*score) || math.IsInf(*score, 0) || *score < 0 || *score > 1) {
			return fmt.Errorf("score must be between 0 and 1")
		}
	}
	if scores.NoMemory != nil && scores.Oracle != nil && *scores.Oracle <= *scores.NoMemory {
		return fmt.Errorf("oracle score must exceed the no-memory score")
	}
	return nil
}

func normalizedEffectiveness(baseline, actual, oracle *float64) (float64, bool) {
	if baseline == nil || actual == nil || oracle == nil || *oracle <= *baseline {
		return 0, false
	}
	value := (*actual - *baseline) / (*oracle - *baseline)
	return min(1, max(0, value)), true
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func average(total float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return total / float64(count)
}
