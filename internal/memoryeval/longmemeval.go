package memoryeval

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
)

const (
	LongMemEvalModeDefaultHistory    = "domainry_default_history"
	LongMemEvalModeFullLexical       = "domainry_full_history_lexical"
	LongMemEvalModeBM25Provider      = "domainry_bm25_provider"
	LongMemEvalModeHybridTurn        = "domainry_hybrid_turn_provider"
	LongMemEvalModeHybridSemantic    = "domainry_hybrid_semantic_provider"
	LongMemEvalModeRRFSemantic       = "domainry_rrf_semantic_provider"
	LongMemEvalModePrecisionCoverage = "domainry_precision_coverage_provider"
	LongMemEvalOfficialSHA256        = "d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442"
	longMemEvalAlgorithmVersion      = "domainry-longmemeval-v5"

	longMemEvalProvider = "longmemeval"
)

var (
	longMemEvalCutoffs = []int{1, 3, 5, 10}
	longMemEvalModes   = []string{LongMemEvalModeDefaultHistory, LongMemEvalModeFullLexical, LongMemEvalModeBM25Provider, LongMemEvalModeHybridTurn}
)

type LongMemEvalConfig struct {
	InputPath      string
	ExpectedSHA256 string
	SemanticCache  string
	Split          string
	Modes          []string
	SemanticWeight float64
	Limit          int
	TopK           int
	IncludeCases   bool
}

type LongMemEvalReport struct {
	Benchmark           string                  `json:"benchmark"`
	DatasetSource       string                  `json:"dataset_source"`
	DatasetSHA256       string                  `json:"dataset_sha256"`
	DatasetBytes        int64                   `json:"dataset_bytes"`
	DatasetCases        int                     `json:"dataset_cases"`
	EvaluatedCases      int                     `json:"evaluated_cases"`
	SkippedAbstention   int                     `json:"skipped_abstention"`
	SkippedSplit        int                     `json:"skipped_split"`
	Split               string                  `json:"split"`
	SemanticWeight      float64                 `json:"semantic_weight,omitempty"`
	TopK                int                     `json:"top_k"`
	Modes               []LongMemEvalModeReport `json:"modes"`
	StartedAt           time.Time               `json:"started_at"`
	DurationMillis      int64                   `json:"duration_millis"`
	ReproducibilityNote string                  `json:"reproducibility_note"`
	Integrity           LongMemEvalIntegrity    `json:"integrity"`
	Cases               []LongMemEvalCaseResult `json:"cases,omitempty"`
}

type LongMemEvalIntegrity struct {
	DatasetHashVerified     bool     `json:"dataset_hash_verified"`
	GoldInputIsolation      string   `json:"gold_input_isolation"`
	ModelAPIsUsed           bool     `json:"model_apis_used"`
	RetrievalInputs         []string `json:"retrieval_inputs"`
	SemanticCacheSHA256     string   `json:"semantic_cache_sha256,omitempty"`
	EmbeddingModel          string   `json:"embedding_model,omitempty"`
	EmbeddingRevision       string   `json:"embedding_revision,omitempty"`
	EmbeddingQueryPrefix    string   `json:"embedding_query_prefix,omitempty"`
	EmbeddingGranularity    string   `json:"embedding_granularity,omitempty"`
	PrefilterCacheSHA256    string   `json:"prefilter_cache_sha256,omitempty"`
	PrefilterSessions       int      `json:"prefilter_sessions,omitempty"`
	PrefilterTurns          int      `json:"prefilter_turns,omitempty"`
	CrossEncoderWeight      float64  `json:"cross_encoder_weight,omitempty"`
	SourceCacheSHA256       []string `json:"source_cache_sha256,omitempty"`
	QueryRewriteCacheSHA256 string   `json:"query_rewrite_cache_sha256,omitempty"`
	QueryRewriteModel       string   `json:"query_rewrite_model,omitempty"`
	QueryRewriteRevision    string   `json:"query_rewrite_revision,omitempty"`
	QueryRewriteMode        string   `json:"query_rewrite_request_mode,omitempty"`
	GoldFieldsDropped       []string `json:"semantic_cache_gold_fields_dropped,omitempty"`
	AlgorithmVersion        string   `json:"algorithm_version"`
	GoVersion               string   `json:"go_version"`
	GOOS                    string   `json:"goos"`
	GOARCH                  string   `json:"goarch"`
	ExecutableSHA256        string   `json:"executable_sha256,omitempty"`
	EnvironmentClean        bool     `json:"environment_clean"`
	EnvironmentKeys         []string `json:"environment_keys"`
}

type LongMemEvalModeReport struct {
	Mode                 string                               `json:"mode"`
	Description          string                               `json:"description"`
	Cases                int                                  `json:"cases"`
	CandidateRecallAny   float64                              `json:"candidate_recall_any"`
	CandidateRecallAll   float64                              `json:"candidate_recall_all"`
	AverageCandidates    float64                              `json:"average_candidates"`
	AverageSelectedItems float64                              `json:"average_selected_items"`
	AverageSelectedBytes float64                              `json:"average_selected_bytes"`
	LatencyP50Millis     float64                              `json:"latency_p50_millis"`
	LatencyP95Millis     float64                              `json:"latency_p95_millis"`
	Metrics              map[string]LongMemEvalRetrievalScore `json:"metrics"`
	ByQuestionType       map[string]LongMemEvalRetrievalScore `json:"by_question_type_at_10"`
}

type LongMemEvalRetrievalScore struct {
	RecallAny      float64 `json:"recall_any"`
	RecallAll      float64 `json:"recall_all"`
	EvidenceRecall float64 `json:"evidence_recall"`
	Precision      float64 `json:"precision"`
	MRR            float64 `json:"mrr"`
	NDCG           float64 `json:"ndcg"`
}

type LongMemEvalCaseResult struct {
	QuestionID       string                               `json:"question_id"`
	QuestionType     string                               `json:"question_type"`
	AnswerSessionIDs []string                             `json:"answer_session_ids"`
	Modes            map[string]LongMemEvalCaseModeResult `json:"modes"`
}

type LongMemEvalCaseModeResult struct {
	CandidateSessionCount int      `json:"candidate_session_count"`
	CandidateRecallAny    bool     `json:"candidate_recall_any"`
	CandidateRecallAll    bool     `json:"candidate_recall_all"`
	SelectedSessionIDs    []string `json:"selected_session_ids"`
	ProviderSessionIDs    []string `json:"provider_ranked_session_ids,omitempty"`
	SelectedItemCount     int      `json:"selected_item_count"`
	SelectedBytes         int      `json:"selected_bytes"`
	LatencyMicros         int64    `json:"latency_micros"`
	candidateSessionIDs   []string
}

type longMemEvalTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type longMemEvalCase struct {
	QuestionID       string              `json:"question_id"`
	QuestionType     string              `json:"question_type"`
	Question         string              `json:"question"`
	QuestionDate     string              `json:"question_date"`
	HaystackDates    []string            `json:"haystack_dates"`
	HaystackSessions [][]longMemEvalTurn `json:"haystack_sessions"`
	HaystackIDs      []string            `json:"haystack_session_ids"`
	AnswerSessionIDs []string            `json:"answer_session_ids"`
}

// longMemEvalRetrievalInput is the only value passed to retrieval code. Gold
// answers, has_answer turn labels and answer_session_ids are intentionally not
// representable here, so a retriever cannot consume them accidentally.
type longMemEvalRetrievalInput struct {
	Question         string
	QuestionDate     string
	HaystackDates    []string
	HaystackSessions [][]longMemEvalTurn
	HaystackIDs      []string
}

type longMemEvalAccumulator struct {
	description                              string
	cases, candidateAny, candidateAll        int
	candidates, selectedItems, selectedBytes int
	latencies                                []float64
	metrics                                  map[int]*longMemEvalMetricAccumulator
	byType                                   map[string]*longMemEvalMetricAccumulator
}

type longMemEvalMetricAccumulator struct {
	cases                int
	recallAny, recallAll float64
	evidenceRecall       float64
	precision            float64
	mrr                  float64
	ndcg                 float64
}

type longMemEvalSemanticCache struct {
	Format               string                    `json:"format"`
	DatasetSHA256        string                    `json:"dataset_sha256"`
	Model                string                    `json:"model"`
	ModelRevision        string                    `json:"model_revision"`
	QueryPrefix          string                    `json:"query_prefix,omitempty"`
	Granularity          string                    `json:"granularity,omitempty"`
	PrefilterSHA256      string                    `json:"prefilter_cache_sha256,omitempty"`
	PrefilterSessions    int                       `json:"prefilter_sessions,omitempty"`
	PrefilterTurns       int                       `json:"prefilter_turns,omitempty"`
	CrossWeight          float64                   `json:"cross_weight,omitempty"`
	SourceCacheSHA256    []string                  `json:"source_cache_sha256,omitempty"`
	QueryRewriteSHA256   string                    `json:"query_rewrite_cache_sha256,omitempty"`
	QueryRewriteModel    string                    `json:"query_rewrite_model,omitempty"`
	QueryRewriteRevision string                    `json:"query_rewrite_revision,omitempty"`
	QueryRewriteMode     string                    `json:"query_rewrite_request_mode,omitempty"`
	GoldFieldsDropped    []string                  `json:"gold_fields_dropped_during_json_decode"`
	Cases                []longMemEvalSemanticCase `json:"cases"`
	FileSHA256           string                    `json:"-"`
}

type longMemEvalSemanticCase struct {
	QuestionSHA256 string                       `json:"question_sha256"`
	RetrievalQuery string                       `json:"retrieval_query,omitempty"`
	Sessions       []longMemEvalSemanticSession `json:"sessions"`
}

type longMemEvalSemanticSession struct {
	ContentSHA256 string    `json:"content_sha256"`
	Score         float64   `json:"score"`
	TurnScores    []float64 `json:"turn_scores"`
}

func RunLongMemEval(ctx context.Context, config LongMemEvalConfig) (LongMemEvalReport, error) {
	if strings.TrimSpace(config.InputPath) == "" {
		return LongMemEvalReport{}, fmt.Errorf("LongMemEval input path is required")
	}
	if config.Limit < 0 {
		return LongMemEvalReport{}, fmt.Errorf("LongMemEval limit cannot be negative")
	}
	if config.Split == "" {
		config.Split = "all"
	}
	if config.Split != "all" && config.Split != "development" && config.Split != "holdout" {
		return LongMemEvalReport{}, fmt.Errorf("LongMemEval split must be all, development, or holdout")
	}
	if config.SemanticWeight == 0 {
		config.SemanticWeight = 0.5
	}
	if config.SemanticWeight < 0 || config.SemanticWeight > 1 {
		return LongMemEvalReport{}, fmt.Errorf("LongMemEval semantic weight must be between 0 and 1")
	}
	if config.TopK == 0 {
		config.TopK = 10
	}
	if config.TopK < 1 || config.TopK > agentsdk.AgentMemoryMaxItems {
		return LongMemEvalReport{}, fmt.Errorf("LongMemEval top-k must be between 1 and %d", agentsdk.AgentMemoryMaxItems)
	}
	started := time.Now().UTC()
	hash, size, err := hashLongMemEvalFile(config.InputPath)
	if err != nil {
		return LongMemEvalReport{}, err
	}
	if config.ExpectedSHA256 != "" && !strings.EqualFold(config.ExpectedSHA256, hash) {
		return LongMemEvalReport{}, fmt.Errorf("LongMemEval dataset SHA-256 mismatch: got %s, want %s", hash, strings.ToLower(config.ExpectedSHA256))
	}
	var semanticCache *longMemEvalSemanticCache
	if strings.TrimSpace(config.SemanticCache) != "" {
		semanticCache, err = loadLongMemEvalSemanticCache(config.SemanticCache, hash)
		if err != nil {
			return LongMemEvalReport{}, err
		}
	}
	file, err := os.Open(config.InputPath)
	if err != nil {
		return LongMemEvalReport{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReaderSize(file, 1<<20))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return LongMemEvalReport{}, fmt.Errorf("LongMemEval input must be a JSON array")
	}
	modes := map[string]*longMemEvalAccumulator{
		LongMemEvalModeDefaultHistory: newLongMemEvalAccumulator("Current built-in history provider: up to four paginated literal queries, then recent original user turns up to the 64-item rank budget; 256-byte excerpts and the production deterministic selector, without model-ranker scores."),
		LongMemEvalModeFullLexical:    newLongMemEvalAccumulator("All history sessions exposed by a source-owned provider, 4096-byte bounded projections, production lexical selector."),
		LongMemEvalModeBM25Provider:   newLongMemEvalAccumulator("All sessions searched by a deterministic source-owned BM25 provider; normalized provider relevance then passes through production policy gates and budgets."),
		LongMemEvalModeHybridTurn:     newLongMemEvalAccumulator("All user and assistant turns searched with a deterministic word, stem and character hybrid; session scores use both whole-session and best-turn evidence, while compact top-turn projections pass through production policy gates and budgets."),
	}
	modeOrder := append([]string(nil), longMemEvalModes...)
	if semanticCache != nil {
		semanticDescription := "The deterministic hybrid turn provider fused with a pinned semantic cache. Gold fields are removed while decoding cache inputs; scoring consumes only the frozen cache."
		if semanticCache.QueryRewriteMode != "" {
			semanticDescription = "The deterministic hybrid turn provider fused with a pinned, gold-isolated model-generated query/rerank cache. The clean scoring process receives no API credentials and consumes only the frozen cache."
		}
		modeOrder = append(modeOrder, LongMemEvalModeHybridSemantic, LongMemEvalModeRRFSemantic, LongMemEvalModePrecisionCoverage)
		modes[LongMemEvalModeHybridSemantic] = newLongMemEvalAccumulator(semanticDescription)
		modes[LongMemEvalModeRRFSemantic] = newLongMemEvalAccumulator("The deterministic hybrid turn and pinned semantic rankings fused by reciprocal rank fusion. Gold fields are removed while decoding cache inputs.")
		modes[LongMemEvalModePrecisionCoverage] = newLongMemEvalAccumulator("Fixed three-item blocks from the pinned semantic ranking and deterministic lexical ranking are interleaved for precision and independent coverage. The composition uses no question type or gold fields and passes through production policy gates and budgets.")
	}
	if len(config.Modes) != 0 {
		requested := map[string]bool{}
		for _, mode := range config.Modes {
			requested[strings.TrimSpace(mode)] = true
		}
		filtered := make([]string, 0, len(modeOrder))
		for _, mode := range modeOrder {
			if requested[mode] {
				filtered = append(filtered, mode)
				delete(requested, mode)
			}
		}
		if len(requested) != 0 || len(filtered) == 0 {
			return LongMemEvalReport{}, fmt.Errorf("LongMemEval requested an unavailable mode")
		}
		modeOrder = filtered
	}
	report := LongMemEvalReport{
		Benchmark: "LongMemEval-S retrieval", DatasetSource: "https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned",
		DatasetSHA256: hash, DatasetBytes: size, TopK: config.TopK, Split: config.Split, SemanticWeight: config.SemanticWeight, StartedAt: started,
		ReproducibilityNote: "Official abstention cases are excluded from retrieval scoring because they have no gold answer location. No LLM judge is used. Model-generated caches are content-addressed and created before the credential-free scoring process.",
		Integrity: LongMemEvalIntegrity{
			DatasetHashVerified: config.ExpectedSHA256 != "" && strings.EqualFold(config.ExpectedSHA256, hash),
			GoldInputIsolation:  "retrievers receive longMemEvalRetrievalInput, which cannot represent answer, has_answer, or answer_session_ids",
			ModelAPIsUsed:       false,
			RetrievalInputs:     []string{"question", "question_date", "haystack_session_ids", "haystack_dates", "turn_role", "turn_content"},
			AlgorithmVersion:    longMemEvalAlgorithmVersion,
			GoVersion:           runtime.Version(),
			GOOS:                runtime.GOOS,
			GOARCH:              runtime.GOARCH,
		},
		Cases: []LongMemEvalCaseResult{},
	}
	report.Integrity.ExecutableSHA256, report.Integrity.EnvironmentKeys, report.Integrity.EnvironmentClean = longMemEvalProcessIntegrity()
	if semanticCache != nil {
		report.Integrity.SemanticCacheSHA256 = semanticCache.FileSHA256
		report.Integrity.EmbeddingModel = semanticCache.Model
		report.Integrity.EmbeddingRevision = semanticCache.ModelRevision
		report.Integrity.EmbeddingQueryPrefix = semanticCache.QueryPrefix
		report.Integrity.EmbeddingGranularity = semanticCache.Granularity
		report.Integrity.PrefilterCacheSHA256 = semanticCache.PrefilterSHA256
		report.Integrity.PrefilterSessions = semanticCache.PrefilterSessions
		report.Integrity.PrefilterTurns = semanticCache.PrefilterTurns
		report.Integrity.CrossEncoderWeight = semanticCache.CrossWeight
		report.Integrity.SourceCacheSHA256 = append([]string(nil), semanticCache.SourceCacheSHA256...)
		report.Integrity.QueryRewriteCacheSHA256 = semanticCache.QueryRewriteSHA256
		report.Integrity.QueryRewriteModel = semanticCache.QueryRewriteModel
		report.Integrity.QueryRewriteRevision = semanticCache.QueryRewriteRevision
		report.Integrity.QueryRewriteMode = semanticCache.QueryRewriteMode
		report.Integrity.ModelAPIsUsed = semanticCache.QueryRewriteMode != ""
		if semanticCache.QueryRewriteMode != "" {
			report.Integrity.RetrievalInputs = append(report.Integrity.RetrievalInputs, "pinned_query_rerank_cache")
		}
		report.Integrity.GoldFieldsDropped = append([]string(nil), semanticCache.GoldFieldsDropped...)
	}
	for decoder.More() {
		var test longMemEvalCase
		if err = decoder.Decode(&test); err != nil {
			return LongMemEvalReport{}, fmt.Errorf("decode LongMemEval case %d: %w", report.DatasetCases, err)
		}
		report.DatasetCases++
		if strings.HasSuffix(test.QuestionID, "_abs") || len(test.AnswerSessionIDs) == 0 {
			report.SkippedAbstention++
			continue
		}
		if !longMemEvalInSplit(test.Question, config.Split) {
			report.SkippedSplit++
			continue
		}
		if config.Limit > 0 && report.EvaluatedCases >= config.Limit {
			continue
		}
		if err = validateLongMemEvalCase(test); err != nil {
			return LongMemEvalReport{}, fmt.Errorf("LongMemEval case %s: %w", test.QuestionID, err)
		}
		retrievalInput := longMemEvalPublicInput(test)
		var semanticCase *longMemEvalSemanticCase
		if semanticCache != nil {
			if report.DatasetCases > len(semanticCache.Cases) {
				return LongMemEvalReport{}, fmt.Errorf("LongMemEval semantic cache has fewer cases than the dataset")
			}
			semanticCase = &semanticCache.Cases[report.DatasetCases-1]
		}
		caseResult := LongMemEvalCaseResult{}
		if config.IncludeCases {
			caseResult = LongMemEvalCaseResult{QuestionID: test.QuestionID, QuestionType: test.QuestionType, AnswerSessionIDs: append([]string(nil), test.AnswerSessionIDs...), Modes: map[string]LongMemEvalCaseModeResult{}}
		}
		for _, mode := range modeOrder {
			result, runErr := runLongMemEvalCase(ctx, retrievalInput, semanticCase, mode, config.TopK, config.SemanticWeight)
			if runErr != nil {
				return LongMemEvalReport{}, fmt.Errorf("LongMemEval case %s mode %s: %w", test.QuestionID, mode, runErr)
			}
			result.CandidateRecallAny, result.CandidateRecallAll = longMemEvalRecall(result.candidateSessionIDs, test.AnswerSessionIDs)
			if config.IncludeCases {
				caseResult.Modes[mode] = result
			}
			modes[mode].add(test.QuestionType, test.AnswerSessionIDs, result)
		}
		if config.IncludeCases {
			report.Cases = append(report.Cases, caseResult)
		}
		report.EvaluatedCases++
	}
	if _, err = decoder.Token(); err != nil {
		return LongMemEvalReport{}, fmt.Errorf("finish LongMemEval input: %w", err)
	}
	if semanticCache != nil && len(semanticCache.Cases) != report.DatasetCases {
		return LongMemEvalReport{}, fmt.Errorf("LongMemEval semantic cache case count %d does not match dataset case count %d", len(semanticCache.Cases), report.DatasetCases)
	}
	if report.EvaluatedCases == 0 {
		return LongMemEvalReport{}, fmt.Errorf("LongMemEval input contains no scored cases")
	}
	for _, mode := range modeOrder {
		report.Modes = append(report.Modes, modes[mode].report(mode))
	}
	report.DurationMillis = time.Since(started).Milliseconds()
	return report, nil
}

func longMemEvalInSplit(question, split string) bool {
	if split == "all" {
		return true
	}
	digest := sha256.Sum256([]byte(question))
	development := digest[0]%10 < 7
	return split == "development" && development || split == "holdout" && !development
}

func longMemEvalPublicInput(test longMemEvalCase) longMemEvalRetrievalInput {
	return longMemEvalRetrievalInput{
		Question: test.Question, QuestionDate: test.QuestionDate,
		HaystackDates: append([]string(nil), test.HaystackDates...), HaystackSessions: test.HaystackSessions, HaystackIDs: append([]string(nil), test.HaystackIDs...),
	}
}

func newLongMemEvalAccumulator(description string) *longMemEvalAccumulator {
	metrics := map[int]*longMemEvalMetricAccumulator{}
	for _, k := range longMemEvalCutoffs {
		metrics[k] = &longMemEvalMetricAccumulator{}
	}
	return &longMemEvalAccumulator{description: description, metrics: metrics, byType: map[string]*longMemEvalMetricAccumulator{}}
}

func validateLongMemEvalCase(test longMemEvalCase) error {
	if strings.TrimSpace(test.QuestionID) == "" || strings.TrimSpace(test.Question) == "" || strings.TrimSpace(test.QuestionType) == "" || len(test.HaystackIDs) == 0 || len(test.HaystackIDs) != len(test.HaystackSessions) || len(test.HaystackIDs) != len(test.HaystackDates) {
		return fmt.Errorf("invalid question or haystack shape")
	}
	for _, id := range test.HaystackIDs {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("empty session ID")
		}
	}
	for _, id := range test.AnswerSessionIDs {
		found := false
		for _, candidate := range test.HaystackIDs {
			found = found || candidate == id
		}
		if !found {
			return fmt.Errorf("answer session %q is absent from haystack", id)
		}
	}
	return nil
}

func runLongMemEvalCase(ctx context.Context, test longMemEvalRetrievalInput, semantic *longMemEvalSemanticCase, mode string, topK int, semanticWeight float64) (LongMemEvalCaseModeResult, error) {
	now := parseLongMemEvalDate(test.QuestionDate)
	providerKey, referenceHistory := longMemEvalProvider, false
	if mode == LongMemEvalModeDefaultHistory {
		providerKey, referenceHistory = agentsdk.AgentMemoryProviderHistory, true
	}
	request := agentsdk.MemoryContextRequest{
		Consumer: agentsdk.AgentMemoryConsumerTask, WorkspaceID: "longmemeval", UserID: "benchmark", RunID: "benchmark",
		Query: test.Question, Policy: agentsdk.AgentMemoryPolicy{ContractVersion: agentsdk.AgentMemoryPolicyVersion, Enabled: true, ReferenceHistory: referenceHistory, ProviderKeys: []string{providerKey}, RetrievalMode: agentsdk.AgentMemoryRetrievalHybrid, MaxItems: topK, MaxBytes: agentsdk.AgentMemoryMaxBytes},
	}
	started := time.Now()
	items, err := longMemEvalItems(ctx, test, semantic, mode, request, semanticWeight)
	if err != nil {
		return LongMemEvalCaseModeResult{}, err
	}
	candidateSessions := uniqueLongMemEvalSources(items)
	providerSessions := rankedLongMemEvalSources(items)
	snapshot, err := application.BuildMemoryContextSnapshotForEvaluation(ctx, request, []agentsdk.MemoryContextResult{{ProviderKey: providerKey, Revision: "dataset-98d7416c", Items: items}}, nil, now)
	latency := time.Since(started)
	if err != nil {
		return LongMemEvalCaseModeResult{}, err
	}
	selected := uniqueLongMemEvalSources(snapshot.Items)
	selectedBytes := 0
	for _, item := range snapshot.Items {
		selectedBytes += len(item.Content)
	}
	return LongMemEvalCaseModeResult{
		CandidateSessionCount: len(candidateSessions), candidateSessionIDs: candidateSessions,
		SelectedSessionIDs: selected, ProviderSessionIDs: providerSessions, SelectedItemCount: len(snapshot.Items), SelectedBytes: selectedBytes, LatencyMicros: latency.Microseconds(),
	}, nil
}

func longMemEvalItems(ctx context.Context, test longMemEvalRetrievalInput, semantic *longMemEvalSemanticCase, mode string, request agentsdk.MemoryContextRequest, semanticWeight float64) ([]agentsdk.MemoryContextItem, error) {
	if mode == LongMemEvalModeDefaultHistory {
		return longMemEvalDefaultHistoryItems(ctx, test, request)
	}
	if mode == LongMemEvalModeHybridTurn {
		return longMemEvalHybridTurnItems(test), nil
	}
	if mode == LongMemEvalModeHybridSemantic {
		return longMemEvalHybridSemanticItems(test, semantic, semanticWeight)
	}
	if mode == LongMemEvalModeRRFSemantic {
		return longMemEvalRRFSemanticItems(test, semantic, semanticWeight)
	}
	if mode == LongMemEvalModePrecisionCoverage {
		return longMemEvalPrecisionCoverageItems(test, semantic, semanticWeight, request.Policy.MaxItems)
	}
	items := make([]agentsdk.MemoryContextItem, 0, len(test.HaystackIDs))
	documents := make([]string, 0, len(test.HaystackIDs))
	for index, id := range test.HaystackIDs {
		content := longMemEvalSessionText(test.HaystackDates[index], test.HaystackSessions[index], 4096)
		documents = append(documents, content)
		itemID := "session_" + strconv.Itoa(index+1) + "_" + id
		items = append(items, longMemEvalItem(longMemEvalProvider, itemID, "1", "conversation_session", "History session on "+test.HaystackDates[index], content, id, parseLongMemEvalDate(test.HaystackDates[index])))
	}
	if mode == LongMemEvalModeBM25Provider {
		scores := longMemEvalBM25(test.Question, documents)
		maximum := 0.0
		for _, score := range scores {
			maximum = max(maximum, score)
		}
		if maximum > 0 {
			for index := range items {
				items[index].Relevance = scores[index] / maximum
				items[index].RelevanceSource = "provider"
			}
		}
	}
	return items, nil
}

func longMemEvalHybridTurnItems(test longMemEvalRetrievalInput) []agentsdk.MemoryContextItem {
	scores, turnScores := longMemEvalHybridScores(test.Question, test.HaystackSessions)
	maximum := 0.0
	for _, score := range scores {
		maximum = max(maximum, score)
	}
	items := make([]agentsdk.MemoryContextItem, 0, len(test.HaystackIDs))
	for index, id := range test.HaystackIDs {
		content := longMemEvalTurnProjection(test.HaystackDates[index], test.HaystackSessions[index], turnScores[index], 3072)
		itemID := "hybrid_session_" + strconv.Itoa(index+1) + "_" + id
		item := longMemEvalItem(longMemEvalProvider, itemID, "1", "conversation_session", "Relevant history on "+test.HaystackDates[index], content, id, parseLongMemEvalDate(test.HaystackDates[index]))
		if maximum > 0 {
			item.Relevance = scores[index] / maximum
			item.RelevanceSource = "provider"
		}
		items = append(items, item)
	}
	return items
}

func longMemEvalRetrievalQuery(question string, semantic *longMemEvalSemanticCase) (string, error) {
	if semantic == nil || semantic.RetrievalQuery == "" {
		return question, nil
	}
	if len(semantic.RetrievalQuery) > 1024 || !utf8.ValidString(semantic.RetrievalQuery) || !strings.HasPrefix(semantic.RetrievalQuery, question) {
		return "", fmt.Errorf("semantic cache retrieval query must preserve the original question as its prefix")
	}
	return semantic.RetrievalQuery, nil
}

func longMemEvalHybridSemanticItems(test longMemEvalRetrievalInput, semantic *longMemEvalSemanticCase, semanticWeight float64) ([]agentsdk.MemoryContextItem, error) {
	if semantic == nil || semantic.QuestionSHA256 != longMemEvalTextHash(test.Question) || len(semantic.Sessions) != len(test.HaystackSessions) {
		return nil, fmt.Errorf("semantic cache does not match the retrieval input")
	}
	retrievalQuery, err := longMemEvalRetrievalQuery(test.Question, semantic)
	if err != nil {
		return nil, err
	}
	lexicalScores, lexicalTurnScores := longMemEvalHybridScores(retrievalQuery, test.HaystackSessions)
	normalizeLongMemEval(lexicalScores)
	semanticScores := make([]float64, len(semantic.Sessions))
	combinedTurnScores := make([][]float64, len(semantic.Sessions))
	for sessionIndex, cached := range semantic.Sessions {
		if cached.ContentSHA256 != longMemEvalSessionHash(test.HaystackIDs[sessionIndex], test.HaystackDates[sessionIndex], test.HaystackSessions[sessionIndex]) || len(cached.TurnScores) != len(test.HaystackSessions[sessionIndex]) || math.IsNaN(cached.Score) || math.IsInf(cached.Score, 0) {
			return nil, fmt.Errorf("semantic cache session %d does not match the retrieval input", sessionIndex)
		}
		semanticScores[sessionIndex] = cached.Score
		combinedTurnScores[sessionIndex] = make([]float64, len(cached.TurnScores))
		semanticTurns := append([]float64(nil), cached.TurnScores...)
		for _, score := range semanticTurns {
			if math.IsNaN(score) || math.IsInf(score, 0) {
				return nil, fmt.Errorf("semantic cache session %d contains a non-finite turn score", sessionIndex)
			}
		}
		normalizeLongMemEvalRange(semanticTurns)
		for turnIndex := range semanticTurns {
			combinedTurnScores[sessionIndex][turnIndex] = (1-semanticWeight)*lexicalTurnScores[sessionIndex][turnIndex] + semanticWeight*semanticTurns[turnIndex]
		}
	}
	normalizeLongMemEvalRange(semanticScores)
	items := make([]agentsdk.MemoryContextItem, 0, len(test.HaystackIDs))
	maximum := 0.0
	combined := make([]float64, len(semanticScores))
	for index := range combined {
		combined[index] = (1-semanticWeight)*lexicalScores[index] + semanticWeight*semanticScores[index]
		maximum = max(maximum, combined[index])
	}
	for index, id := range test.HaystackIDs {
		content := longMemEvalTurnProjection(test.HaystackDates[index], test.HaystackSessions[index], combinedTurnScores[index], 3072)
		itemID := "semantic_session_" + strconv.Itoa(index+1) + "_" + id
		item := longMemEvalItem(longMemEvalProvider, itemID, "1", "conversation_session", "Relevant history on "+test.HaystackDates[index], content, id, parseLongMemEvalDate(test.HaystackDates[index]))
		if maximum > 0 {
			item.Relevance = combined[index] / maximum
			item.RelevanceSource = "provider"
		}
		items = append(items, item)
	}
	return items, nil
}

func longMemEvalRRFSemanticItems(test longMemEvalRetrievalInput, semantic *longMemEvalSemanticCase, semanticWeight float64) ([]agentsdk.MemoryContextItem, error) {
	if semantic == nil || semantic.QuestionSHA256 != longMemEvalTextHash(test.Question) || len(semantic.Sessions) != len(test.HaystackSessions) {
		return nil, fmt.Errorf("semantic cache does not match the retrieval input")
	}
	retrievalQuery, err := longMemEvalRetrievalQuery(test.Question, semantic)
	if err != nil {
		return nil, err
	}
	lexicalScores, lexicalTurnScores := longMemEvalHybridScores(retrievalQuery, test.HaystackSessions)
	semanticScores := make([]float64, len(semantic.Sessions))
	combinedTurnScores := make([][]float64, len(semantic.Sessions))
	for sessionIndex, cached := range semantic.Sessions {
		if cached.ContentSHA256 != longMemEvalSessionHash(test.HaystackIDs[sessionIndex], test.HaystackDates[sessionIndex], test.HaystackSessions[sessionIndex]) || len(cached.TurnScores) != len(test.HaystackSessions[sessionIndex]) || math.IsNaN(cached.Score) || math.IsInf(cached.Score, 0) {
			return nil, fmt.Errorf("semantic cache session %d does not match the retrieval input", sessionIndex)
		}
		semanticScores[sessionIndex] = cached.Score
		semanticTurns := append([]float64(nil), cached.TurnScores...)
		for _, score := range semanticTurns {
			if math.IsNaN(score) || math.IsInf(score, 0) {
				return nil, fmt.Errorf("semantic cache session %d contains a non-finite turn score", sessionIndex)
			}
		}
		combinedTurnScores[sessionIndex] = longMemEvalRRF(lexicalTurnScores[sessionIndex], semanticTurns, semanticWeight)
	}
	combined := longMemEvalRRF(lexicalScores, semanticScores, semanticWeight)
	maximum := 0.0
	for _, score := range combined {
		maximum = max(maximum, score)
	}
	items := make([]agentsdk.MemoryContextItem, 0, len(test.HaystackIDs))
	for index, id := range test.HaystackIDs {
		content := longMemEvalTurnProjection(test.HaystackDates[index], test.HaystackSessions[index], combinedTurnScores[index], 3072)
		itemID := "rrf_semantic_session_" + strconv.Itoa(index+1) + "_" + id
		item := longMemEvalItem(longMemEvalProvider, itemID, "1", "conversation_session", "Relevant history on "+test.HaystackDates[index], content, id, parseLongMemEvalDate(test.HaystackDates[index]))
		if maximum > 0 {
			item.Relevance = combined[index] / maximum
			item.RelevanceSource = "provider"
		}
		items = append(items, item)
	}
	return items, nil
}

func longMemEvalRRF(left, right []float64, rightWeight float64) []float64 {
	if len(left) != len(right) {
		return nil
	}
	rank := func(scores []float64) []int {
		positions := make([]int, len(scores))
		for index := range positions {
			positions[index] = index
		}
		sort.SliceStable(positions, func(i, j int) bool {
			if scores[positions[i]] != scores[positions[j]] {
				return scores[positions[i]] > scores[positions[j]]
			}
			return positions[i] < positions[j]
		})
		out := make([]int, len(scores))
		for position, index := range positions {
			out[index] = position + 1
		}
		return out
	}
	leftRanks, rightRanks := rank(left), rank(right)
	out := make([]float64, len(left))
	for index := range out {
		out[index] = (1-rightWeight)/(60+float64(leftRanks[index])) + rightWeight/(60+float64(rightRanks[index]))
	}
	return out
}

func longMemEvalPrecisionCoverageItems(test longMemEvalRetrievalInput, semantic *longMemEvalSemanticCase, semanticWeight float64, limit int) ([]agentsdk.MemoryContextItem, error) {
	precision, err := longMemEvalHybridSemanticItems(test, semantic, semanticWeight)
	if err != nil {
		return nil, err
	}
	coverage := longMemEvalHybridTurnItems(test)
	if len(precision) != len(coverage) {
		return nil, fmt.Errorf("precision and coverage rankings do not match")
	}
	ranked := func(items []agentsdk.MemoryContextItem) []int {
		positions := make([]int, len(items))
		for index := range positions {
			positions[index] = index
		}
		sort.SliceStable(positions, func(i, j int) bool {
			if items[positions[i]].Relevance != items[positions[j]].Relevance {
				return items[positions[i]].Relevance > items[positions[j]].Relevance
			}
			return positions[i] < positions[j]
		})
		return positions
	}
	precisionOrder, coverageOrder := ranked(precision), ranked(coverage)
	order := make([]int, 0, len(precision))
	fromPrecision := make([]bool, 0, len(precision))
	seen := make([]bool, len(precision))
	precisionCursor, coverageCursor := 0, 0
	const blockSize = 3
	appendBlock := func(source []int, cursor *int, semanticSource bool) {
		added := 0
		for *cursor < len(source) && added < blockSize {
			index := source[*cursor]
			*cursor++
			if seen[index] {
				continue
			}
			order = append(order, index)
			fromPrecision = append(fromPrecision, semanticSource)
			seen[index] = true
			added++
		}
	}
	for len(order) < len(precision) {
		before := len(order)
		appendBlock(precisionOrder, &precisionCursor, true)
		appendBlock(coverageOrder, &coverageCursor, false)
		if len(order) == before {
			break
		}
	}
	if limit <= 0 || limit > len(order) {
		limit = len(order)
	}
	out := make([]agentsdk.MemoryContextItem, 0, limit)
	for rank, index := range order[:limit] {
		item := coverage[index]
		if fromPrecision[rank] {
			item = precision[index]
		}
		item.Relevance = 1 - 0.5*float64(rank)/float64(max(1, limit-1))
		item.ID = "precision_coverage_" + strconv.Itoa(index+1) + "_" + test.HaystackIDs[index]
		item.RelevanceSource = "provider"
		out = append(out, item)
	}
	return out, nil
}

func loadLongMemEvalSemanticCache(path, datasetSHA256 string) (*longMemEvalSemanticCache, error) {
	hash, _, err := hashLongMemEvalFile(path)
	if err != nil {
		return nil, fmt.Errorf("hash LongMemEval semantic cache: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open LongMemEval semantic cache: %w", err)
	}
	defer file.Close()
	var cache longMemEvalSemanticCache
	decoder := json.NewDecoder(bufio.NewReaderSize(file, 1<<20))
	if err = decoder.Decode(&cache); err != nil {
		return nil, fmt.Errorf("decode LongMemEval semantic cache: %w", err)
	}
	expectedDropped := []string{"answer", "answer_session_ids", "has_answer", "question_id", "question_type"}
	validSourceHashes := true
	for _, digest := range cache.SourceCacheSHA256 {
		validSourceHashes = validSourceHashes && validLongMemEvalSHA256(digest)
	}
	validRewrite := cache.QueryRewriteSHA256 == "" && cache.QueryRewriteModel == "" && cache.QueryRewriteRevision == "" && cache.QueryRewriteMode == "" ||
		validLongMemEvalSHA256(cache.QueryRewriteSHA256) && strings.TrimSpace(cache.QueryRewriteModel) != "" && strings.TrimSpace(cache.QueryRewriteRevision) != "" && strings.TrimSpace(cache.QueryRewriteMode) != ""
	if cache.Format != "domainry-longmemeval-semantic-v1" || !strings.EqualFold(cache.DatasetSHA256, datasetSHA256) || strings.TrimSpace(cache.Model) == "" || strings.TrimSpace(cache.ModelRevision) == "" || !equalLongMemEvalStrings(cache.GoldFieldsDropped, expectedDropped) || len(cache.Cases) == 0 || cache.PrefilterSHA256 != "" && !validLongMemEvalSHA256(cache.PrefilterSHA256) || cache.PrefilterSessions < 0 || cache.PrefilterTurns < 0 || cache.CrossWeight < 0 || cache.CrossWeight > 1 || !validSourceHashes || !validRewrite {
		return nil, fmt.Errorf("LongMemEval semantic cache metadata is invalid")
	}
	cache.FileSHA256 = hash
	return &cache, nil
}

func validLongMemEvalSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func equalLongMemEvalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func longMemEvalTextHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func longMemEvalSessionHash(id, date string, turns []longMemEvalTurn) string {
	digest := sha256.New()
	for _, value := range []string{id, date} {
		_, _ = digest.Write([]byte(value))
		_, _ = digest.Write([]byte{0})
	}
	for _, turn := range turns {
		_, _ = digest.Write([]byte(turn.Role))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(turn.Content))
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func longMemEvalHybridScores(query string, sessions [][]longMemEvalTurn) ([]float64, [][]float64) {
	sessionWords := make([][]string, len(sessions))
	sessionCharacters := make([][]string, len(sessions))
	turnWords, turnCharacters := [][]string{}, [][]string{}
	turnSessions, turnPositions := []int{}, []int{}
	for sessionIndex, turns := range sessions {
		var full strings.Builder
		for turnIndex, turn := range turns {
			full.WriteString(turn.Role)
			full.WriteByte(' ')
			full.WriteString(turn.Content)
			full.WriteByte('\n')
			turnWords = append(turnWords, longMemEvalHybridWordTokens(turn.Content))
			turnCharacters = append(turnCharacters, longMemEvalCharacterTokens(turn.Content))
			turnSessions = append(turnSessions, sessionIndex)
			turnPositions = append(turnPositions, turnIndex)
		}
		sessionWords[sessionIndex] = longMemEvalHybridWordTokens(full.String())
		sessionCharacters[sessionIndex] = longMemEvalCharacterTokens(full.String())
	}
	queryWords := longMemEvalHybridWordTokens(query)
	queryCharacters := longMemEvalCharacterTokens(query)
	sessionWordScores := longMemEvalBM25TokenSets(queryWords, sessionWords)
	sessionCharacterScores := longMemEvalBM25TokenSets(queryCharacters, sessionCharacters)
	flatTurnWordScores := longMemEvalBM25TokenSets(queryWords, turnWords)
	flatTurnCharacterScores := longMemEvalBM25TokenSets(queryCharacters, turnCharacters)
	turnScores := make([][]float64, len(sessions))
	for index, turns := range sessions {
		turnScores[index] = make([]float64, len(turns))
	}
	normalizeLongMemEval(sessionWordScores)
	normalizeLongMemEval(sessionCharacterScores)
	normalizeLongMemEval(flatTurnWordScores)
	normalizeLongMemEval(flatTurnCharacterScores)
	bestTurns := make([]float64, len(sessions))
	for index := range flatTurnWordScores {
		score := 0.8*flatTurnWordScores[index] + 0.2*flatTurnCharacterScores[index]
		sessionIndex, turnIndex := turnSessions[index], turnPositions[index]
		turnScores[sessionIndex][turnIndex] = score
		bestTurns[sessionIndex] = max(bestTurns[sessionIndex], score)
	}
	scores := make([]float64, len(sessions))
	for index := range scores {
		scores[index] = 0.35*sessionWordScores[index] + 0.15*sessionCharacterScores[index] + 0.5*bestTurns[index]
	}
	return scores, turnScores
}

func longMemEvalTurnProjection(date string, turns []longMemEvalTurn, scores []float64, limit int) string {
	positions := make([]int, len(turns))
	for index := range positions {
		positions[index] = index
	}
	sort.SliceStable(positions, func(i, j int) bool {
		if scores[positions[i]] != scores[positions[j]] {
			return scores[positions[i]] > scores[positions[j]]
		}
		return positions[i] > positions[j]
	})
	if len(positions) > 3 {
		positions = positions[:3]
	}
	sort.Ints(positions)
	var builder strings.Builder
	builder.WriteString("Session date: ")
	builder.WriteString(date)
	for _, position := range positions {
		builder.WriteByte('\n')
		builder.WriteString(turns[position].Role)
		builder.WriteString(": ")
		builder.WriteString(truncateLongMemEval(turns[position].Content, 1024))
	}
	return truncateLongMemEval(builder.String(), limit)
}

func normalizeLongMemEval(scores []float64) {
	maximum := 0.0
	for _, score := range scores {
		maximum = max(maximum, score)
	}
	if maximum == 0 {
		return
	}
	for index := range scores {
		scores[index] /= maximum
	}
}

func normalizeLongMemEvalRange(scores []float64) {
	if len(scores) == 0 {
		return
	}
	minimum, maximum := scores[0], scores[0]
	for _, score := range scores[1:] {
		minimum, maximum = min(minimum, score), max(maximum, score)
	}
	if maximum == minimum {
		for index := range scores {
			scores[index] = 0
		}
		return
	}
	for index := range scores {
		scores[index] = (scores[index] - minimum) / (maximum - minimum)
	}
}

func longMemEvalDefaultHistoryItems(ctx context.Context, test longMemEvalRetrievalInput, request agentsdk.MemoryContextRequest) ([]agentsdk.MemoryContextItem, error) {
	repository := newLongMemEvalHistoryRepository(test)
	providers := application.NewDefaultMemoryContextProviders(repository, longMemEvalCandidateRanker{})
	for _, provider := range providers {
		result, err := provider.ResolveMemoryContext(ctx, request)
		if err != nil {
			return nil, err
		}
		if result.ProviderKey != agentsdk.AgentMemoryProviderHistory {
			continue
		}
		for index := range result.Items {
			if sessionID := repository.sessionByMessage[result.Items[index].Source.SourceID]; sessionID != "" {
				result.Items[index].Source.SourceID = sessionID
			}
		}
		return result.Items, nil
	}
	return nil, fmt.Errorf("production history memory provider is unavailable")
}

type longMemEvalHistoryRepository struct {
	hits             []agentsdk.ConversationHistoryHit
	contentByMessage map[string]string
	sessionByMessage map[string]string
}

func newLongMemEvalHistoryRepository(test longMemEvalRetrievalInput) *longMemEvalHistoryRepository {
	repository := &longMemEvalHistoryRepository{contentByMessage: map[string]string{}, sessionByMessage: map[string]string{}}
	sequence := int64(0)
	for sessionIndex, turns := range test.HaystackSessions {
		createdAt := parseLongMemEvalDate(test.HaystackDates[sessionIndex])
		for turnIndex, turn := range turns {
			sequence++
			messageID := "message_" + strconv.Itoa(sessionIndex+1) + "_" + strconv.Itoa(turnIndex+1)
			repository.contentByMessage[messageID] = turn.Content
			repository.sessionByMessage[messageID] = test.HaystackIDs[sessionIndex]
			repository.hits = append(repository.hits, agentsdk.ConversationHistoryHit{
				ConversationID: test.HaystackIDs[sessionIndex], MessageID: messageID, Seq: sequence, Role: turn.Role,
				Excerpt: truncateLongMemEval(turn.Content, 256), CreatedAt: createdAt.Add(time.Duration(turnIndex) * time.Millisecond),
			})
		}
	}
	for left, right := 0, len(repository.hits)-1; left < right; left, right = left+1, right-1 {
		repository.hits[left], repository.hits[right] = repository.hits[right], repository.hits[left]
	}
	return repository
}

func (r *longMemEvalHistoryRepository) Memories(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationMemory, error) {
	return nil, nil
}

func (r *longMemEvalHistoryRepository) SearchHistory(_ context.Context, search agentsdk.ConversationHistorySearch, _ agentsdk.ConversationAuthority) (agentsdk.ConversationHistorySearchResult, error) {
	offset := 0
	if search.Cursor != "" {
		parsed, err := strconv.Atoi(search.Cursor)
		if err != nil || parsed < 0 || parsed > len(r.hits) {
			return agentsdk.ConversationHistorySearchResult{}, fmt.Errorf("invalid benchmark history cursor")
		}
		offset = parsed
	}
	limit := search.Limit
	if limit <= 0 {
		limit = 10
	}
	needle := strings.ToLower(search.Query)
	result := agentsdk.ConversationHistorySearchResult{Items: []agentsdk.ConversationHistoryHit{}}
	index, scanned := offset, 0
	for index < len(r.hits) && scanned < 300 && len(result.Items) < limit {
		hit := r.hits[index]
		index++
		scanned++
		if search.ConversationID != "" && hit.ConversationID != search.ConversationID || !strings.Contains(strings.ToLower(r.contentByMessage[hit.MessageID]), needle) {
			continue
		}
		result.Items = append(result.Items, hit)
	}
	result.Complete = index == len(r.hits)
	if !result.Complete {
		result.NextCursor = strconv.Itoa(index)
	}
	return result, nil
}

func (r *longMemEvalHistoryRepository) HistoryMessage(context.Context, string, string, agentsdk.ConversationAuthority) (agentsdk.ConversationMessage, error) {
	return agentsdk.ConversationMessage{}, fmt.Errorf("benchmark history message reads are unsupported")
}

func (r *longMemEvalHistoryRepository) RecentMemoryHistory(_ context.Context, excludeConversationID string, limit int, _ agentsdk.ConversationAuthority) ([]agentsdk.ConversationHistoryHit, error) {
	out := make([]agentsdk.ConversationHistoryHit, 0, limit)
	for _, hit := range r.hits {
		if hit.ConversationID == excludeConversationID || hit.Role != "user" && hit.Role != "assistant" || strings.TrimSpace(hit.Excerpt) == "" {
			continue
		}
		out = append(out, hit)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

type longMemEvalCandidateRanker struct{}

func (longMemEvalCandidateRanker) RankMemoryContext(context.Context, agentsdk.MemoryContextRankRequest) (agentsdk.MemoryContextRankResult, error) {
	return agentsdk.MemoryContextRankResult{}, nil
}

func longMemEvalItem(providerKey, id, revision, kind, title, content, sourceID string, updatedAt time.Time) agentsdk.MemoryContextItem {
	return agentsdk.MemoryContextItem{
		ID: id, ProviderKey: providerKey, Revision: revision, Kind: kind, Scope: agentsdk.ConversationMemoryScopeWorkspace,
		Title: title, Content: content, Status: agentsdk.AgentMemoryStatusActive, Importance: 0.4, Confidence: 1, UpdatedAt: updatedAt,
		Source: agentsdk.AgentMemorySource{ProviderKey: providerKey, SourceID: sourceID, SourceRevision: revision, Kind: "conversation_session", CapturedAt: updatedAt},
	}
}

func longMemEvalSessionText(date string, turns []longMemEvalTurn, limit int) string {
	var builder strings.Builder
	builder.WriteString("Session date: ")
	builder.WriteString(date)
	for _, turn := range turns {
		builder.WriteByte('\n')
		builder.WriteString(turn.Role)
		builder.WriteString(": ")
		builder.WriteString(turn.Content)
		if builder.Len() >= limit {
			break
		}
	}
	return truncateLongMemEval(builder.String(), limit)
}

func truncateLongMemEval(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return strings.TrimSpace(value[:limit])
}

func parseLongMemEvalDate(value string) time.Time {
	for _, layout := range []string{"2006/01/02 (Mon) 15:04", "2006/01/02 (Mon)"} {
		if parsed, err := time.ParseInLocation(layout, strings.TrimSpace(value), time.UTC); err == nil {
			return parsed
		}
	}
	return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
}

func uniqueLongMemEvalSources(items []agentsdk.MemoryContextItem) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.Source.SourceID)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func rankedLongMemEvalSources(items []agentsdk.MemoryContextItem) []string {
	ranked := append([]agentsdk.MemoryContextItem(nil), items...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Relevance != ranked[j].Relevance {
			return ranked[i].Relevance > ranked[j].Relevance
		}
		return ranked[i].Source.SourceID < ranked[j].Source.SourceID
	})
	return uniqueLongMemEvalSources(ranked)
}

func longMemEvalRecall(selected, expected []string) (bool, bool) {
	found := make(map[string]bool, len(selected))
	for _, id := range selected {
		found[id] = true
	}
	any, all := false, len(expected) != 0
	for _, id := range expected {
		any = any || found[id]
		all = all && found[id]
	}
	return any, all
}

func (a *longMemEvalAccumulator) add(questionType string, expected []string, result LongMemEvalCaseModeResult) {
	a.cases++
	if result.CandidateRecallAny {
		a.candidateAny++
	}
	if result.CandidateRecallAll {
		a.candidateAll++
	}
	a.candidates += result.CandidateSessionCount
	a.selectedItems += result.SelectedItemCount
	a.selectedBytes += result.SelectedBytes
	a.latencies = append(a.latencies, float64(result.LatencyMicros)/1000)
	for _, k := range longMemEvalCutoffs {
		a.metrics[k].add(expected, result.SelectedSessionIDs, k)
	}
	if a.byType[questionType] == nil {
		a.byType[questionType] = &longMemEvalMetricAccumulator{}
	}
	a.byType[questionType].add(expected, result.SelectedSessionIDs, 10)
}

func (a *longMemEvalMetricAccumulator) add(expected, selected []string, k int) {
	a.cases++
	if len(selected) > k {
		selected = selected[:k]
	}
	any, all := longMemEvalRecall(selected, expected)
	if any {
		a.recallAny++
	}
	if all {
		a.recallAll++
	}
	expectedSet := make(map[string]bool, len(expected))
	for _, id := range expected {
		expectedSet[id] = true
	}
	hits, reciprocalRank := 0, 0.0
	for index, id := range selected {
		if !expectedSet[id] {
			continue
		}
		hits++
		if reciprocalRank == 0 {
			reciprocalRank = 1 / float64(index+1)
		}
	}
	if len(expectedSet) != 0 {
		a.evidenceRecall += float64(hits) / float64(len(expectedSet))
	}
	if len(selected) != 0 {
		a.precision += float64(hits) / float64(len(selected))
	}
	a.mrr += reciprocalRank
	a.ndcg += longMemEvalNDCG(selected, expected, k)
}

func (a *longMemEvalAccumulator) report(mode string) LongMemEvalModeReport {
	metrics := map[string]LongMemEvalRetrievalScore{}
	for _, k := range longMemEvalCutoffs {
		if k <= 10 {
			metrics["@"+strconv.Itoa(k)] = a.metrics[k].score()
		}
	}
	byType := map[string]LongMemEvalRetrievalScore{}
	for kind, value := range a.byType {
		byType[kind] = value.score()
	}
	sort.Float64s(a.latencies)
	return LongMemEvalModeReport{
		Mode: mode, Description: a.description, Cases: a.cases,
		CandidateRecallAny: ratio(a.candidateAny, a.cases), CandidateRecallAll: ratio(a.candidateAll, a.cases),
		AverageCandidates: ratio(a.candidates, a.cases), AverageSelectedItems: ratio(a.selectedItems, a.cases), AverageSelectedBytes: ratio(a.selectedBytes, a.cases),
		LatencyP50Millis: percentileLongMemEval(a.latencies, 0.50), LatencyP95Millis: percentileLongMemEval(a.latencies, 0.95),
		Metrics: metrics, ByQuestionType: byType,
	}
}

func (a *longMemEvalMetricAccumulator) score() LongMemEvalRetrievalScore {
	if a == nil || a.cases == 0 {
		return LongMemEvalRetrievalScore{}
	}
	return LongMemEvalRetrievalScore{
		RecallAny: a.recallAny / float64(a.cases), RecallAll: a.recallAll / float64(a.cases),
		EvidenceRecall: a.evidenceRecall / float64(a.cases), Precision: a.precision / float64(a.cases), MRR: a.mrr / float64(a.cases), NDCG: a.ndcg / float64(a.cases),
	}
}

func longMemEvalNDCG(selected, expected []string, k int) float64 {
	correct := make(map[string]bool, len(expected))
	for _, id := range expected {
		correct[id] = true
	}
	actual := 0.0
	for index, id := range selected {
		if index >= k {
			break
		}
		if correct[id] {
			if index == 0 {
				actual++
			} else {
				actual += 1 / math.Log2(float64(index+1))
			}
		}
	}
	ideal := 0.0
	for index := 0; index < min(k, len(expected)); index++ {
		if index == 0 {
			ideal++
		} else {
			ideal += 1 / math.Log2(float64(index+1))
		}
	}
	if ideal == 0 {
		return 0
	}
	return actual / ideal
}

func percentileLongMemEval(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(percentile*float64(len(values)))) - 1
	return values[max(0, min(index, len(values)-1))]
}

func longMemEvalBM25(query string, documents []string) []float64 {
	queryTerms := longMemEvalTokens(query)
	documentTerms := make([][]string, len(documents))
	for index, document := range documents {
		documentTerms[index] = longMemEvalTokens(document)
	}
	return longMemEvalBM25TokenSets(queryTerms, documentTerms)
}

func longMemEvalBM25TokenSets(queryTerms []string, documentTerms [][]string) []float64 {
	documentFrequency := map[string]int{}
	totalLength := 0
	for _, terms := range documentTerms {
		totalLength += len(terms)
		seen := map[string]bool{}
		for _, term := range terms {
			if !seen[term] {
				seen[term] = true
				documentFrequency[term]++
			}
		}
	}
	scores := make([]float64, len(documentTerms))
	if len(queryTerms) == 0 || len(documentTerms) == 0 {
		return scores
	}
	averageLength := float64(totalLength) / float64(len(documentTerms))
	if averageLength == 0 {
		averageLength = 1
	}
	const k1, b = 1.2, 0.75
	for index, terms := range documentTerms {
		frequencies := map[string]int{}
		for _, term := range terms {
			frequencies[term]++
		}
		for _, term := range queryTerms {
			frequency := frequencies[term]
			if frequency == 0 {
				continue
			}
			df := documentFrequency[term]
			idf := math.Log(1 + (float64(len(documentTerms)-df)+0.5)/(float64(df)+0.5))
			numerator := float64(frequency) * (k1 + 1)
			denominator := float64(frequency) + k1*(1-b+b*float64(len(terms))/averageLength)
			scores[index] += idf * numerator / denominator
		}
	}
	return scores
}

func longMemEvalHybridWordTokens(value string) []string {
	base := longMemEvalTokens(value)
	out := make([]string, 0, len(base)*2)
	for _, term := range base {
		out = append(out, term)
		if stem := longMemEvalStem(term); stem != term {
			out = append(out, "stem:"+stem)
		}
	}
	return out
}

func longMemEvalStem(value string) string {
	switch {
	case len(value) > 5 && strings.HasSuffix(value, "ies"):
		return strings.TrimSuffix(value, "ies") + "y"
	case len(value) > 6 && strings.HasSuffix(value, "ing"):
		return strings.TrimSuffix(value, "ing")
	case len(value) > 5 && strings.HasSuffix(value, "ed"):
		return strings.TrimSuffix(value, "ed")
	case len(value) > 5 && strings.HasSuffix(value, "es"):
		return strings.TrimSuffix(value, "es")
	case len(value) > 4 && strings.HasSuffix(value, "s"):
		return strings.TrimSuffix(value, "s")
	default:
		return value
	}
}

func longMemEvalCharacterTokens(value string) []string {
	words := longMemEvalTokens(value)
	out := make([]string, 0, len(words)*6)
	for _, word := range words {
		runes := []rune("^" + word + "$")
		if len(runes) <= 4 {
			out = append(out, "char:"+word)
			continue
		}
		for index := 0; index+3 <= len(runes); index++ {
			out = append(out, "char:"+string(runes[index:index+3]))
		}
	}
	return out
}

func longMemEvalTokens(value string) []string {
	stop := longMemEvalStopWords()
	out := []string{}
	var token []rune
	flush := func() {
		if len(token) == 0 {
			return
		}
		value := strings.ToLower(string(token))
		if len(value) > 1 && !stop[value] {
			out = append(out, value)
		}
		token = token[:0]
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			token = append(token, character)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func longMemEvalStopWords() map[string]bool {
	return map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "been": true, "but": true, "by": true,
		"did": true, "do": true, "does": true, "for": true, "from": true, "had": true, "has": true, "have": true, "how": true, "i": true,
		"in": true, "is": true, "it": true, "me": true, "my": true, "of": true, "on": true, "or": true, "our": true, "that": true,
		"the": true, "their": true, "them": true, "they": true, "this": true, "to": true, "was": true, "we": true, "were": true,
		"what": true, "when": true, "where": true, "which": true, "who": true, "why": true, "with": true, "would": true, "you": true, "your": true,
	}
}

func hashLongMemEvalFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(digest.Sum(nil)), size, nil
}

func longMemEvalProcessIntegrity() (string, []string, bool) {
	executableHash := ""
	if executable, err := os.Executable(); err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
			executable = resolved
		}
		if hash, _, hashErr := hashLongMemEvalFile(executable); hashErr == nil {
			executableHash = hash
		}
	}
	keys := make([]string, 0, len(os.Environ()))
	allowed := map[string]bool{"PATH": true, "TMPDIR": true}
	clean := true
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		keys = append(keys, key)
		clean = clean && allowed[key]
	}
	sort.Strings(keys)
	return executableHash, keys, clean
}
