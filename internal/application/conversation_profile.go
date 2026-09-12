package application

import (
	"context"
	"fmt"
	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/definition"
	"time"
)

func (s *ConversationService) configureProfile() error {
	if s.options.AssembleTools != nil && s.options.ToolHost != nil {
		h, err := s.options.AssembleTools(&assemblyConfirmationHost{ConversationToolHost: s.options.ToolHost, service: s})
		if err != nil {
			return err
		}
		s.options.ToolHost = &extensionInteractionHost{ConversationToolHost: h, original: s.options.ToolHost}
	}
	if s.options.Agent == nil {
		return nil
	}
	available := append(sdk.PersonalConversationTools(), sdk.ArtifactConversationTools()...)
	available = append(available, sdk.KnowledgeConversationTools()...)
	available = append(available, sdk.AttachmentConversationTools()...)
	available = append(available, sdk.KnowledgeLibraryCatalogTool(), sdk.KnowledgeExtractionTool())
	available = append(available, sdk.BusinessConversationTools()...)
	available = append(available, sdk.BusinessRelationConversationTools()...)
	available = append(available, sdk.BusinessActionConversationTools()...)
	available = append(available, sdk.BusinessWorkflowConversationTools()...)
	available = append(available, s.options.ToolDefinitions...)
	keys := []string{}
	for _, d := range available {
		keys = append(keys, d.Key)
	}
	p, err := definition.CompileProfile(*s.options.Agent, s.options.Skills, keys)
	if err != nil {
		return err
	}
	// Copy trusted configuration; mutations by the caller cannot change a live Agent.
	s.profile = &p
	if s.options.ToolHost != nil {
		allowed := map[string]bool{}
		for _, k := range p.Tools {
			allowed[k] = true
		}
		s.options.ToolHost = &profileToolHost{base: s.options.ToolHost, allowed: allowed}
	}
	l := p.Limits
	if l.TimeoutSeconds > 0 {
		s.options.RunTimeout = min(s.options.RunTimeout, time.Duration(l.TimeoutSeconds)*time.Second)
	}
	if l.MaxSteps > 0 {
		s.options.MaxSteps = min(s.options.MaxSteps, l.MaxSteps)
	}
	if l.MaxToolCalls > 0 {
		s.options.MaxToolCalls = min(s.options.MaxToolCalls, l.MaxToolCalls)
	}
	if l.MaxInputBytes > 0 {
		s.options.MaxInputBytes = min(s.options.MaxInputBytes, l.MaxInputBytes)
	}
	if l.MaxOutputBytes > 0 {
		s.options.MaxOutputBytes = min(s.options.MaxOutputBytes, l.MaxOutputBytes)
	}
	return nil
}

type profileToolHost struct {
	base    sdk.ConversationToolHost
	allowed map[string]bool
}

func (h *profileToolHost) ConversationTools(ctx context.Context, a sdk.ConversationAuthority) ([]sdk.ConversationToolDefinition, error) {
	defs, err := h.base.ConversationTools(ctx, a)
	out := []sdk.ConversationToolDefinition{}
	for _, d := range defs {
		if h.allowed[d.Key] {
			out = append(out, d)
		}
	}
	return out, err
}
func (h *profileToolHost) AuthorizeConversationTool(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	if !h.allowed[in.Definition.Key] {
		return sdk.ConversationToolAuthorization{}, nil
	}
	return h.base.AuthorizeConversationTool(ctx, in)
}
func (h *profileToolHost) InvokeConversationTool(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	if !h.allowed[in.Definition.Key] {
		return sdk.ConversationToolResult{}, fmt.Errorf("tool is not loaded by this Agent")
	}
	return h.base.InvokeConversationTool(ctx, in)
}
func (h *profileToolHost) ReconcileConversationTool(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	if !h.allowed[in.Definition.Key] {
		return sdk.ConversationToolResult{}, fmt.Errorf("tool is not loaded by this Agent")
	}
	return h.base.ReconcileConversationTool(ctx, in)
}
func (h *profileToolHost) AuthorizeConversationToolResult(ctx context.Context, in sdk.ConversationToolRequest, out sdk.ConversationToolResult) error {
	if !h.allowed[in.Definition.Key] {
		return conversationFailure("forbidden", "tool_access_denied")
	}
	if p, ok := h.base.(sdk.ConversationToolResultAuthorizer); ok {
		return p.AuthorizeConversationToolResult(ctx, in, out)
	}
	return nil
}
func (h *profileToolHost) AuthorizeConversationInteraction(ctx context.Context, a sdk.ConversationAuthority, in sdk.ConversationInteraction) (sdk.ConversationToolAuthorization, error) {
	if p, ok := h.base.(sdk.ConversationInteractionAuthorizer); ok {
		return p.AuthorizeConversationInteraction(ctx, a, in)
	}
	return sdk.ConversationToolAuthorization{}, conversationFailure("unavailable", "interaction_unavailable")
}

type extensionInteractionHost struct {
	sdk.ConversationToolHost
	original sdk.ConversationToolHost
}

func (h *extensionInteractionHost) AuthorizeConversationInteraction(ctx context.Context, a sdk.ConversationAuthority, in sdk.ConversationInteraction) (sdk.ConversationToolAuthorization, error) {
	if p, ok := h.original.(sdk.ConversationInteractionAuthorizer); ok {
		return p.AuthorizeConversationInteraction(ctx, a, in)
	}
	return sdk.ConversationToolAuthorization{}, conversationFailure("unavailable", "interaction_unavailable")
}
func (h *extensionInteractionHost) AuthorizeConversationToolResult(ctx context.Context, in sdk.ConversationToolRequest, out sdk.ConversationToolResult) error {
	if p, ok := h.ConversationToolHost.(sdk.ConversationToolResultAuthorizer); ok {
		return p.AuthorizeConversationToolResult(ctx, in, out)
	}
	return nil
}
