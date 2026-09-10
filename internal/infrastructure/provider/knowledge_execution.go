package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-connectors/providers/knowledge_base/http_api"
)

func (k *Knowledge) knowledgeScope(a agentsdk.ConversationAuthority) string {
	// Non-secret configuration and owner identity. Rotating an API key is not
	// a source change; switching team, KB, origin or owner is.
	raw, _ := json.Marshal([]any{k.config.BaseURL, k.config.TeamID, k.config.KBID, k.config.WorkspaceID, k.config.TopK, a.RuntimeID, a.WorkspaceID, a.UserID})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (k *Knowledge) SearchKnowledge(ctx context.Context, query string, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	raw, err := k.Search(ctx, query, a)
	return k.knowledgeEvidence(raw, err, "search", query, "", a)
}

func (k *Knowledge) ReadKnowledge(ctx context.Context, docID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	raw, err := k.Fetch(ctx, docID, a)
	return k.knowledgeEvidence(raw, err, "fetch", "", docID, a)
}

func (k *Knowledge) knowledgeEvidence(raw json.RawMessage, err error, operation, query, docID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	var envelope httpapi.Output
	if json.Unmarshal(raw, &envelope) != nil || envelope.Provider != httpapi.ProviderKey || envelope.KBID != k.config.KBID || len(envelope.Result) == 0 {
		return agentsdk.ConversationKnowledgeResult{}, knowledgeFailure("response_invalid")
	}
	// Normalize JSON once before exposing it; preserve integer precision and
	// all source fields without guessing an undocumented response schema.
	decoder := json.NewDecoder(bytes.NewReader(envelope.Result))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return agentsdk.ConversationKnowledgeResult{}, knowledgeFailure("response_invalid")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, knowledgeFailure("response_invalid")
	}
	evidence := agentsdk.ConversationKnowledgeResult{Provider: envelope.Provider, KBID: envelope.KBID, Operation: operation, Query: query, DocumentID: docID, ScopeSHA256: k.knowledgeScope(a), Data: data}
	evidence.Citations, err = k.citations(evidence, value)
	return evidence, err
}

func (k *Knowledge) RevalidateKnowledge(ctx context.Context, saved agentsdk.ConversationKnowledgeResult, a agentsdk.ConversationAuthority) error {
	if saved.LibraryID != "" || saved.ScopeSHA256 != k.knowledgeScope(a) || saved.Provider != httpapi.ProviderKey || saved.KBID != k.config.KBID {
		return knowledgeFailure("access_denied")
	}
	var current agentsdk.ConversationKnowledgeResult
	var err error
	switch {
	case saved.Operation == "search" && saved.Query != "" && saved.DocumentID == "":
		current, err = k.SearchKnowledge(ctx, saved.Query, a)
	case saved.Operation == "fetch" && saved.DocumentID != "" && saved.Query == "":
		current, err = k.ReadKnowledge(ctx, saved.DocumentID, a)
	default:
		return knowledgeFailure("response_invalid")
	}
	if err != nil {
		return err
	}
	// The service currently documents no ACL/version receipt or response
	// schema. Comparing the complete current response is conservative but
	// checks remote ACL changes as well as host permission changes. Never
	// replace a frozen model input with newly retrieved data on resume.
	if !bytes.Equal(saved.Data, current.Data) {
		return &agentsdk.Error{Class: "conflict", Code: "agent.conversation.knowledge_source_changed"}
	}
	// Old receipts without citations remain readable. Once a normalized
	// citation exists, its metadata must still be derived from the real data.
	if len(saved.Citations) > 0 {
		one, _ := json.Marshal(saved.Citations)
		two, _ := json.Marshal(current.Citations)
		if !bytes.Equal(one, two) {
			return &agentsdk.Error{Class: "conflict", Code: "agent.conversation.knowledge_source_changed"}
		}
	}
	return nil
}

var _ agentsdk.ConversationKnowledgeSource = (*Knowledge)(nil)
