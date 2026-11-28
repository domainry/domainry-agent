package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/domainry/domainry-agent/internal/memoryeval"
)

func main() {
	input := flag.String("input", "", "path to a MemoryBench JSON file")
	flag.Parse()
	if *input == "" {
		fatal(fmt.Errorf("-input is required"))
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		fatal(err)
	}
	var benchmark memoryeval.Benchmark
	if err = json.Unmarshal(raw, &benchmark); err != nil {
		fatal(err)
	}
	report, err := memoryeval.Evaluate(benchmark)
	if err != nil {
		fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(report); err != nil {
		fatal(err)
	}
	if !report.Passed {
		os.Exit(1)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
