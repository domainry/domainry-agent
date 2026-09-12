package application

import (
	"context"
	"encoding/json"
	"fmt"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type attachmentKnowledgeHost struct {
	base    agentsdk.ConversationToolHost
	service *ConversationService
}

func attachmentKnowledgeTool(key string) (agentsdk.ConversationToolDefinition, bool) {
	for _, d := range agentsdk.AttachmentConversationTools() {
		if d.Key == key {
			return d, true
		}
	}
	return agentsdk.ConversationToolDefinition{}, false
}
func privateRemoteAttachmentCall(call agentsdk.ConversationToolCall) bool {
	_, ok := attachmentKnowledgeTool(call.Name)
	return ok
}

type attachmentKnowledgeArguments struct {
	Query        string `json:"query"`
	AttachmentID string `json:"attachment_id"`
}

func attachmentToolArguments(in agentsdk.ConversationToolRequest) (attachmentKnowledgeArguments, error) {
	var args attachmentKnowledgeArguments
	d, ok := attachmentKnowledgeTool(in.Call.Name)
	if !ok || conversationDigest(d) != conversationDigest(in.Definition) {
		return args, conversationFailure("conflict", "tool_changed")
	}
	schema, err := compileConversationSchema(d.InputSchema)
	if err != nil || validateToolJSON(schema, []byte(in.Call.Arguments)) != nil {
		return args, conversationFailure("bad_request", "arguments_invalid")
	}
	err = json.Unmarshal([]byte(in.Call.Arguments), &args)
	return args, err
}
func (s *ConversationService) authorizeAttachmentKnowledgeTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	var denied agentsdk.ConversationToolAuthorization
	d, ok := attachmentKnowledgeTool(in.Definition.Key)
	if !ok || conversationDigest(d) != conversationDigest(in.Definition) || s.options.PersonalAuthorizer == nil {
		return denied, nil
	}
	bound := false
	for _, b := range s.options.AttachmentKnowledge {
		bound = bound || b.WorkspaceID == in.Authority.WorkspaceID
	}
	if !bound {
		return denied, nil
	}
	if _, err := s.attachmentAccess(ctx, "attachments_download", in.Authority); err != nil {
		return denied, nil
	}
	if in.ConversationID != "" {
		parent, err := s.repo.Get(ctx, in.ConversationID, in.Authority)
		if err != nil {
			return denied, err
		}
		if parent.Archived {
			return denied, nil
		}
	}
	return s.options.PersonalAuthorizer.AuthorizeConversationTool(ctx, in)
}
func (h *attachmentKnowledgeHost) ConversationTools(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	tools, err := h.base.ConversationTools(ctx, a)
	if err != nil {
		return nil, err
	}
	for _, tool := range tools {
		if _, ok := attachmentKnowledgeTool(tool.Key); ok {
			return nil, fmt.Errorf("attachment tools are already registered by the host")
		}
	}
	tools = append([]agentsdk.ConversationToolDefinition(nil), tools...)
	for _, d := range agentsdk.AttachmentConversationTools() {
		auth, err := h.service.authorizeAttachmentKnowledgeTool(ctx, agentsdk.ConversationToolRequest{Authority: a, Definition: d})
		if err != nil {
			return nil, err
		}
		if auth.Granted && !auth.ConfirmationRequired {
			tools = append(tools, d)
		}
	}
	return tools, nil
}
func (h *attachmentKnowledgeHost) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	if _, ok := attachmentKnowledgeTool(in.Definition.Key); ok {
		return h.service.authorizeAttachmentKnowledgeTool(ctx, in)
	}
	return h.base.AuthorizeConversationTool(ctx, in)
}
func (h *attachmentKnowledgeHost) InvokeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	if _, ok := attachmentKnowledgeTool(in.Definition.Key); !ok {
		return h.base.InvokeConversationTool(ctx, in)
	}
	auth, err := h.service.authorizeAttachmentKnowledgeTool(ctx, in)
	if err != nil || !auth.Granted || auth.ConfirmationRequired {
		return personalToolFailure("tool_access_denied"), nil
	}
	args, err := attachmentToolArguments(in)
	if err != nil {
		return personalToolFailure("arguments_invalid"), nil
	}
	op := "search"
	if in.Call.Name == "attachment_read" {
		op = "fetch"
	}
	out, err := h.service.attachmentKnowledge(ctx, in.ConversationID, op, args.Query, args.AttachmentID, in.Authority)
	if err != nil {
		return personalToolFailure(conversationModelFailureCode(err, "knowledge_failed")), nil
	}
	auth, err = h.service.authorizeAttachmentKnowledgeTool(ctx, in)
	if err != nil || !auth.Granted || auth.ConfirmationRequired {
		return personalToolFailure("tool_access_denied"), nil
	}
	return personalToolResult(out)
}
func (h *attachmentKnowledgeHost) ReconcileConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	if _, ok := attachmentKnowledgeTool(in.Definition.Key); ok {
		return h.InvokeConversationTool(ctx, in)
	}
	return h.base.ReconcileConversationTool(ctx, in)
}
func (h *attachmentKnowledgeHost) AuthorizeConversationInteraction(ctx context.Context, a agentsdk.ConversationAuthority, interaction agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	if policy, ok := h.base.(agentsdk.ConversationInteractionAuthorizer); ok {
		return policy.AuthorizeConversationInteraction(ctx, a, interaction)
	}
	return agentsdk.ConversationToolAuthorization{}, conversationFailure("unavailable", "interaction_unavailable")
}
func (h *attachmentKnowledgeHost) AuthorizeConversationToolResult(ctx context.Context, in agentsdk.ConversationToolRequest, result agentsdk.ConversationToolResult) error {
	if privateRemoteAttachmentCall(in.Call) {
		return h.service.authorizeAttachmentKnowledgeResult(ctx, in, result)
	}
	if policy, ok := h.base.(agentsdk.ConversationToolResultAuthorizer); ok {
		return policy.AuthorizeConversationToolResult(ctx, in, result)
	}
	return nil
}
