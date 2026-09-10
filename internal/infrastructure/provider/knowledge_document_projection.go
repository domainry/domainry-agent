package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	httpapi "github.com/domainry/domainry-connectors/providers/knowledge_base/http_api"
)

func (k *Knowledge) KnowledgeDocumentSourceIdentity() string {
	u, _ := url.Parse(k.config.BaseURL)
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80" {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	raw, _ := json.Marshal([]string{"knowledge_document_source.v1", strings.TrimRight(u.String(), "/"), k.config.TeamID, k.config.KBID})
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (k *Knowledge) KnowledgeDocumentManagementReady() error {
	if !k.config.DocumentManagement || k.config.PermissionIDs != nil || k.config.AuthorizeWorkspace != nil {
		return knowledgeFailure("document_management_unavailable")
	}
	return k.documentProjectionReady()
}
func (k *Knowledge) documentProjectionReady() error {
	m := k.config.ResponseMapping
	if m == nil || m.Search == nil || m.Fetch == nil || m.Search.Excerpt == "" || m.Fetch.Excerpt == "" {
		return knowledgeFailure("document_mapping_required")
	}
	return nil
}
func (k *Knowledge) SearchKnowledgeDocumentPassages(ctx context.Context, q string, a agentsdk.ConversationAuthority) ([]agentsdk.KnowledgeDocumentPassage, error) {
	if e := k.documentProjectionReady(); e != nil {
		return nil, e
	}
	raw, e := k.Search(ctx, q, a)
	if e != nil {
		return nil, e
	}
	return k.documentPassages(raw, k.config.ResponseMapping.Search, "")
}
func (k *Knowledge) ReadKnowledgeDocumentPassages(ctx context.Context, id string, a agentsdk.ConversationAuthority) ([]agentsdk.KnowledgeDocumentPassage, error) {
	if e := k.documentProjectionReady(); e != nil {
		return nil, e
	}
	raw, e := k.Fetch(ctx, id, a)
	if e != nil {
		return nil, e
	}
	return k.documentPassages(raw, k.config.ResponseMapping.Fetch, id)
}
func (k *Knowledge) documentPassages(raw []byte, m *KnowledgeCitationMapping, id string) ([]agentsdk.KnowledgeDocumentPassage, error) {
	var envelope httpapi.Output
	if json.Unmarshal(raw, &envelope) != nil || envelope.Provider != httpapi.ProviderKey || envelope.KBID != k.config.KBID {
		return nil, knowledgeFailure("response_invalid")
	}
	var data any
	d := json.NewDecoder(bytes.NewReader(envelope.Result))
	d.UseNumber()
	if d.Decode(&data) != nil {
		return nil, knowledgeFailure("response_invalid")
	}
	selected, ok := citationPointer(data, m.Items)
	if !ok {
		return nil, knowledgeFailure("citation_mapping_invalid")
	}
	items := []any{selected}
	if m.Many {
		items, ok = selected.([]any)
		if !ok {
			return nil, knowledgeFailure("citation_mapping_invalid")
		}
	}
	if len(items) > 1000 {
		return nil, knowledgeFailure("document_passage_limit")
	}
	var metadata any
	if m.MetadataObject != nil {
		metadata, ok = citationPointer(data, *m.MetadataObject)
		if !ok {
			return nil, knowledgeFailure("citation_mapping_invalid")
		}
		if _, ok = metadata.(map[string]any); !ok {
			return nil, knowledgeFailure("citation_mapping_invalid")
		}
	}
	out := make([]agentsdk.KnowledgeDocumentPassage, 0, len(items))
	for _, item := range items {
		if _, ok := item.(map[string]any); !ok {
			return nil, knowledgeFailure("citation_mapping_invalid")
		}
		fields := item
		if m.MetadataObject != nil {
			fields = metadata
		}
		doc, e := citationText(fields, m.DocumentID, 4096)
		if e != nil {
			return nil, e
		}
		if id != "" && m.DocumentID == "" {
			doc = id
		}
		if strings.TrimSpace(doc) == "" || id != "" && doc != id {
			return nil, knowledgeFailure("citation_mapping_invalid")
		}
		title, e := citationText(fields, m.Title, 512)
		if e != nil {
			return nil, e
		}
		link, e := citationText(fields, m.URL, 4096)
		if e != nil {
			return nil, e
		}
		if link != "" {
			u, e := url.Parse(link)
			if e != nil || u.Hostname() == "" || u.User != nil || u.Scheme != "https" && u.Scheme != "http" || strings.ContainsAny(link, "\r\n\t") {
				link = ""
			}
		}
		content, e := citationText(item, m.Excerpt, 512*1024)
		if e != nil {
			return nil, e
		}
		out = append(out, agentsdk.KnowledgeDocumentPassage{DocumentID: doc, Title: title, URL: link, Content: content})
	}
	return out, nil
}

var _ agentsdk.ManagedKnowledgeDocumentSource = (*Knowledge)(nil)
