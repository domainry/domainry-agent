package application

import (
	"context"
	"encoding/json"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
	knowledge "github.com/domainry/domainry-knowledge-sdk/contract"
)

func knowledgeOptions(o ConversationOptions) knowledge.Options {
	options := knowledge.Options{
		DocumentStorage: o.DocumentStorage, DocumentSecurity: o.DocumentSecurity, DocumentPoll: o.DocumentPoll, DocumentWorkTimeout: o.DocumentWorkTimeout, DocumentUploadIdleTimeout: o.DocumentUploadIdleTimeout,
		LibraryKnowledge: o.LibraryKnowledge, KnowledgeDatasources: o.KnowledgeDatasources,
		LibraryAuthorizer: o.LibraryAuthorizer, SourceAuthorizer: o.SourceAuthorizer,
		PrincipalDirectory: o.PrincipalDirectory, AccessExplainer: o.AccessExplainer,
	}
	if source, ok := o.Knowledge.(knowledge.Knowledge); ok {
		options.Knowledge = source
	} else if source, ok := o.Knowledge.(agentsdk.ConversationKnowledgeSource); ok {
		options.Knowledge = conversationKnowledgeContractAdapter{source: source}
	}
	return options
}

// Knowledge owns retrieval contracts. Agent only adds conversation-scoped
// receipts, so this boundary performs the explicit conversion in both
// directions instead of keeping a second Knowledge implementation alive.
type conversationKnowledgeContractAdapter struct {
	source agentsdk.ConversationKnowledgeSource
}

func (a conversationKnowledgeContractAdapter) SearchKnowledge(ctx context.Context, query string, authority knowledgesdk.Authority) (knowledgesdk.Result, error) {
	result, err := a.source.SearchKnowledge(ctx, query, authority)
	return knowledgeResult(result), err
}

func (a conversationKnowledgeContractAdapter) ReadKnowledge(ctx context.Context, documentID string, authority knowledgesdk.Authority) (knowledgesdk.Result, error) {
	result, err := a.source.ReadKnowledge(ctx, documentID, authority)
	return knowledgeResult(result), err
}

func (a conversationKnowledgeContractAdapter) RevalidateKnowledge(ctx context.Context, saved knowledgesdk.Result, authority knowledgesdk.Authority) error {
	return a.source.RevalidateKnowledge(ctx, conversationKnowledgeResult(saved), authority)
}

type canonicalLibraryKnowledge interface {
	knowledge.Knowledge
	ListKnowledgeLibraries(context.Context, string, int, knowledgesdk.Authority) (knowledgesdk.Result, error)
	SearchLibraryKnowledge(context.Context, string, string, knowledgesdk.Authority) (knowledgesdk.Result, error)
	ReadLibraryKnowledge(context.Context, string, string, knowledgesdk.Authority) (knowledgesdk.Result, error)
}

type canonicalKnowledgeAdapter struct {
	source knowledge.Knowledge
}

func (a canonicalKnowledgeAdapter) Search(ctx context.Context, query string, authority agentsdk.ConversationAuthority) (json.RawMessage, error) {
	result, err := a.source.SearchKnowledge(ctx, query, authority)
	return result.Data, err
}

func (a canonicalKnowledgeAdapter) SearchKnowledge(ctx context.Context, query string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	result, err := a.source.SearchKnowledge(ctx, query, authority)
	return conversationKnowledgeResult(result), err
}

func (a canonicalKnowledgeAdapter) ReadKnowledge(ctx context.Context, documentID string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	result, err := a.source.ReadKnowledge(ctx, documentID, authority)
	return conversationKnowledgeResult(result), err
}

func (a canonicalKnowledgeAdapter) RevalidateKnowledge(ctx context.Context, saved agentsdk.ConversationKnowledgeResult, authority agentsdk.ConversationAuthority) error {
	return a.source.RevalidateKnowledge(ctx, knowledgeResult(saved), authority)
}

func (a canonicalKnowledgeAdapter) AuthorizeKnowledgeResultRead(ctx context.Context, saved agentsdk.ConversationKnowledgeResult, authority agentsdk.ConversationAuthority) error {
	if authorizer, ok := a.source.(knowledgesdk.ResultReadAuthorizer); ok {
		return authorizer.AuthorizeKnowledgeResultRead(ctx, knowledgeResult(saved), authority)
	}
	return a.RevalidateKnowledge(ctx, saved, authority)
}

type canonicalExtractionKnowledgeAdapter struct {
	canonicalKnowledgeAdapter
	extraction knowledgesdk.ExtractionContentSource
}

func (a canonicalExtractionKnowledgeAdapter) KnowledgeExtractionPassages(ctx context.Context, saved agentsdk.ConversationKnowledgeResult, authority agentsdk.ConversationAuthority) ([]agentsdk.KnowledgeDocumentPassage, error) {
	return a.extraction.KnowledgeExtractionPassages(ctx, knowledgeResult(saved), authority)
}

type canonicalLibraryKnowledgeAdapter struct {
	canonicalKnowledgeAdapter
	source canonicalLibraryKnowledge
}

type canonicalLibraryExtractionKnowledgeAdapter struct {
	canonicalLibraryKnowledgeAdapter
	extraction knowledgesdk.ExtractionContentSource
}

func (a canonicalLibraryExtractionKnowledgeAdapter) KnowledgeExtractionPassages(ctx context.Context, saved agentsdk.ConversationKnowledgeResult, authority agentsdk.ConversationAuthority) ([]agentsdk.KnowledgeDocumentPassage, error) {
	return a.extraction.KnowledgeExtractionPassages(ctx, knowledgeResult(saved), authority)
}

func (a canonicalLibraryKnowledgeAdapter) ListKnowledgeLibraries(ctx context.Context, after string, limit int, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	result, err := a.source.ListKnowledgeLibraries(ctx, after, limit, authority)
	return conversationKnowledgeResult(result), err
}

func (a canonicalLibraryKnowledgeAdapter) SearchLibraryKnowledge(ctx context.Context, libraryID, query string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	result, err := a.source.SearchLibraryKnowledge(ctx, libraryID, query, authority)
	return conversationKnowledgeResult(result), err
}

func (a canonicalLibraryKnowledgeAdapter) ReadLibraryKnowledge(ctx context.Context, libraryID, documentID string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	result, err := a.source.ReadLibraryKnowledge(ctx, libraryID, documentID, authority)
	return conversationKnowledgeResult(result), err
}

func AdaptCanonicalKnowledge(source knowledge.Knowledge) ConversationKnowledge {
	base := canonicalKnowledgeAdapter{source: source}
	library, hasLibrary := source.(canonicalLibraryKnowledge)
	extraction, hasExtraction := source.(knowledgesdk.ExtractionContentSource)
	if hasLibrary {
		adapted := canonicalLibraryKnowledgeAdapter{canonicalKnowledgeAdapter: base, source: library}
		if hasExtraction {
			return canonicalLibraryExtractionKnowledgeAdapter{canonicalLibraryKnowledgeAdapter: adapted, extraction: extraction}
		}
		return adapted
	}
	if hasExtraction {
		return canonicalExtractionKnowledgeAdapter{canonicalKnowledgeAdapter: base, extraction: extraction}
	}
	return base
}

func knowledgeResult(value agentsdk.ConversationKnowledgeResult) knowledgesdk.Result {
	result := knowledgesdk.Result{
		LibraryID: value.LibraryID, Provider: value.Provider, KBID: value.KBID, Operation: value.Operation,
		Query: value.Query, DocumentID: value.DocumentID, SearchTopK: value.SearchTopK, SearchMode: value.SearchMode,
		SearchReranked: value.SearchReranked, SearchDegraded: value.SearchDegraded, ScopeSHA256: value.ScopeSHA256, Data: value.Data,
	}
	for _, citation := range value.Citations {
		result.Citations = append(result.Citations, knowledgesdk.Citation{
			LibraryID: citation.LibraryID, ID: citation.ID, Source: citation.Source, Provider: citation.Provider, KBID: citation.KBID,
			Operation: citation.Operation, ObjectKey: citation.ObjectKey, RecordID: citation.RecordID, DocumentID: citation.DocumentID,
			PageID: citation.PageID, PageVersion: citation.PageVersion, ChunkID: citation.ChunkID, DocumentVersion: citation.DocumentVersion,
			ContentSHA256: citation.ContentSHA256, SourceRevision: citation.SourceRevision, Score: citation.Score, Title: citation.Title,
			URL: citation.URL, Excerpt: citation.Excerpt, ExcerptTruncated: citation.ExcerptTruncated, Location: citation.Location,
		})
	}
	return result
}

func conversationKnowledgeResult(value knowledgesdk.Result) agentsdk.ConversationKnowledgeResult {
	result := agentsdk.ConversationKnowledgeResult{
		LibraryID: value.LibraryID, Provider: value.Provider, KBID: value.KBID, Operation: value.Operation,
		Query: value.Query, DocumentID: value.DocumentID, SearchTopK: value.SearchTopK, SearchMode: value.SearchMode,
		SearchReranked: value.SearchReranked, SearchDegraded: value.SearchDegraded, ScopeSHA256: value.ScopeSHA256, Data: value.Data,
	}
	for _, citation := range value.Citations {
		result.Citations = append(result.Citations, agentsdk.ConversationCitation{
			LibraryID: citation.LibraryID, ID: citation.ID, Source: citation.Source, Provider: citation.Provider, KBID: citation.KBID,
			Operation: citation.Operation, ObjectKey: citation.ObjectKey, RecordID: citation.RecordID, DocumentID: citation.DocumentID,
			PageID: citation.PageID, PageVersion: citation.PageVersion, ChunkID: citation.ChunkID, DocumentVersion: citation.DocumentVersion,
			ContentSHA256: citation.ContentSHA256, SourceRevision: citation.SourceRevision, Score: citation.Score, Title: citation.Title,
			URL: citation.URL, Excerpt: citation.Excerpt, ExcerptTruncated: citation.ExcerptTruncated, Location: citation.Location,
		})
	}
	return result
}

func (s *ConversationService) knowledgeService() knowledge.Service {
	if s.knowledgeModule != nil {
		return s.knowledgeModule
	}
	s.knowledgeOnce.Do(func() {
		if s.options.KnowledgeRuntime != nil {
			s.knowledgeModule = s.options.KnowledgeRuntime.NewService(s.runtimeID, knowledgeOptions(s.options))
		} else {
			s.knowledgeModule = unconfiguredKnowledge{}
		}
	})
	return s.knowledgeModule
}
