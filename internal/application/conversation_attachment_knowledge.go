package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

const AttachmentKnowledgeProvider = "agent_conversation_documents"

// Metadata and Connector passages only: this path never reads original bytes.
func (s *ConversationService) attachmentKnowledgeAccess(ctx context.Context, conversation string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentKnowledgeScope, map[string]persistence.ConversationAttachmentRecord, error) {
	var zero agentsdk.ConversationAttachmentKnowledgeScope
	if _, err := s.attachmentAccess(ctx, "attachments_download", a); err != nil {
		return zero, nil, err
	}
	parent, err := s.Get(ctx, conversation, a)
	if err != nil {
		return zero, nil, err
	}
	if parent.Archived {
		return zero, nil, conversationFailure("forbidden", "attachment_conversation_archived")
	}
	scope, err := s.attachmentKnowledgeBinding(ctx, conversation, a)
	if err != nil {
		return zero, nil, err
	}
	repo, ok := s.repo.(persistence.ConversationAttachmentKnowledgeRepository)
	if !ok {
		return zero, nil, conversationFailure("unavailable", "attachment_knowledge_unavailable")
	}
	records, err := repo.AttachmentKnowledgeRecords(ctx, conversation, a)
	if err != nil {
		return zero, nil, err
	}
	if len(records) > 50 {
		return zero, nil, conversationFailure("unavailable", "attachment_metadata_invalid")
	}
	out := map[string]persistence.ConversationAttachmentRecord{}
	for _, r := range records {
		if r.Attachment.ConversationID != conversation || r.Attachment.State != "ready" || r.Source == nil || r.Index == nil || !r.Index.IndexObserved || r.Index.DeleteStarted ||
			r.Index.Actor.RuntimeID != a.RuntimeID || r.Index.Actor.WorkspaceID != a.WorkspaceID || r.Index.Actor.UserID != a.UserID ||
			r.Source.Identity != scope.Source.KnowledgeDocumentSourceIdentity() || r.Source.AccessPolicySHA256 != scope.Source.KnowledgeDocumentAccessPolicySHA256() || !slices.Equal(r.Source.DocumentPermissionIDs, scope.DocumentPermissionIDs) || r.Source.DocID == "" {
			continue
		}
		if _, duplicate := out[r.Source.DocID]; duplicate {
			return zero, nil, conversationFailure("unavailable", "attachment_metadata_invalid")
		}
		out[r.Source.DocID] = r
	}
	return scope, out, nil
}

func AttachmentKnowledgeReceipt(scope agentsdk.ConversationAttachmentKnowledgeScope, conversation, op, q, id string, data DocumentEvidence, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	raw, err := json.Marshal(data)
	if err != nil || len(raw) > 640*1024 {
		return agentsdk.ConversationKnowledgeResult{}, conversationFailure("unavailable", "knowledge_context_exceeded")
	}
	identity := []any{AttachmentKnowledgeProvider, scope.Source.KnowledgeDocumentSourceIdentity(), scope.Source.KnowledgeDocumentAccessPolicySHA256(), scope.DocumentPermissionIDs, scope.ReadPermissionIDs, a.RuntimeID, a.WorkspaceID, a.UserID, conversation}
	out := agentsdk.ConversationKnowledgeResult{Provider: AttachmentKnowledgeProvider, ConversationID: conversation, Operation: op, Query: q, DocumentID: id, SearchTopK: data.SearchTopK, SearchMode: data.SearchMode, SearchReranked: data.SearchReranked, SearchDegraded: data.SearchDegraded, Data: raw}
	for i, p := range data.Passages {
		if i >= 50 {
			break
		}
		excerpt := p.Content
		if len(excerpt) > 2048 {
			excerpt = excerpt[:2048]
			for !utf8.ValidString(excerpt) {
				excerpt = excerpt[:len(excerpt)-1]
			}
		}
		out.Citations = append(out.Citations, agentsdk.ConversationCitation{ID: "kc_" + conversationDigest([]any{identity, op, q, id, data, i})[:32], Provider: AttachmentKnowledgeProvider, ConversationID: conversation, Operation: op, DocumentID: p.DocumentID, ChunkID: p.ChunkID, DocumentVersion: p.DocumentVersion, ContentSHA256: p.ContentSHA256, SourceRevision: p.SourceRevision, Score: p.Score, Title: p.Title, Excerpt: excerpt, ExcerptTruncated: len(excerpt) < len(p.Content), Location: p.Location})
	}
	out.ScopeSHA256 = conversationDigest([]any{identity, out})
	return out, nil
}

func (s *ConversationService) attachmentKnowledge(ctx context.Context, conversation, op, q, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	var zero agentsdk.ConversationKnowledgeResult
	scope, allowed, err := s.attachmentKnowledgeAccess(ctx, conversation, a)
	if err != nil {
		return zero, err
	}
	var passages []agentsdk.KnowledgeDocumentPassage
	var search agentsdk.KnowledgeDocumentSearch
	if op == "search" {
		search, err = managedKnowledgeSearch(ctx, scope.Source, q, a)
		passages = search.Passages
	} else if op == "fetch" {
		remote := ""
		for remoteID, r := range allowed {
			if r.Attachment.ID == id {
				remote = remoteID
				break
			}
		}
		if remote == "" {
			return zero, conversationFailure("not_found", "attachment_not_found")
		}
		passages, err = scope.Source.ReadKnowledgeDocumentPassages(ctx, remote, a)
	} else {
		return zero, conversationFailure("bad_request", "knowledge_response_invalid")
	}
	if err != nil {
		return zero, err
	}
	if len(passages) > 1000 {
		return zero, conversationFailure("unavailable", "knowledge_response_invalid")
	}
	// Re-resolve after network I/O to catch revocation, deletion and source changes.
	currentScope, current, err := s.attachmentKnowledgeAccess(ctx, conversation, a)
	if err != nil {
		return zero, err
	}
	if currentScope.Source.KnowledgeDocumentSourceIdentity() != scope.Source.KnowledgeDocumentSourceIdentity() || currentScope.Source.KnowledgeDocumentAccessPolicySHA256() != scope.Source.KnowledgeDocumentAccessPolicySHA256() || !slices.Equal(currentScope.DocumentPermissionIDs, scope.DocumentPermissionIDs) || !slices.Equal(currentScope.ReadPermissionIDs, scope.ReadPermissionIDs) {
		return zero, conversationFailure("forbidden", "knowledge_access_denied")
	}
	data := DocumentEvidence{Passages: []agentsdk.KnowledgeDocumentPassage{}, Documents: []DocumentSnapshot{}, SearchTopK: search.TopK, SearchMode: search.Mode, SearchReranked: search.Reranked, SearchDegraded: search.DegradedReason, Partial: true}
	seen := map[string]bool{}
	for _, p := range passages {
		before, known := allowed[p.DocumentID]
		r, readable := current[p.DocumentID]
		if !known {
			continue
		} // Even a faulty upstream ACL cannot expose foreign hits.
		if !readable || r.Attachment.ID != before.Attachment.ID || r.Attachment.SHA256 != before.Attachment.SHA256 {
			return zero, conversationFailure("forbidden", "knowledge_access_denied")
		}
		if id != "" && r.Attachment.ID != id {
			return zero, conversationFailure("unavailable", "knowledge_response_invalid")
		}
		if !conversationText(p.Content, 512*1024, true) {
			return zero, conversationFailure("unavailable", "knowledge_response_invalid")
		}
		if !conversationText(p.ChunkID, 512, false) || !conversationText(p.SourceRevision, 512, false) || p.DocumentVersion < 0 || p.ContentSHA256 != "" && p.ContentSHA256 != r.Attachment.SHA256 {
			return zero, conversationFailure("unavailable", "knowledge_response_invalid")
		}
		p.DocumentID, p.Title, p.URL = r.Attachment.ID, r.Attachment.Filename, ""
		p.ContentSHA256 = r.Attachment.SHA256
		data.Passages = append(data.Passages, p)
		if !seen[p.DocumentID] {
			seen[p.DocumentID] = true
			data.Documents = append(data.Documents, DocumentSnapshot{ID: p.DocumentID, SHA256: r.Attachment.SHA256})
		}
	}
	if id != "" && len(data.Passages) == 0 {
		return zero, conversationFailure("not_found", "knowledge_not_found")
	}
	return AttachmentKnowledgeReceipt(scope, conversation, op, q, id, data, a)
}

func (s *ConversationService) authorizeAttachmentKnowledgeResult(ctx context.Context, in agentsdk.ConversationToolRequest, result agentsdk.ConversationToolResult) error {
	auth, err := s.authorizeAttachmentKnowledgeTool(ctx, in)
	if err != nil {
		return err
	}
	if !auth.Granted || auth.ConfirmationRequired {
		return conversationFailure("forbidden", "tool_access_denied")
	}
	args, err := attachmentToolArguments(in)
	if err != nil {
		return err
	}
	var saved agentsdk.ConversationKnowledgeResult
	decoder := json.NewDecoder(bytes.NewReader(result.Content))
	decoder.DisallowUnknownFields()
	var extra any
	if len(result.Content) > in.Definition.MaxOutputBytes || decoder.Decode(&saved) != nil || decoder.Decode(&extra) != io.EOF || saved.Provider != AttachmentKnowledgeProvider || saved.ConversationID != in.ConversationID || saved.LibraryID != "" || saved.KBID != "" {
		return conversationFailure("forbidden", "knowledge_access_denied")
	}
	op := "search"
	if in.Call.Name == "attachment_read" {
		op = "fetch"
	}
	if saved.Operation != op || saved.Query != args.Query || saved.DocumentID != args.AttachmentID {
		return conversationFailure("conflict", "knowledge_response_invalid")
	}
	current, err := s.attachmentKnowledge(ctx, in.ConversationID, op, args.Query, args.AttachmentID, in.Authority)
	if err != nil {
		return err
	}
	// Strict decoding rejects unknown envelope/citation fields; raw passage data
	// is compared too. A receipt is never treated as an ongoing Access grant.
	if conversationDigest(current) != conversationDigest(saved) {
		return conversationFailure("conflict", "knowledge_source_changed")
	}
	return nil
}
