package application

import (
	"context"
	"slices"
	"sort"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type conversationModelSelectionContextKey struct{}

func selectedConversationModel(ctx context.Context) *agentsdk.ConversationModelSelection {
	selection, _ := ctx.Value(conversationModelSelectionContextKey{}).(*agentsdk.ConversationModelSelection)
	return selection
}

func (s *ConversationService) conversationModelByKey(key string) agentsdk.ConversationModel {
	if key == "" || key == "default" {
		return s.model
	}
	return s.options.AgentModels[key]
}

func canonicalConversationModelCapabilities(in agentsdk.ConversationModelCapabilities) agentsdk.ConversationModelCapabilities {
	out := in
	out.ReasoningEfforts = append([]string(nil), in.ReasoningEfforts...)
	for index := range out.ReasoningEfforts {
		out.ReasoningEfforts[index] = strings.ToLower(strings.TrimSpace(out.ReasoningEfforts[index]))
	}
	sort.Strings(out.ReasoningEfforts)
	return out
}

func validConversationReasoningEffort(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

func (s *ConversationService) conversationModelDescriptor(key string) (agentsdk.ConversationModelDescriptor, error) {
	if key == "" {
		key = "default"
	}
	model := s.conversationModelByKey(key)
	if model == nil {
		return agentsdk.ConversationModelDescriptor{}, conversationFailure("bad_request", "agent_model_unavailable")
	}
	descriptor := agentsdk.ConversationModelDescriptor{Key: key}
	if identified, ok := model.(agentsdk.ConversationAgentModel); ok {
		descriptor.Identity = identified.ConversationModelIdentity()
	}
	if declared, ok := model.(agentsdk.ConversationModelCapabilitiesProvider); ok {
		descriptor.Capabilities = canonicalConversationModelCapabilities(declared.ConversationModelCapabilities())
		descriptor.DefaultReasoningEffort = strings.ToLower(strings.TrimSpace(declared.ConversationModelDefaultReasoningEffort()))
	}
	if descriptor.Capabilities.ContextTokenLimit < 0 || descriptor.Capabilities.ContextTokenLimit > 0 && descriptor.Capabilities.ContextTokenLimit < 4096 {
		return agentsdk.ConversationModelDescriptor{}, conversationFailure("bad_request", "agent_model_capabilities_invalid")
	}
	seen := map[string]bool{}
	for _, effort := range descriptor.Capabilities.ReasoningEfforts {
		if !validConversationReasoningEffort(effort) || seen[effort] {
			return agentsdk.ConversationModelDescriptor{}, conversationFailure("bad_request", "agent_model_capabilities_invalid")
		}
		seen[effort] = true
	}
	if descriptor.DefaultReasoningEffort != "" && !seen[descriptor.DefaultReasoningEffort] {
		return agentsdk.ConversationModelDescriptor{}, conversationFailure("bad_request", "agent_model_capabilities_invalid")
	}
	return descriptor, nil
}

func conversationModelDescriptorSupports(descriptor agentsdk.ConversationModelDescriptor, effort string) bool {
	return effort == "" || slices.Contains(descriptor.Capabilities.ReasoningEfforts, effort)
}

func (s *ConversationService) resolveConversationModelSelection(request *agentsdk.ConversationModelRequestSelection, fallbackKey, fallbackEffort string) (*agentsdk.ConversationModelSelection, error) {
	key, effort := fallbackKey, fallbackEffort
	if request != nil {
		if strings.TrimSpace(request.Key) != "" {
			key = strings.TrimSpace(request.Key)
		}
		effort = strings.ToLower(strings.TrimSpace(request.ReasoningEffort))
	}
	if key == "" {
		key = "default"
	}
	descriptor, err := s.conversationModelDescriptor(key)
	if err != nil {
		return nil, err
	}
	if effort == "" {
		effort = descriptor.DefaultReasoningEffort
	}
	if effort != "" && (!validConversationReasoningEffort(effort) || !conversationModelDescriptorSupports(descriptor, effort)) {
		return nil, conversationFailure("bad_request", "reasoning_effort_unsupported")
	}
	return &agentsdk.ConversationModelSelection{Key: key, ReasoningEffort: effort, Identity: descriptor.Identity, Capabilities: descriptor.Capabilities}, nil
}

func (s *ConversationService) selectConversationTaskModel(ctx context.Context, selection *agentsdk.ConversationModelSelection) (context.Context, error) {
	if selection == nil {
		return ctx, nil
	}
	descriptor, err := s.conversationModelDescriptor(selection.Key)
	if err != nil {
		return ctx, conversationFailure("conflict", "model_changed")
	}
	if descriptor.Identity != selection.Identity || conversationDigest(descriptor.Capabilities) != conversationDigest(selection.Capabilities) || !conversationModelDescriptorSupports(descriptor, selection.ReasoningEffort) {
		return ctx, conversationFailure("conflict", "model_changed")
	}
	copy := *selection
	copy.Capabilities = canonicalConversationModelCapabilities(selection.Capabilities)
	return context.WithValue(ctx, conversationModelSelectionContextKey{}, &copy), nil
}

func (s *ConversationService) currentConversationModelDescriptor(ctx context.Context) (agentsdk.ConversationModelDescriptor, error) {
	if selection := selectedConversationModel(ctx); selection != nil {
		return agentsdk.ConversationModelDescriptor{Key: selection.Key, Identity: selection.Identity, Capabilities: canonicalConversationModelCapabilities(selection.Capabilities), DefaultReasoningEffort: selection.ReasoningEffort}, nil
	}
	if agent := selectedConversationAgent(ctx); agent != nil {
		return agentsdk.ConversationModelDescriptor{Key: agent.ModelKey, Identity: agent.ModelIdentity, Capabilities: canonicalConversationModelCapabilities(agent.ModelCapabilities), DefaultReasoningEffort: agent.ReasoningEffort}, nil
	}
	return s.conversationModelDescriptor("default")
}

func (s *ConversationService) conversationContextLimit(ctx context.Context) int {
	limit := s.options.ContextBytes
	descriptor, err := s.currentConversationModelDescriptor(ctx)
	if err == nil && descriptor.Capabilities.ContextTokenLimit > 0 {
		limit = min(limit, descriptor.Capabilities.ContextTokenLimit)
	}
	if lifecycle, ok := ctx.Value(conversationLifecycleContextLimitKey{}).(int); ok && lifecycle > 0 {
		limit = min(limit, lifecycle)
	}
	return limit
}
