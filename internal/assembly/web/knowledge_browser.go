package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/telemetry"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

var knowledgeBrowserLibraryID = regexp.MustCompile(`^lib_[a-f0-9]{32}$`)

// KnowledgeBrowserRoutes exposes the configured read-only knowledge source to
// the authenticated product UI. It uses the same live Identity permission as
// the conversation tools; the browser cannot choose credentials or source IDs.
func (h *Host) KnowledgeBrowserRoutes() map[string]http.Handler {
	if h == nil || h.Agent == nil {
		return nil
	}
	handler := http.HandlerFunc(h.handleKnowledgeBrowser)
	return map[string]http.Handler{
		"GET /app/product/knowledge/search":                 handler,
		"GET /app/product/knowledge/documents/{documentID}": handler,
	}
}

func (h *Host) handleKnowledgeBrowser(w http.ResponseWriter, r *http.Request) {
	current, ok := identitysdk.RequestIdentityFromContext(r.Context())
	if !ok || !current.Principal.Known {
		writeKnowledgeBrowserError(w, http.StatusUnauthorized, "agent.web.login_required")
		return
	}
	binding, ok := h.Agent.(agentsdk.ConversationBinding)
	if !ok || binding.Conversations() == nil {
		writeKnowledgeBrowserError(w, http.StatusServiceUnavailable, "agent.conversation.knowledge_unavailable")
		return
	}
	source, ok := binding.Conversations().(agentsdk.ConversationKnowledgeSource)
	if !ok {
		writeKnowledgeBrowserError(w, http.StatusServiceUnavailable, "agent.conversation.knowledge_unavailable")
		return
	}
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: h.runtimeID, WorkspaceID: current.Principal.WorkspaceID, UserID: current.Principal.UserID, RoleKey: current.Principal.RoleKey}
	libraryID := strings.TrimSpace(r.URL.Query().Get("library_id"))
	if libraryID != "" && !knowledgeBrowserLibraryID.MatchString(libraryID) {
		writeKnowledgeBrowserError(w, http.StatusBadRequest, "agent.conversation.library_invalid")
		return
	}
	operation := "knowledge_search"
	if r.PathValue("documentID") != "" {
		operation = "knowledge_read"
	}
	definition, found := knowledgeBrowserDefinition(operation)
	if !found {
		writeKnowledgeBrowserError(w, http.StatusServiceUnavailable, "agent.conversation.knowledge_unavailable")
		return
	}
	authorization, err := h.AuthorizeConversationTool(r.Context(), agentsdk.ConversationToolRequest{Authority: authority, Definition: definition})
	if err != nil {
		writeKnowledgeBrowserFailure(w, err)
		return
	}
	if !authorization.Granted || authorization.ConfirmationRequired {
		writeKnowledgeBrowserError(w, http.StatusForbidden, "agent.conversation.knowledge_access_denied")
		return
	}
	var result agentsdk.ConversationKnowledgeResult
	if operation == "knowledge_search" {
		query := strings.TrimSpace(r.URL.Query().Get("query"))
		if query == "" || len(query) > 4096 || !utf8.ValidString(query) || strings.ContainsRune(query, 0) {
			writeKnowledgeBrowserError(w, http.StatusBadRequest, "agent.conversation.knowledge_request_invalid")
			return
		}
		if libraryID == "" {
			result, err = source.SearchKnowledge(r.Context(), query, authority)
		} else if scoped, available := source.(agentsdk.ConversationLibraryKnowledgeSource); available {
			result, err = scoped.SearchLibraryKnowledge(r.Context(), libraryID, query, authority)
		} else {
			err = &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_unavailable"}
		}
	} else {
		documentID := strings.TrimSpace(r.PathValue("documentID"))
		if documentID == "" || len(documentID) > 4096 || !utf8.ValidString(documentID) || strings.ContainsRune(documentID, 0) {
			writeKnowledgeBrowserError(w, http.StatusBadRequest, "agent.conversation.knowledge_request_invalid")
			return
		}
		if libraryID == "" {
			result, err = source.ReadKnowledge(r.Context(), documentID, authority)
		} else if scoped, available := source.(agentsdk.ConversationLibraryKnowledgeSource); available {
			result, err = scoped.ReadLibraryKnowledge(r.Context(), libraryID, documentID, authority)
		} else {
			err = &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_unavailable"}
		}
	}
	if err != nil {
		writeKnowledgeBrowserFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}

func knowledgeBrowserDefinition(key string) (agentsdk.ConversationToolDefinition, bool) {
	for _, definition := range agentsdk.KnowledgeConversationTools() {
		if definition.Key == key {
			return definition, true
		}
	}
	return agentsdk.ConversationToolDefinition{}, false
}

func writeKnowledgeBrowserFailure(w http.ResponseWriter, err error) {
	var coded *agentsdk.Error
	if !errors.As(err, &coded) {
		writeKnowledgeBrowserError(w, http.StatusServiceUnavailable, "agent.conversation.knowledge_failed")
		return
	}
	status := http.StatusServiceUnavailable
	switch coded.Class {
	case "bad_request":
		status = http.StatusBadRequest
	case "forbidden":
		status = http.StatusForbidden
	case "not_found":
		status = http.StatusNotFound
	case "conflict":
		status = http.StatusConflict
	}
	writeKnowledgeBrowserError(w, status, coded.Code)
}

func writeKnowledgeBrowserError(w http.ResponseWriter, status int, code string) {
	telemetry.RecordHTTPStatusError(w, status, code)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
}
