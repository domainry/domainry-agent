package application

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type DocumentSnapshot struct {
	ID              string `json:"id"`
	ContentVersion  int64  `json:"content_version,omitempty"`
	IndexGeneration int64  `json:"index_generation,omitempty"`
	SHA256          string `json:"sha256"`
	PageID          string `json:"page_id,omitempty"`
	PageVersion     int64  `json:"page_version,omitempty"`
	AccessRevision  int64  `json:"access_revision,omitempty"`
	PermissionID    string `json:"permission_id,omitempty"`
}

type DocumentEvidence struct {
	Passages       []agentsdk.KnowledgeDocumentPassage `json:"passages"`
	Documents      []DocumentSnapshot                  `json:"documents"`
	SearchTopK     int                                 `json:"search_top_k,omitempty"`
	SearchMode     string                              `json:"search_mode,omitempty"`
	SearchReranked *bool                               `json:"search_reranked,omitempty"`
	SearchDegraded string                              `json:"search_degraded_reason,omitempty"`
	Partial        bool                                `json:"partial"`
}

func DocumentIndexMaximumBytes(source any) int64 {
	maximum := agentsdk.KnowledgeDocumentMaximumBytes
	if limit, ok := source.(agentsdk.KnowledgeDocumentIndexLimitSource); ok {
		if value := limit.KnowledgeDocumentIndexMaximumBytes(); value > 0 && value < maximum {
			maximum = value
		}
	}
	return maximum
}

var knowledgeDocumentExtension = regexp.MustCompile(`^\.[a-z0-9]{1,8}$`)

func DocumentIndexMaximumBytesForFilename(source any, filename string) int64 {
	formats, ok := source.(agentsdk.KnowledgeDocumentFormatSource)
	if !ok {
		return 0
	}
	raw := formats.KnowledgeDocumentFormats()
	if len(raw) == 0 || len(raw) > 128 {
		return 0
	}
	extension := strings.ToLower(filepath.Ext(filename))
	seen := map[string]bool{}
	for _, format := range raw {
		format.Extension = strings.ToLower(format.Extension)
		if !knowledgeDocumentExtension.MatchString(format.Extension) || seen[format.Extension] || format.ContentType == "" || len(format.ContentType) > 128 || format.ParserRoute == "" || len(format.ParserRoute) > 32 || format.BuilderMaxBytes < 0 || format.AssetMaxBytes < 0 || format.EnrichmentMaxBytes < 0 || format.AnalysisOutputMaxBytes < 0 {
			return 0
		}
		seen[format.Extension] = true
		if format.Extension == extension {
			maximum := DocumentIndexMaximumBytes(source)
			if format.BuilderMaxBytes > 0 && format.BuilderMaxBytes < maximum {
				maximum = format.BuilderMaxBytes
			}
			return maximum
		}
	}
	return 0
}

func RecoverKnowledgeDocumentDelete(ctx context.Context, source agentsdk.KnowledgeDocumentSource, id string, authority agentsdk.ConversationAuthority, leaseUntil time.Time) bool {
	recovery, ok := source.(agentsdk.KnowledgeDocumentDeleteRecoverySource)
	if !ok {
		return false
	}
	write, cancel, err := CommittedKnowledgeWriteContext(ctx, leaseUntil)
	if err != nil {
		return false
	}
	defer cancel()
	return recovery.RecoverKnowledgeDocumentDelete(write, id, authority) == nil
}

func CommittedKnowledgeWriteContext(ctx context.Context, expires time.Time) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	deadline := expires.Add(-5 * time.Second)
	if prior, ok := ctx.Deadline(); ok && prior.Before(deadline) {
		deadline = prior
	}
	if !deadline.After(time.Now()) {
		return nil, nil, context.DeadlineExceeded
	}
	write, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	return write, cancel, nil
}

func managedKnowledgeSearch(ctx context.Context, source agentsdk.ManagedKnowledgeDocumentSource, query string, authority agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentSearch, error) {
	if searchable, ok := source.(agentsdk.ManagedKnowledgeDocumentSearchSource); ok {
		result, err := searchable.SearchKnowledgeDocuments(ctx, query, authority)
		if err != nil {
			return result, err
		}
		if result.TopK < 1 || result.TopK > 20 || result.Mode != "" && result.Mode != "hybrid" && result.Mode != "dense" && result.Mode != "sparse" || result.DegradedReason != "" && result.DegradedReason != "embedder_unavailable" && result.DegradedReason != "bm25_unavailable" && result.DegradedReason != "rerank_unavailable" {
			return agentsdk.KnowledgeDocumentSearch{}, conversationFailure("unavailable", "knowledge_response_invalid")
		}
		if result.Reranked != nil {
			value := *result.Reranked
			result.Reranked = &value
		}
		return result, nil
	}
	passages, err := source.SearchKnowledgeDocumentPassages(ctx, query, authority)
	return agentsdk.KnowledgeDocumentSearch{Passages: passages}, err
}
