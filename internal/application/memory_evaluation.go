package application

import (
	"context"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// BuildMemoryContextSnapshotForEvaluation executes the production ranking and
// selection path for a fixed provider result. It exists so offline benchmarks
// measure the shipped policy gates, relevance merge, budgets and receipts
// instead of a second implementation that merely resembles them.
func BuildMemoryContextSnapshotForEvaluation(ctx context.Context, request agentsdk.MemoryContextRequest, results []agentsdk.MemoryContextResult, ranker agentsdk.MemoryContextRanker, now time.Time) (agentsdk.MemoryContextSnapshot, error) {
	ranked, ranking, err := rankMemoryContextResults(ctx, request, results, ranker, now)
	if err != nil {
		return agentsdk.MemoryContextSnapshot{}, err
	}
	snapshot, err := selectTaskMemorySnapshot(request, ranked, now)
	if err != nil {
		return agentsdk.MemoryContextSnapshot{}, err
	}
	snapshot.Ranking = ranking
	return snapshot, nil
}
