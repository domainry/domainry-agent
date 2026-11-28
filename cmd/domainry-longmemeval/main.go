package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/domainry/domainry-agent/internal/memoryeval"
)

func main() {
	input := flag.String("input", "", "path to the official longmemeval_s_cleaned.json dataset")
	output := flag.String("output", "", "optional path for the JSON report; stdout when omitted")
	datasetSHA256 := flag.String("dataset-sha256", memoryeval.LongMemEvalOfficialSHA256, "required SHA-256 for the benchmark dataset; empty disables pinning")
	semanticCache := flag.String("semantic-cache", "", "optional cache produced by cmd/domainry-longmemeval/scripts/longmemeval_semantic_cache.py")
	split := flag.String("split", "all", "evaluation split: all, development, or holdout")
	modes := flag.String("modes", "", "optional comma-separated mode allowlist")
	semanticWeight := flag.Float64("semantic-weight", 0.5, "semantic contribution for the hybrid semantic mode")
	limit := flag.Int("limit", 0, "optional number of non-abstention cases; zero runs the full dataset")
	topK := flag.Int("top-k", 10, "maximum memory items selected by the production memory budget")
	includeCases := flag.Bool("include-cases", false, "include per-case retrieval evidence in the JSON report")
	flag.Parse()
	var requestedModes []string
	if strings.TrimSpace(*modes) != "" {
		requestedModes = strings.Split(*modes, ",")
	}
	report, err := memoryeval.RunLongMemEval(context.Background(), memoryeval.LongMemEvalConfig{InputPath: *input, ExpectedSHA256: *datasetSHA256, SemanticCache: *semanticCache, Split: *split, Modes: requestedModes, SemanticWeight: *semanticWeight, Limit: *limit, TopK: *topK, IncludeCases: *includeCases})
	if err != nil {
		fatal(err)
	}
	var destination *os.File
	if *output == "" {
		destination = os.Stdout
	} else {
		destination, err = os.Create(*output)
		if err != nil {
			fatal(err)
		}
		defer destination.Close()
	}
	encoder := json.NewEncoder(destination)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(report); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
