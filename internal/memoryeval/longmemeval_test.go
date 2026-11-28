package memoryeval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunLongMemEvalUsesProductionSelectorAndSkipsAbstention(t *testing.T) {
	dataset := []map[string]any{
		{
			"question_id": "question-one", "question_type": "single-session-user", "question": "What degree did I graduate with?", "answer": "Business Administration", "question_date": "2023/05/30 (Tue) 23:40",
			"haystack_session_ids": []string{"decoy", "answer-session"}, "haystack_dates": []string{"2023/05/01 (Mon) 10:00", "2023/05/20 (Sat) 12:00"}, "answer_session_ids": []string{"answer-session"},
			"haystack_sessions": []any{
				[]map[string]any{{"role": "user", "content": "I planted tomatoes."}, {"role": "assistant", "content": "That sounds good."}},
				[]map[string]any{{"role": "user", "content": "I graduated with a degree in Business Administration."}, {"role": "assistant", "content": "Congratulations."}},
			},
		},
		{
			"question_id": "question_abs", "question_type": "single-session-user", "question": "What never happened?", "answer": "I don't know", "question_date": "2023/05/30 (Tue) 23:40",
			"haystack_session_ids": []string{"decoy"}, "haystack_dates": []string{"2023/05/01 (Mon) 10:00"}, "answer_session_ids": []string{},
			"haystack_sessions": []any{[]map[string]any{{"role": "user", "content": "I planted tomatoes."}}},
		},
	}
	raw, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "longmemeval.json")
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := RunLongMemEval(t.Context(), LongMemEvalConfig{InputPath: path, TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.DatasetCases != 2 || report.EvaluatedCases != 1 || report.SkippedAbstention != 1 || len(report.Modes) != len(longMemEvalModes) || len(report.DatasetSHA256) != 64 {
		t.Fatalf("report metadata=%+v", report)
	}
	for _, mode := range report.Modes {
		if mode.Metrics["@1"].RecallAny != 1 || mode.ByQuestionType["single-session-user"].RecallAny != 1 {
			t.Fatalf("mode did not retrieve the answer: %+v", mode)
		}
	}
	if len(report.Cases) != 0 {
		t.Fatalf("summary report unexpectedly contains case details: %+v", report.Cases)
	}
	detailed, err := RunLongMemEval(t.Context(), LongMemEvalConfig{InputPath: path, TopK: 10, IncludeCases: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(detailed.Cases) != 1 || detailed.Cases[0].QuestionID != "question-one" || len(detailed.Cases[0].Modes) != len(longMemEvalModes) {
		t.Fatalf("detailed report cases=%+v", detailed.Cases)
	}
}

func TestLongMemEvalBM25RanksMatchingDocument(t *testing.T) {
	scores := longMemEvalBM25("Where did the production workload run?", []string{"The garden has tomatoes", "Production workloads execute in Tokyo"})
	if len(scores) != 2 || scores[1] <= scores[0] || scores[1] <= 0 {
		t.Fatalf("scores=%v", scores)
	}
}

func TestLongMemEvalRetrievalQueryPreservesOriginalQuestion(t *testing.T) {
	question := "What was my previous occupation?"
	query, err := longMemEvalRetrievalQuery(question, &longMemEvalSemanticCase{RetrievalQuery: question + "\nSearch paraphrase: previous job role position"})
	if err != nil || !strings.Contains(query, "job role") {
		t.Fatalf("query=%q err=%v", query, err)
	}
	if _, err = longMemEvalRetrievalQuery(question, &longMemEvalSemanticCase{RetrievalQuery: "What job did I have?"}); err == nil {
		t.Fatal("expected replacement query to be rejected")
	}
}

func TestLongMemEvalDefaultUsesProductionLiteralHistoryBeforeRecentFallback(t *testing.T) {
	sessions := make([]any, 66)
	ids := make([]string, 66)
	dates := make([]string, 66)
	for index := range sessions {
		content := "This is an unrelated recent conversation."
		if index == 0 {
			content = "The launch codename is Zanzibar."
		}
		sessions[index] = []map[string]any{{"role": "user", "content": content}}
		ids[index] = "session-" + string(rune('a'+index))
		dates[index] = "2023/05/20 (Sat) 02:21"
	}
	dataset := []map[string]any{{
		"question_id": "literal-before-recent", "question_type": "single-session-user", "question": "What was the Zanzibar launch codename?", "answer": "Zanzibar", "question_date": "2023/05/30 (Tue) 23:40",
		"haystack_session_ids": ids, "haystack_dates": dates, "answer_session_ids": []string{ids[0]}, "haystack_sessions": sessions,
	}}
	raw, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "longmemeval.json")
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := RunLongMemEval(t.Context(), LongMemEvalConfig{InputPath: path, TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	var current LongMemEvalModeReport
	for _, mode := range report.Modes {
		if mode.Mode == LongMemEvalModeDefaultHistory {
			current = mode
		}
	}
	if current.CandidateRecallAny != 1 || current.Metrics["@10"].RecallAny != 1 {
		t.Fatalf("production literal history did not recover evidence outside the recent window: %+v", current)
	}
}

func TestLongMemEvalRetrievalIsIndependentOfGoldLabels(t *testing.T) {
	base := map[string]any{
		"question_id": "gold-isolation", "question_type": "single-session-user", "question": "Where does the production workload run?", "answer": "Tokyo", "question_date": "2023/05/30 (Tue) 23:40",
		"haystack_session_ids": []string{"garden", "production"}, "haystack_dates": []string{"2023/05/01 (Mon) 10:00", "2023/05/20 (Sat) 12:00"},
		"haystack_sessions": []any{
			[]map[string]any{{"role": "user", "content": "I planted tomatoes.", "has_answer": true}},
			[]map[string]any{{"role": "assistant", "content": "Production workloads execute in Tokyo.", "has_answer": false}},
		},
	}
	run := func(answerID string) LongMemEvalCaseResult {
		fixture := make(map[string]any, len(base)+1)
		for key, value := range base {
			fixture[key] = value
		}
		fixture["answer_session_ids"] = []string{answerID}
		raw, err := json.Marshal([]map[string]any{fixture})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "longmemeval.json")
		if err = os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		report, err := RunLongMemEval(t.Context(), LongMemEvalConfig{InputPath: path, TopK: 10, IncludeCases: true})
		if err != nil {
			t.Fatal(err)
		}
		return report.Cases[0]
	}
	first, second := run("garden"), run("production")
	for mode, firstResult := range first.Modes {
		secondResult := second.Modes[mode]
		if !reflect.DeepEqual(firstResult.SelectedSessionIDs, secondResult.SelectedSessionIDs) {
			t.Fatalf("mode %s changed retrieval after only gold labels changed: first=%v second=%v", mode, firstResult.SelectedSessionIDs, secondResult.SelectedSessionIDs)
		}
	}
}

func TestLongMemEvalRejectsUnexpectedDatasetHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "longmemeval.json")
	if err := os.WriteFile(path, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := RunLongMemEval(t.Context(), LongMemEvalConfig{InputPath: path, ExpectedSHA256: "deadbeef", TopK: 10})
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLongMemEvalSemanticCacheIsContentBoundAndGoldIsolated(t *testing.T) {
	dataset := []map[string]any{{
		"question_id": "semantic", "question_type": "single-session-assistant", "question": "Where does the production workload run?", "answer": "Tokyo", "question_date": "2023/05/30 (Tue) 23:40",
		"haystack_session_ids": []string{"garden", "production"}, "haystack_dates": []string{"2023/05/01 (Mon) 10:00", "2023/05/20 (Sat) 12:00"}, "answer_session_ids": []string{"production"},
		"haystack_sessions": []any{
			[]map[string]any{{"role": "user", "content": "I planted tomatoes."}},
			[]map[string]any{{"role": "assistant", "content": "Production workloads execute in Tokyo."}},
		},
	}}
	raw, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	datasetPath := filepath.Join(directory, "longmemeval.json")
	if err = os.WriteFile(datasetPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var decoded []longMemEvalCase
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	input := longMemEvalPublicInput(decoded[0])
	datasetHash, _, err := hashLongMemEvalFile(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	cache := longMemEvalSemanticCache{
		Format: "domainry-longmemeval-semantic-v1", DatasetSHA256: datasetHash, Model: "test-embedder", ModelRevision: "revision",
		GoldFieldsDropped: []string{"answer", "answer_session_ids", "has_answer", "question_id", "question_type"},
		Cases: []longMemEvalSemanticCase{{QuestionSHA256: longMemEvalTextHash(input.Question), Sessions: []longMemEvalSemanticSession{
			{ContentSHA256: longMemEvalSessionHash(input.HaystackIDs[0], input.HaystackDates[0], input.HaystackSessions[0]), Score: 0.1, TurnScores: []float64{0.1}},
			{ContentSHA256: longMemEvalSessionHash(input.HaystackIDs[1], input.HaystackDates[1], input.HaystackSessions[1]), Score: 0.9, TurnScores: []float64{0.9}},
		}}},
	}
	cacheRaw, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(directory, "semantic.json")
	if err = os.WriteFile(cachePath, cacheRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := RunLongMemEval(t.Context(), LongMemEvalConfig{InputPath: datasetPath, ExpectedSHA256: datasetHash, SemanticCache: cachePath, TopK: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Modes) != len(longMemEvalModes)+3 || report.Modes[len(report.Modes)-3].Mode != LongMemEvalModeHybridSemantic || report.Modes[len(report.Modes)-2].Mode != LongMemEvalModeRRFSemantic || report.Modes[len(report.Modes)-1].Mode != LongMemEvalModePrecisionCoverage || report.Modes[len(report.Modes)-1].Metrics["@1"].RecallAny != 1 || !report.Integrity.DatasetHashVerified || report.Integrity.EmbeddingModel != "test-embedder" || report.Integrity.SemanticCacheSHA256 == "" {
		t.Fatalf("semantic report=%+v", report)
	}
	cache.Cases[0].Sessions[0].ContentSHA256 = "tampered"
	cacheRaw, err = json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cachePath, cacheRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = RunLongMemEval(t.Context(), LongMemEvalConfig{InputPath: datasetPath, SemanticCache: cachePath, TopK: 10}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered semantic cache error=%v", err)
	}
}

func TestLongMemEvalPrecisionCoverageUsesIndependentLexicalBlocks(t *testing.T) {
	test := longMemEvalRetrievalInput{
		Question:      "What was the Zanzibar launch codename?",
		QuestionDate:  "2023/05/30 (Tue) 23:40",
		HaystackIDs:   []string{"lexical", "semantic-one", "semantic-two", "semantic-three", "other-one", "other-two"},
		HaystackDates: []string{"2023/05/01 (Mon) 10:00", "2023/05/02 (Tue) 10:00", "2023/05/03 (Wed) 10:00", "2023/05/04 (Thu) 10:00", "2023/05/05 (Fri) 10:00", "2023/05/06 (Sat) 10:00"},
		HaystackSessions: [][]longMemEvalTurn{
			{{Role: "user", Content: "The Zanzibar launch codename was recorded here."}},
			{{Role: "user", Content: "Unrelated semantic candidate one."}},
			{{Role: "user", Content: "Unrelated semantic candidate two."}},
			{{Role: "user", Content: "Unrelated semantic candidate three."}},
			{{Role: "user", Content: "Other history one."}},
			{{Role: "user", Content: "Other history two."}},
		},
	}
	semantic := &longMemEvalSemanticCase{QuestionSHA256: longMemEvalTextHash(test.Question)}
	for index := range test.HaystackIDs {
		semantic.Sessions = append(semantic.Sessions, longMemEvalSemanticSession{
			ContentSHA256: longMemEvalSessionHash(test.HaystackIDs[index], test.HaystackDates[index], test.HaystackSessions[index]),
			Score:         []float64{0.1, 0.8, 0.9, 1, 0.3, 0.2}[index],
			TurnScores:    []float64{1},
		})
	}
	items, err := longMemEvalPrecisionCoverageItems(test, semantic, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"semantic-three", "semantic-two", "semantic-one", "lexical"}
	if len(items) != len(want) {
		t.Fatalf("items=%+v", items)
	}
	for index := range want {
		if items[index].Source.SourceID != want[index] {
			t.Fatalf("rank %d source=%q want=%q", index+1, items[index].Source.SourceID, want[index])
		}
		if index > 0 && items[index].Relevance >= items[index-1].Relevance {
			t.Fatalf("relevance is not strictly descending: %+v", items)
		}
	}
	if !strings.Contains(items[3].Content, "Zanzibar") {
		t.Fatalf("lexical coverage item lost its own evidence projection: %q", items[3].Content)
	}
}
