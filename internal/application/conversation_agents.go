package application

import (
	"context"
	"fmt"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/definition"
	"sort"
	"strconv"
)

type conversationAgentContextKey struct{}
type conversationAgentCatalogKey struct{}

func selectedConversationAgent(ctx context.Context) *agentsdk.ConversationAgentSnapshot {
	v, _ := ctx.Value(conversationAgentContextKey{}).(*agentsdk.ConversationAgentSnapshot)
	return v
}

func conversationProfileAllows(ctx context.Context, key string, defaults map[string]bool) bool {
	if all, _ := ctx.Value(conversationAgentCatalogKey{}).(bool); all {
		return defaults == nil || defaults[key]
	}
	if agent := selectedConversationAgent(ctx); agent != nil {
		if defaults != nil && !defaults[key] {
			return false
		}
		for _, allowed := range agent.Profile.Tools {
			if allowed == key {
				return true
			}
		}
		return false
	}
	return defaults[key]
}

func (s *ConversationService) collaborationRepository() (persistence.ConversationCollaborationRepository, error) {
	repo, ok := s.repo.(persistence.ConversationCollaborationRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "collaboration_unavailable")
	}
	return repo, nil
}

func (s *ConversationService) conversationModel(ctx context.Context) agentsdk.ConversationModel {
	if agent := selectedConversationAgent(ctx); agent != nil && agent.ModelKey != "default" {
		return s.options.AgentModels[agent.ModelKey]
	}
	return s.model
}

func (s *ConversationService) defaultConversationAgent(ctx context.Context, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error) {
	ctx = context.WithValue(ctx, conversationAgentContextKey{}, (*agentsdk.ConversationAgentSnapshot)(nil))
	agent := agentsdk.ConversationAgent{ID: "default", Name: "默认 Agent", Instructions: conversationExecutionSystem, ModelKey: "default", Enabled: s.model != nil, MaxConcurrent: s.options.Workers, Revision: 1, Tools: []string{}, SkillKeys: []string{}}
	if s.options.Agent != nil {
		agent.Name, agent.Description, agent.Instructions = s.options.Agent.Name, s.options.Agent.Description, s.options.Agent.Instructions
		agent.Tools, agent.SkillKeys = append([]string{}, s.options.Agent.Tools...), append([]string{}, s.options.Agent.SkillKeys...)
	} else if s.options.ToolHost != nil {
		definitions, _, err := s.registeredExecutionCatalog(ctx, a)
		if err != nil {
			return agent, err
		}
		for _, d := range definitions {
			agent.Tools = append(agent.Tools, d.Key)
		}
	}
	return agent, nil
}

func (s *ConversationService) conversationAgent(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error) {
	if id == "" || id == "default" {
		return s.defaultConversationAgent(ctx, a)
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return agentsdk.ConversationAgent{}, err
	}
	return repo.ConversationAgent(ctx, id, a)
}

func (s *ConversationService) ConversationAgents(ctx context.Context, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgentPage, error) {
	page, err := s.conversationAgentDirectory(ctx, a)
	if err != nil {
		return page, err
	}
	match, err := s.matchConversationAgentDirectory(ctx, page, agentsdk.ConversationAgentMatchRequest{}, a)
	page.Availability = match.Items
	return page, err
}

func (s *ConversationService) conversationAgentDirectory(ctx context.Context, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgentPage, error) {
	out := agentsdk.ConversationAgentPage{Items: []agentsdk.ConversationAgent{}, Tools: []agentsdk.ConversationToolDefinition{}, Models: []string{"default"}, Complete: true}
	if err := s.authorizeCollaboration(ctx, "discover", nil, a); err != nil {
		return out, err
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	first, err := s.defaultConversationAgent(ctx, a)
	if err != nil {
		return out, err
	}
	items, err := repo.ConversationAgents(ctx, a)
	if err != nil {
		return out, err
	}
	out.Items = append([]agentsdk.ConversationAgent{first}, items...)
	if s.options.ToolHost != nil {
		out.Tools, _, err = s.executionCatalog(context.WithValue(ctx, conversationAgentCatalogKey{}, true), a)
	}
	for key := range s.options.AgentModels {
		if key != "default" {
			out.Models = append(out.Models, key)
		}
	}
	sort.Strings(out.Models)
	for _, d := range s.options.AgentDefinitions {
		out.Definitions = append(out.Definitions, agentsdk.ConversationAgentDefinition{Key: d.Key, Version: d.Version, Name: d.Name, Description: d.Description, Instructions: d.Instructions, Tools: append([]string{}, d.Tools...), SkillKeys: append([]string{}, d.SkillKeys...)})
	}
	for _, skill := range s.options.Skills {
		out.Skills = append(out.Skills, agentsdk.ConversationSkillSummary{Key: skill.Key, Version: skill.Version, Name: skill.Name, Description: skill.Description, AllowedTools: append([]string{}, skill.AllowedTools...)})
	}
	return out, err
}

func (s *ConversationService) WriteConversationAgent(ctx context.Context, id string, in agentsdk.ConversationAgentWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error) {
	var out agentsdk.ConversationAgent
	if err := s.authorizeCollaboration(ctx, "configure", nil, a); err != nil {
		return out, err
	}
	in.DefinitionVersion, in.DefinitionDigest = "", ""
	if in.DefinitionKey != "" {
		d := s.agentDefinition(in.DefinitionKey)
		if d == nil {
			return out, conversationFailure("bad_request", "agent_definition_unavailable")
		}
		in.DefinitionVersion, in.DefinitionDigest = d.Version, conversationDigest(d)
		in.Instructions, in.Tools, in.SkillKeys = d.Instructions, append([]string{}, d.Tools...), append([]string{}, d.SkillKeys...)
		if in.Description == "" {
			in.Description = d.Description
		}
	}
	if id == "default" || id != "" && !conversationKey(id) || !conversationKey(in.ClientID) || !conversationText(in.Name, 128, true) || !conversationText(in.Description, 2048, false) || !conversationText(in.Instructions, 32768, true) || in.MaxConcurrent < 1 || in.MaxConcurrent > 32 || len(in.Tools) > 128 || len(in.SkillKeys) > 32 || in.ExpectedRevision < 0 {
		return out, conversationFailure("bad_request", "agent_invalid")
	}
	if in.ModelKey == "" {
		in.ModelKey = "default"
	}
	if in.ModelKey != "default" && s.options.AgentModels[in.ModelKey] == nil {
		return out, conversationFailure("bad_request", "agent_model_unavailable")
	}
	catalog, _, err := s.registeredExecutionCatalog(context.WithValue(ctx, conversationAgentCatalogKey{}, true), a)
	if err != nil {
		return out, err
	}
	keys := []string{}
	for _, d := range catalog {
		keys = append(keys, d.Key)
	}
	spec := agentsdk.AgentSchema{Key: "configured", Version: "1", Name: in.Name, Instructions: in.Instructions, Tools: in.Tools, SkillKeys: in.SkillKeys}
	if _, err = definition.CompileProfile(spec, s.options.Skills, keys); err != nil {
		return out, conversationFailure("bad_request", "agent_profile_invalid")
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	return repo.WriteConversationAgent(ctx, id, in, a)
}

func (s *ConversationService) freezeConversationAgent(ctx context.Context, id string, a agentsdk.ConversationAuthority) (*agentsdk.ConversationAgentSnapshot, error) {
	agent, err := s.conversationAgent(ctx, id, a)
	if err != nil {
		return nil, err
	}
	if !agent.Enabled {
		return nil, conversationFailure("forbidden", "agent_disabled")
	}
	var capabilityDefinition *agentsdk.AgentSchema
	if agent.DefinitionKey != "" {
		capabilityDefinition = s.agentDefinition(agent.DefinitionKey)
		if capabilityDefinition == nil || capabilityDefinition.Version != agent.DefinitionVersion || conversationDigest(capabilityDefinition) != agent.DefinitionDigest {
			return nil, conversationFailure("conflict", "agent_definition_changed")
		}
	}
	snapshot := &agentsdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision, ModelKey: agent.ModelKey, Profile: agentsdk.AgentSchema{Key: agent.ID, Version: strconv.FormatInt(agent.Revision, 10), Name: agent.Name, Description: agent.Description, Instructions: agent.Instructions, Tools: append([]string{}, agent.Tools...), SkillKeys: append([]string{}, agent.SkillKeys...)}}
	for _, key := range agent.SkillKeys {
		found := false
		for _, skill := range s.options.Skills {
			if skill.Key == key {
				snapshot.Skills = append(snapshot.Skills, skill)
				found = true
				break
			}
		}
		if !found {
			return nil, conversationFailure("conflict", "agent_profile_changed")
		}
	}
	if agent.ID == "default" && s.options.Agent != nil {
		snapshot.Profile.Version = s.options.Agent.Version
		snapshot.Profile.ExecutionLimits = s.options.Agent.ExecutionLimits
	}
	if capabilityDefinition != nil {
		snapshot.Profile.ExecutionLimits = capabilityDefinition.ExecutionLimits
	}
	model := s.model
	if agent.ModelKey != "default" {
		model = s.options.AgentModels[agent.ModelKey]
	}
	agentModel, ok := model.(agentsdk.ConversationAgentModel)
	if !ok {
		return nil, conversationFailure("unavailable", "agent_model_unavailable")
	}
	snapshot.ModelIdentity = agentModel.ConversationModelIdentity()
	snapshot.Digest = conversationDigest(*snapshot)
	return snapshot, nil
}

func (s *ConversationService) agentDefinition(key string) *agentsdk.AgentSchema {
	for i := range s.options.AgentDefinitions {
		if s.options.AgentDefinitions[i].Key == key {
			return &s.options.AgentDefinitions[i]
		}
	}
	return nil
}

func (s *ConversationService) selectConversationAgent(ctx context.Context, snapshot *agentsdk.ConversationAgentSnapshot, a agentsdk.ConversationAuthority) (context.Context, error) {
	if snapshot == nil {
		return ctx, nil
	}
	copy := *snapshot
	copy.Digest = ""
	if snapshot.Digest == "" || conversationDigest(copy) != snapshot.Digest {
		return ctx, conversationFailure("conflict", "agent_snapshot_invalid")
	}
	current, err := s.conversationAgent(ctx, snapshot.ID, a)
	if err != nil {
		return ctx, err
	}
	if !current.Enabled {
		return ctx, conversationFailure("forbidden", "agent_disabled")
	}
	// Any configuration change requires an explicit new run. This includes
	// enabled-state changes so a disabled/re-enabled Agent cannot revive stale work.
	if current.Revision != snapshot.Revision {
		return ctx, conversationFailure("conflict", "agent_changed")
	}
	fresh, err := s.freezeConversationAgent(ctx, snapshot.ID, a)
	if err != nil {
		return ctx, err
	}
	if fresh.Digest != snapshot.Digest {
		return ctx, conversationFailure("conflict", "agent_changed")
	}
	ctx = context.WithValue(ctx, conversationAgentContextKey{}, snapshot)
	model, ok := s.conversationModel(ctx).(agentsdk.ConversationAgentModel)
	if !ok || model.ConversationModelIdentity() != snapshot.ModelIdentity {
		return ctx, conversationFailure("conflict", "model_changed")
	}
	return ctx, nil
}

func (s *ConversationService) conversationProfile(ctx context.Context) (*definition.Profile, error) {
	if selected := selectedConversationAgent(ctx); selected != nil {
		profile, err := definition.CompileProfile(selected.Profile, selected.Skills, selected.Profile.Tools)
		if err != nil {
			return nil, fmt.Errorf("invalid frozen Agent profile")
		}
		return &profile, nil
	}
	return s.profile, nil
}
