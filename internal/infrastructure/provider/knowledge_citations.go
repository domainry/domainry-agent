package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// A host supplies an explicit, verified mapping; absent configuration never
// guesses field names. Live fetch responses can keep document metadata apart
// from the chunk array.
// Items is an RFC 6901 pointer into the response. Field pointers are relative to
// each selected object. Many distinguishes an array from one document object.
type KnowledgeCitationMapping struct {
	// Fetch only: metadata pointers resolve against this response object;
	// Excerpt still resolves against each item. An empty pointer means root.
	MetadataObject *string `json:"metadata_object,omitempty"`
	Items          string  `json:"items"`
	Many           bool    `json:"many,omitempty"`
	DocumentID     string  `json:"doc_id,omitempty"`
	Title          string  `json:"title,omitempty"`
	URL            string  `json:"url,omitempty"`
	Excerpt        string  `json:"excerpt,omitempty"`
}
type KnowledgeResponseMapping struct {
	Search *KnowledgeCitationMapping `json:"search,omitempty"`
	Fetch  *KnowledgeCitationMapping `json:"fetch,omitempty"`
}

func knowledgeMappingJSON(raw string) (*KnowledgeResponseMapping, error) {
	var mapping KnowledgeResponseMapping
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&mapping); err != nil {
		return nil, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("invalid trailing mapping JSON")
	}
	return &mapping, validateKnowledgeMapping(&mapping)
}

func citationPointerParts(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if len(pointer) > 1024 || !utf8.ValidString(pointer) || pointer[0] != '/' || strings.ContainsRune(pointer, 0) {
		return nil, fmt.Errorf("invalid knowledge citation pointer")
	}
	parts := strings.Split(pointer[1:], "/")
	if len(parts) > 32 {
		return nil, fmt.Errorf("knowledge citation pointer too deep")
	}
	for i, p := range parts {
		var out strings.Builder
		for j := 0; j < len(p); j++ {
			if p[j] != '~' {
				out.WriteByte(p[j])
				continue
			}
			j++
			if j >= len(p) || (p[j] != '0' && p[j] != '1') {
				return nil, fmt.Errorf("invalid knowledge citation escape")
			}
			if p[j] == '0' {
				out.WriteByte('~')
			} else {
				out.WriteByte('/')
			}
		}
		parts[i] = out.String()
	}
	return parts, nil
}
func citationPointer(value any, pointer string) (any, bool) {
	parts, err := citationPointerParts(pointer)
	if err != nil {
		return nil, false
	}
	for _, part := range parts {
		switch node := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = node[part]
			if !ok {
				return nil, false
			}
		case []any:
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 || n >= len(node) || strconv.Itoa(n) != part {
				return nil, false
			}
			value = node[n]
		default:
			return nil, false
		}
	}
	return value, true
}
func validateKnowledgeMapping(mapping *KnowledgeResponseMapping) error {
	if mapping == nil {
		return nil
	}
	for operation, m := range map[string]*KnowledgeCitationMapping{"search": mapping.Search, "fetch": mapping.Fetch} {
		if m == nil {
			continue
		}
		if operation == "search" && m.DocumentID == "" {
			return fmt.Errorf("search citation mapping requires document ID")
		}
		if m.MetadataObject != nil {
			if operation != "fetch" {
				return fmt.Errorf("shared citation metadata is only supported for fetch")
			}
			if _, err := citationPointerParts(*m.MetadataObject); err != nil {
				return err
			}
		}
		for _, pointer := range []string{m.Items, m.DocumentID, m.Title, m.URL, m.Excerpt} {
			if _, err := citationPointerParts(pointer); err != nil {
				return err
			}
		}
	}
	return nil
}
func citationText(item any, pointer string, limit int) (string, error) {
	if pointer == "" {
		return "", nil
	}
	value, ok := citationPointer(item, pointer)
	if !ok || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) || strings.ContainsRune(text, 0) || len(text) > limit {
		return "", knowledgeFailure("citation_mapping_invalid")
	}
	return text, nil
}

func (k *Knowledge) citations(evidence agentsdk.ConversationKnowledgeResult, data any) ([]agentsdk.ConversationCitation, error) {
	if k.config.ResponseMapping == nil {
		return nil, nil
	}
	mapping := k.config.ResponseMapping.Search
	if evidence.Operation == "fetch" {
		mapping = k.config.ResponseMapping.Fetch
	}
	if mapping == nil {
		return nil, nil
	}
	selected, ok := citationPointer(data, mapping.Items)
	if !ok {
		return nil, knowledgeFailure("citation_mapping_invalid")
	}
	items := []any{selected}
	if mapping.Many {
		items, ok = selected.([]any)
		if !ok {
			return nil, knowledgeFailure("citation_mapping_invalid")
		}
	}
	if len(items) > 50 {
		return nil, knowledgeFailure("citation_limit_exceeded")
	}
	var metadata any
	if mapping.MetadataObject != nil {
		metadata, ok = citationPointer(data, *mapping.MetadataObject)
		if !ok {
			return nil, knowledgeFailure("citation_mapping_invalid")
		}
		if _, ok := metadata.(map[string]any); !ok {
			return nil, knowledgeFailure("citation_mapping_invalid")
		}
	}
	out := make([]agentsdk.ConversationCitation, 0, len(items))
	dataHash := sha256.Sum256(evidence.Data)
	for i, item := range items {
		if _, ok := item.(map[string]any); !ok {
			return nil, knowledgeFailure("citation_mapping_invalid")
		}
		fields := item
		if mapping.MetadataObject != nil {
			fields = metadata
		}
		docID, err := citationText(fields, mapping.DocumentID, 4096)
		if err != nil {
			return nil, err
		}
		if evidence.Operation == "fetch" && mapping.DocumentID == "" {
			docID = evidence.DocumentID
		}
		if strings.TrimSpace(docID) == "" || (evidence.Operation == "fetch" && docID != evidence.DocumentID) {
			return nil, knowledgeFailure("citation_mapping_invalid")
		}
		title, err := citationText(fields, mapping.Title, 512)
		if err != nil {
			return nil, err
		}
		link, err := citationText(fields, mapping.URL, 4096)
		if err != nil {
			return nil, err
		}
		if link != "" {
			parsed, err := url.Parse(link)
			if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || strings.ContainsAny(link, "\r\n\t") {
				link = ""
			}
		}
		excerpt, err := citationText(item, mapping.Excerpt, 512*1024)
		if err != nil {
			return nil, err
		}
		truncated := len(excerpt) > 2048
		if truncated {
			end := 2048
			for !utf8.RuneStart(excerpt[end]) {
				end--
			}
			excerpt = excerpt[:end]
		}
		citation := agentsdk.ConversationCitation{Provider: evidence.Provider, KBID: evidence.KBID, Operation: evidence.Operation, DocumentID: docID, Title: title, URL: link, Excerpt: excerpt, ExcerptTruncated: truncated}
		raw, _ := json.Marshal([]any{evidence.ScopeSHA256, hex.EncodeToString(dataHash[:]), mapping.Items, i, citation})
		sum := sha256.Sum256(raw)
		citation.ID = "kc_" + hex.EncodeToString(sum[:16])
		out = append(out, citation)
	}
	encoded, _ := json.Marshal(out)
	if len(encoded) > 192*1024 {
		return nil, knowledgeFailure("citation_limit_exceeded")
	}
	return out, nil
}
