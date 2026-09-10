package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Bindings are trusted host configuration, never browser or model input. Each
// binding must own an isolated remote knowledge source. In particular, team-
// visible documents in a shared remote KB cannot isolate two different libraries.
type LibraryKnowledgeBinding struct {
	WorkspaceID     string
	LibraryID       string
	Source          agentsdk.ConversationKnowledgeSource
	ManageDocuments bool // Trusted host opt-in; never a client supplied grant.
}

type libraryKnowledgeSource struct {
	repo      persistence.KnowledgeLibraryRepository
	policy    agentsdk.KnowledgeLibraryAuthorizer
	bindings  map[string]LibraryKnowledgeBinding
	legacy    ConversationKnowledge
	runtimeID string
	documents persistence.KnowledgeDocumentRepository
}

func newLibraryKnowledgeSource(repo persistence.ConversationRepository, runtimeID string, policy agentsdk.KnowledgeLibraryAuthorizer, bindings []LibraryKnowledgeBinding, legacy ConversationKnowledge) (*libraryKnowledgeSource, error) {
	libraries, ok := repo.(persistence.KnowledgeLibraryRepository)
	if !ok || policy == nil || len(bindings) == 0 || len(bindings) > 1000 {
		return nil, fmt.Errorf("library retrieval requires library persistence, live authorization and at most 1000 host bindings")
	}
	if legacy != nil {
		if _, ok := legacy.(agentsdk.ConversationKnowledgeSource); !ok {
			return nil, fmt.Errorf("legacy knowledge source must support revalidation")
		}
	}
	out := &libraryKnowledgeSource{repo: libraries, policy: policy, bindings: map[string]LibraryKnowledgeBinding{}, legacy: legacy, runtimeID: runtimeID}
	out.documents, _ = repo.(persistence.KnowledgeDocumentRepository)
	for _, binding := range bindings {
		if !conversationText(binding.WorkspaceID, 255, true) || !strings.HasPrefix(binding.LibraryID, "lib_") || len(binding.LibraryID) != 36 || !conversationKey(binding.LibraryID) || binding.Source == nil {
			return nil, fmt.Errorf("invalid library knowledge binding")
		}
		key := conversationDigest([]string{binding.WorkspaceID, binding.LibraryID})
		if _, exists := out.bindings[key]; exists {
			return nil, fmt.Errorf("duplicate library knowledge binding")
		}
		out.bindings[key] = binding
		if binding.ManageDocuments {
			source, ok := binding.Source.(agentsdk.ManagedKnowledgeDocumentSource)
			if !ok || out.documents == nil || source.KnowledgeDocumentManagementReady() != nil {
				return nil, fmt.Errorf("managed library requires document persistence and an isolated document provider with explicit field mappings")
			}
		}
	}
	return out, nil
}
func (k *libraryKnowledgeSource) configured(id string, a agentsdk.ConversationAuthority) bool {
	_, ok := k.bindings[conversationDigest([]string{a.WorkspaceID, id})]
	return ok && a.RuntimeID == k.runtimeID
}
func (k *libraryKnowledgeSource) access(ctx context.Context, id string, a agentsdk.ConversationAuthority) (LibraryKnowledgeBinding, error) {
	if !a.Known || a.RuntimeID != k.runtimeID {
		return LibraryKnowledgeBinding{}, conversationFailure("forbidden", "knowledge_access_denied")
	}
	binding, ok := k.bindings[conversationDigest([]string{a.WorkspaceID, id})]
	if !ok {
		return LibraryKnowledgeBinding{}, conversationFailure("forbidden", "knowledge_access_denied")
	}
	library, err := k.repo.KnowledgeLibrary(ctx, id, a)
	if err != nil {
		return LibraryKnowledgeBinding{}, libraryKnowledgeAccessError(err)
	}
	if library.Archived {
		return LibraryKnowledgeBinding{}, conversationFailure("forbidden", "knowledge_access_denied")
	}
	if err = k.policy.AuthorizeKnowledgeLibrary(ctx, "libraries_get", library, a); err != nil {
		return LibraryKnowledgeBinding{}, libraryKnowledgeAccessError(err)
	}
	return binding, nil
}
func libraryKnowledgeAccessError(err error) error {
	var coded *agentsdk.Error
	if errors.As(err, &coded) && (coded.Class == "forbidden" || coded.Class == "not_found") {
		return conversationFailure("forbidden", "knowledge_access_denied")
	}
	return err
}
func (k *libraryKnowledgeSource) Search(ctx context.Context, q string, a agentsdk.ConversationAuthority) (json.RawMessage, error) {
	if k.legacy == nil {
		return nil, conversationFailure("bad_request", "knowledge_library_required")
	}
	return k.legacy.Search(ctx, q, a)
}
func (k *libraryKnowledgeSource) SearchKnowledge(ctx context.Context, q string, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	if k.legacy == nil {
		return agentsdk.ConversationKnowledgeResult{}, conversationFailure("bad_request", "knowledge_library_required")
	}
	return k.legacy.(agentsdk.ConversationKnowledgeSource).SearchKnowledge(ctx, q, a)
}
func (k *libraryKnowledgeSource) ReadKnowledge(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	if k.legacy == nil {
		return agentsdk.ConversationKnowledgeResult{}, conversationFailure("bad_request", "knowledge_library_required")
	}
	return k.legacy.(agentsdk.ConversationKnowledgeSource).ReadKnowledge(ctx, id, a)
}
func (k *libraryKnowledgeSource) SearchLibraryKnowledge(ctx context.Context, id, q string, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	binding, err := k.access(ctx, id, a)
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	source, managed, err := k.managedSource(ctx, binding, a)
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	if managed {
		return k.managedKnowledge(ctx, binding, source, "search", q, "", a)
	}
	evidence, err := binding.Source.SearchKnowledge(ctx, q, a)
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	if evidence.Operation != "search" || evidence.Query != q || evidence.DocumentID != "" {
		return agentsdk.ConversationKnowledgeResult{}, conversationFailure("unavailable", "knowledge_response_invalid")
	}
	return k.wrap(ctx, id, evidence, a)
}
func (k *libraryKnowledgeSource) ReadLibraryKnowledge(ctx context.Context, id, doc string, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	binding, err := k.access(ctx, id, a)
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	source, managed, err := k.managedSource(ctx, binding, a)
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	if managed {
		return k.managedKnowledge(ctx, binding, source, "fetch", "", doc, a)
	}
	evidence, err := binding.Source.ReadKnowledge(ctx, doc, a)
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	if evidence.Operation != "fetch" || evidence.DocumentID != doc || evidence.Query != "" {
		return agentsdk.ConversationKnowledgeResult{}, conversationFailure("unavailable", "knowledge_response_invalid")
	}
	return k.wrap(ctx, id, evidence, a)
}
func (k *libraryKnowledgeSource) wrap(ctx context.Context, id string, evidence agentsdk.ConversationKnowledgeResult, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	binding, err := k.access(ctx, id, a)
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	if _, managed, err := k.managedSource(ctx, binding, a); err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	} else if managed {
		return agentsdk.ConversationKnowledgeResult{}, conversationFailure("conflict", "knowledge_source_changed")
	}
	if evidence.LibraryID != "" {
		return agentsdk.ConversationKnowledgeResult{}, conversationFailure("unavailable", "knowledge_response_invalid")
	}
	evidence.LibraryID = id
	evidence.Citations = append([]agentsdk.ConversationCitation(nil), evidence.Citations...)
	for i := range evidence.Citations {
		if evidence.Citations[i].LibraryID != "" {
			return agentsdk.ConversationKnowledgeResult{}, conversationFailure("unavailable", "knowledge_response_invalid")
		}
		evidence.Citations[i].LibraryID = id
	}
	return evidence, nil
}
func (k *libraryKnowledgeSource) ListKnowledgeLibraries(ctx context.Context, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	if !a.Known || a.RuntimeID != k.runtimeID {
		return agentsdk.ConversationKnowledgeResult{}, conversationFailure("forbidden", "knowledge_access_denied")
	}
	page, err := k.repo.KnowledgeLibraries(ctx, after, limit, a)
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	items := []agentsdk.KnowledgeLibrary{}
	for _, item := range page.Items {
		if !k.configured(item.ID, a) || item.Archived {
			continue
		}
		_, err := k.access(ctx, item.ID, a)
		if err != nil {
			var coded *agentsdk.Error
			if errors.As(err, &coded) && coded.Class == "forbidden" {
				continue
			}
			return agentsdk.ConversationKnowledgeResult{}, err
		}
		item.KnowledgeConfigured = true
		items = append(items, item)
	}
	page.Items = items
	data, err := json.Marshal(page)
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	for len(data) > 48*1024 && len(page.Items) > 1 {
		page.Items = page.Items[:len(page.Items)-1]
		page.Complete = false
		page.NextAfter = page.Items[len(page.Items)-1].ID
		data, err = json.Marshal(page)
		if err != nil {
			return agentsdk.ConversationKnowledgeResult{}, err
		}
	}
	query, _ := json.Marshal(struct {
		After string `json:"after"`
		Limit int    `json:"limit"`
	}{after, limit})
	return agentsdk.ConversationKnowledgeResult{Provider: "agent_libraries", Operation: "libraries", Query: string(query), ScopeSHA256: k.catalogScope(data, a), Data: data}, nil
}
func (k *libraryKnowledgeSource) catalogScope(data []byte, a agentsdk.ConversationAuthority) string {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return ""
	}
	return conversationDigest([]any{k.runtimeID, a.WorkspaceID, a.UserID, value})
}
func (k *libraryKnowledgeSource) RevalidateKnowledge(ctx context.Context, saved agentsdk.ConversationKnowledgeResult, a agentsdk.ConversationAuthority) error {
	if saved.Operation == "libraries" {
		var args struct {
			After string `json:"after"`
			Limit int    `json:"limit"`
		}
		if json.Unmarshal([]byte(saved.Query), &args) != nil {
			return conversationFailure("unavailable", "knowledge_response_invalid")
		}
		if !a.Known || a.RuntimeID != k.runtimeID || saved.Provider != "agent_libraries" || saved.LibraryID != "" || saved.KBID != "" || saved.DocumentID != "" || len(saved.Citations) != 0 || saved.ScopeSHA256 == "" || saved.ScopeSHA256 != k.catalogScope(saved.Data, a) {
			return conversationFailure("forbidden", "knowledge_access_denied")
		}
		var page agentsdk.KnowledgeLibraryPage
		if len(saved.Data) > 64*1024 || json.Unmarshal(saved.Data, &page) != nil || len(page.Items) > 50 {
			return conversationFailure("unavailable", "knowledge_response_invalid")
		}
		seen := map[string]bool{}
		for _, item := range page.Items {
			if seen[item.ID] {
				return conversationFailure("unavailable", "knowledge_response_invalid")
			}
			seen[item.ID] = true
			if _, err := k.access(ctx, item.ID, a); err != nil {
				return err
			}
		}
		// This is a historical catalog snapshot. Renaming a library, adding
		// unrelated members or restoring a reader must not rewrite that snapshot
		// or prevent resuming it when every referenced library is readable again.
		return nil
	}
	if saved.LibraryID == "" {
		if k.legacy == nil {
			return conversationFailure("forbidden", "knowledge_access_denied")
		}
		return k.legacy.(agentsdk.ConversationKnowledgeSource).RevalidateKnowledge(ctx, saved, a)
	}
	binding, err := k.access(ctx, saved.LibraryID, a)
	if err != nil {
		return err
	}
	source, managed, err := k.managedSource(ctx, binding, a)
	if err != nil {
		return err
	}
	if managed {
		return k.revalidateManagedKnowledge(ctx, binding, source, saved, a)
	}
	if saved.Provider == managedKnowledgeProvider {
		return conversationFailure("forbidden", "knowledge_access_denied")
	}
	inner := saved
	inner.LibraryID = ""
	inner.Citations = append([]agentsdk.ConversationCitation(nil), saved.Citations...)
	for i := range inner.Citations {
		if inner.Citations[i].LibraryID != saved.LibraryID {
			return conversationFailure("unavailable", "knowledge_response_invalid")
		}
		inner.Citations[i].LibraryID = ""
	}
	if err = binding.Source.RevalidateKnowledge(ctx, inner, a); err != nil {
		return err
	}
	if _, err = k.access(ctx, saved.LibraryID, a); err != nil {
		return err
	}
	_, managed, err = k.managedSource(ctx, binding, a)
	if err != nil {
		return err
	}
	if managed {
		return conversationFailure("conflict", "knowledge_source_changed")
	}
	return nil
}

var _ agentsdk.ConversationLibraryKnowledgeSource = (*libraryKnowledgeSource)(nil)
