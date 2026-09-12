package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

// Compose knowledge with the existing host; business/personal implementations
// and their authorization remain owned by that host.
type knowledgeConversationHost struct {
	base       agentsdk.ConversationToolHost
	authorizer agentsdk.ConversationToolAuthorizer
	source     agentsdk.ConversationKnowledgeSource
	repo       persistence.ConversationRepository
}

func knowledgeTool(key string) (agentsdk.ConversationToolDefinition, bool) {
	for _, d := range append(agentsdk.KnowledgeConversationTools(), agentsdk.KnowledgeLibraryCatalogTool(), agentsdk.KnowledgeExtractionTool()) {
		if d.Key == key {
			return d, true
		}
	}
	return agentsdk.ConversationToolDefinition{}, false
}

func knownKnowledgeDefinition(d agentsdk.ConversationToolDefinition) bool {
	definitions := append(agentsdk.KnowledgeConversationTools(), agentsdk.LibraryKnowledgeConversationTools()...)
	definitions = append(definitions, agentsdk.KnowledgeExtractionTool(), agentsdk.LegacyKnowledgeExtractionTool())
	definitions = append(definitions, agentsdk.HistoricalRemoteKnowledgeTools()...)
	for _, current := range definitions {
		if conversationDigest(d) == conversationDigest(current) {
			return true
		}
	}
	return false
}
func (h *knowledgeConversationHost) definitions() []agentsdk.ConversationToolDefinition {
	definitions := []agentsdk.ConversationToolDefinition{}
	if h.source != nil {
		definitions = agentsdk.KnowledgeConversationTools()
	}
	if _, ok := h.source.(agentsdk.ConversationLibraryKnowledgeSource); ok {
		definitions = agentsdk.LibraryKnowledgeConversationTools()
	}
	if _, ok := h.source.(agentsdk.KnowledgeExtractionContentSource); ok {
		definitions = append(definitions, agentsdk.KnowledgeExtractionTool())
	}
	return definitions
}

func (h *knowledgeConversationHost) definition(key string) (agentsdk.ConversationToolDefinition, bool) {
	for _, d := range h.definitions() {
		if d.Key == key {
			return d, true
		}
	}
	return agentsdk.ConversationToolDefinition{}, false
}

type knowledgeArguments struct {
	Query        string `json:"query"`
	DocID        string `json:"doc_id"`
	AttachmentID string `json:"attachment_id"`
	LibraryID    string `json:"library_id"`
	After        string `json:"after"`
	Limit        int    `json:"limit"`
}

func (h *knowledgeConversationHost) ConversationTools(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	tools, err := h.base.ConversationTools(ctx, a)
	if err != nil {
		return nil, err
	}
	for _, tool := range tools {
		if _, reserved := knowledgeTool(tool.Key); reserved {
			return nil, fmt.Errorf("knowledge tools are already registered by the host")
		}
	}
	tools = append([]agentsdk.ConversationToolDefinition(nil), tools...)
	for _, d := range h.definitions() {
		auth, err := h.AuthorizeConversationTool(ctx, agentsdk.ConversationToolRequest{Authority: a, Definition: d})
		if err != nil {
			return nil, err
		}
		if auth.Granted {
			tools = append(tools, d)
		}
	}
	return tools, nil
}

func (h *knowledgeConversationHost) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	d, knowledge := h.definition(in.Definition.Key)
	if !knowledge {
		return h.base.AuthorizeConversationTool(ctx, in)
	}
	if !in.Authority.Known || conversationDigest(d) != conversationDigest(in.Definition) {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	if in.ConversationID != "" {
		if _, err := h.repo.Get(ctx, in.ConversationID, in.Authority); err != nil {
			return agentsdk.ConversationToolAuthorization{}, err
		}
	}
	return h.authorizer.AuthorizeConversationTool(ctx, in)
}

func (h *knowledgeConversationHost) InvokeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	d, knowledge := h.definition(in.Definition.Key)
	if !knowledge {
		return h.base.InvokeConversationTool(ctx, in)
	}
	auth, err := h.AuthorizeConversationTool(ctx, in)
	if err != nil || !auth.Granted || auth.ConfirmationRequired || in.Call.Name != d.Key {
		return personalToolFailure("tool_access_denied"), nil
	}
	schema, err := compileConversationSchema(d.InputSchema)
	if err != nil || validateToolJSON(schema, []byte(in.Call.Arguments)) != nil {
		return personalToolFailure("arguments_invalid"), nil
	}
	if d.Key == "knowledge_extract" {
		return h.invokeExtraction(ctx, in)
	}
	var args knowledgeArguments
	_ = json.Unmarshal([]byte(in.Call.Arguments), &args)
	var evidence agentsdk.ConversationKnowledgeResult
	if h.source == nil {
		return personalToolFailure("knowledge_unavailable"), nil
	} else if scoped, ok := h.source.(agentsdk.ConversationLibraryKnowledgeSource); ok && (d.Key == "knowledge_libraries" || args.LibraryID != "") {
		switch d.Key {
		case "knowledge_libraries":
			evidence, err = scoped.ListKnowledgeLibraries(ctx, args.After, args.Limit, in.Authority)
		case "knowledge_search":
			evidence, err = scoped.SearchLibraryKnowledge(ctx, args.LibraryID, args.Query, in.Authority)
		case "knowledge_read":
			evidence, err = scoped.ReadLibraryKnowledge(ctx, args.LibraryID, args.DocID, in.Authority)
		}
	} else if d.Key == "knowledge_search" {
		evidence, err = h.source.SearchKnowledge(ctx, args.Query, in.Authority)
	} else if d.Key == "knowledge_read" {
		evidence, err = h.source.ReadKnowledge(ctx, args.DocID, in.Authority)
	} else {
		return personalToolFailure("knowledge_unavailable"), nil
	}

	if err != nil {
		return personalToolFailure(conversationModelFailureCode(err, "knowledge_failed")), nil
	}
	return personalToolResult(evidence)
}

func (h *knowledgeConversationHost) ReconcileConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	if _, knowledge := knowledgeTool(in.Definition.Key); knowledge {
		return h.InvokeConversationTool(ctx, in) // read-only, naturally repeatable
	}
	return h.base.ReconcileConversationTool(ctx, in)
}

func (h *knowledgeConversationHost) AuthorizeConversationInteraction(ctx context.Context, a agentsdk.ConversationAuthority, interaction agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	if policy, ok := h.base.(agentsdk.ConversationInteractionAuthorizer); ok {
		return policy.AuthorizeConversationInteraction(ctx, a, interaction)
	}
	return agentsdk.ConversationToolAuthorization{}, conversationFailure("unavailable", "interaction_unavailable")
}

func (h *knowledgeConversationHost) AuthorizeConversationToolResult(ctx context.Context, in agentsdk.ConversationToolRequest, result agentsdk.ConversationToolResult) error {
	if privateAttachmentCall(in.Call) || retiredDocumentResult(result) {
		return conversationFailure("forbidden", "knowledge_source_retired")
	}
	if _, knowledge := knowledgeTool(in.Definition.Key); !knowledge {
		if policy, ok := h.base.(agentsdk.ConversationToolResultAuthorizer); ok {
			return policy.AuthorizeConversationToolResult(ctx, in, result)
		}
		return nil
	}
	if in.Call.Name == "knowledge_extract" {
		return h.authorizeExtractionResult(ctx, in, result)
	}
	var evidence agentsdk.ConversationKnowledgeResult
	var args knowledgeArguments
	if json.Unmarshal(result.Content, &evidence) != nil || json.Unmarshal([]byte(in.Call.Arguments), &args) != nil {
		return conversationFailure("conflict", "knowledge_response_invalid")
	}
	if h.source == nil {
		return conversationFailure("forbidden", "knowledge_access_denied")
	}
	if evidence.LibraryID != args.LibraryID ||
		in.Call.Name == "knowledge_search" && (evidence.Operation != "search" || evidence.Query != args.Query || evidence.DocumentID != "") ||
		in.Call.Name == "knowledge_read" && (evidence.Operation != "fetch" || evidence.DocumentID != args.DocID || evidence.Query != "") {
		return conversationFailure("conflict", "knowledge_response_invalid")
	}
	if args.LibraryID != "" || in.Call.Name == "knowledge_libraries" {
		if _, scoped := h.source.(agentsdk.ConversationLibraryKnowledgeSource); !scoped {
			return conversationFailure("forbidden", "knowledge_access_denied")
		}
	}
	if in.Call.Name == "knowledge_libraries" {
		expected, _ := json.Marshal(struct {
			After string `json:"after"`
			Limit int    `json:"limit"`
		}{args.After, args.Limit})
		if evidence.Operation != "libraries" || evidence.Query != string(expected) || evidence.DocumentID != "" || evidence.LibraryID != "" {
			return conversationFailure("conflict", "knowledge_response_invalid")
		}
	}
	return h.source.RevalidateKnowledge(ctx, evidence, in.Authority)
}

func (s *ConversationService) authorizeStoredToolResult(ctx context.Context, in agentsdk.ConversationToolRequest, result agentsdk.ConversationToolResult) error {
	if privateAttachmentCall(in.Call) || retiredDocumentResult(result) {
		return conversationFailure("forbidden", "knowledge_source_retired")
	}
	if result.Status != "completed" {
		return nil
	}
	if privateRemoteAttachmentCall(in.Call) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return s.authorizeAttachmentKnowledgeResult(ctx, in, result)
	}
	if policy, ok := s.options.ToolHost.(agentsdk.ConversationToolResultAuthorizer); ok {
		ctx, cancel := context.WithTimeout(ctx, time.Duration(in.Definition.TimeoutMillis)*time.Millisecond)
		defer cancel()
		return policy.AuthorizeConversationToolResult(ctx, in, result)
	}
	return nil
}
