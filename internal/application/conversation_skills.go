package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/definition"
)

type skillToolHost struct {
	agentsdk.ConversationToolHost
	service *ConversationService
}

func selectedConversationSkills(ctx context.Context) []agentsdk.SkillSchema {
	snapshot := selectedConversationAgent(ctx)
	if snapshot == nil {
		return nil
	}
	return snapshot.Skills
}

func selectedConversationSkill(ctx context.Context, key, version string) (agentsdk.SkillSchema, bool) {
	for _, skill := range selectedConversationSkills(ctx) {
		if skill.Key == key && skill.Version == version {
			return skill, true
		}
	}
	return agentsdk.SkillSchema{}, false
}

func (h *skillToolHost) ConversationTools(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	out, err := h.ConversationToolHost.ConversationTools(ctx, a)
	if err != nil || len(selectedConversationSkills(ctx)) == 0 {
		return out, err
	}
	return append(out, agentsdk.ConversationSkillLoadTool()), nil
}

func (h *skillToolHost) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	if in.Definition.Key != "skill_load" {
		return h.ConversationToolHost.AuthorizeConversationTool(ctx, in)
	}
	if conversationDigest(in.Definition) != conversationDigest(agentsdk.ConversationSkillLoadTool()) || in.Call.Name != "skill_load" {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	var args agentsdk.ConversationSkillLoadRequest
	if json.Unmarshal([]byte(in.Call.Arguments), &args) != nil {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	skill, ok := selectedConversationSkill(ctx, args.Key, args.Version)
	if !ok {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	if args.ResourceKey != "" {
		found := false
		for _, resource := range skill.Resources {
			found = found || resource.Key == args.ResourceKey
		}
		if !found {
			return agentsdk.ConversationToolAuthorization{}, nil
		}
	}
	return agentsdk.ConversationToolAuthorization{Granted: true, Revision: conversationDigest(skill)}, nil
}

func (h *skillToolHost) InvokeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	if in.Definition.Key != "skill_load" {
		return h.ConversationToolHost.InvokeConversationTool(ctx, in)
	}
	auth, err := h.AuthorizeConversationTool(ctx, in)
	if err != nil {
		return agentsdk.ConversationToolResult{}, err
	}
	if !auth.Granted {
		return personalToolFailure("skill_unavailable"), nil
	}
	var args agentsdk.ConversationSkillLoadRequest
	_ = json.Unmarshal([]byte(in.Call.Arguments), &args)
	skill, _ := selectedConversationSkill(ctx, args.Key, args.Version)
	out := agentsdk.ConversationSkillLoadResult{Summary: agentsdk.ConversationSkillSummaryFor(skill)}
	if args.ResourceKey == "" {
		out.Instructions = skill.Instructions
		out.Workflow = append([]agentsdk.SkillWorkflowStep(nil), skill.Workflow...)
	} else {
		for _, resource := range skill.Resources {
			if resource.Key == args.ResourceKey {
				copy := resource
				out.Resource = &copy
				break
			}
		}
	}
	raw, err := json.Marshal(out)
	return agentsdk.ConversationToolResult{Status: "completed", Content: raw}, err
}

func (h *skillToolHost) ReconcileConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	if in.Definition.Key == "skill_load" {
		return h.InvokeConversationTool(ctx, in)
	}
	return h.ConversationToolHost.ReconcileConversationTool(ctx, in)
}

func (h *skillToolHost) AuthorizeConversationToolResult(ctx context.Context, in agentsdk.ConversationToolRequest, out agentsdk.ConversationToolResult) error {
	if in.Definition.Key == "skill_load" {
		auth, err := h.AuthorizeConversationTool(ctx, in)
		if err != nil {
			return err
		}
		if !auth.Granted || out.Status != "completed" {
			return conversationFailure("forbidden", "skill_access_denied")
		}
		return nil
	}
	if host, ok := h.ConversationToolHost.(agentsdk.ConversationToolResultAuthorizer); ok {
		return host.AuthorizeConversationToolResult(ctx, in, out)
	}
	return nil
}

func (h *skillToolHost) AuthorizeConversationInteraction(ctx context.Context, a agentsdk.ConversationAuthority, in agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	if host, ok := h.ConversationToolHost.(agentsdk.ConversationInteractionAuthorizer); ok {
		return host.AuthorizeConversationInteraction(ctx, a, in)
	}
	return agentsdk.ConversationToolAuthorization{}, conversationFailure("unavailable", "interaction_unavailable")
}

func (s *ConversationService) ConversationSkills(ctx context.Context, a agentsdk.ConversationAuthority) (agentsdk.ConversationSkillPage, error) {
	out := agentsdk.ConversationSkillPage{Items: []agentsdk.ConversationSkillSummary{}, Complete: true}
	if err := s.authorizeCollaboration(ctx, "discover", nil, a); err != nil {
		return out, err
	}
	skills, err := s.effectiveConversationSkills(ctx, a, a.UserID)
	if err != nil {
		return out, err
	}
	for _, skill := range skills {
		out.Items = append(out.Items, agentsdk.ConversationSkillSummaryFor(skill))
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Key < out.Items[j].Key })
	return out, nil
}

func (s *ConversationService) ConversationSkill(ctx context.Context, key, version string, a agentsdk.ConversationAuthority) (agentsdk.ConversationSkillVersion, error) {
	var out agentsdk.ConversationSkillVersion
	if err := s.authorizeCollaboration(ctx, "discover", nil, a); err != nil {
		return out, err
	}
	out, err := s.conversationSkillVersion(ctx, key, version, a)
	if err != nil {
		return out, err
	}
	out.Definition.Resources = append([]agentsdk.SkillResource{}, out.Definition.Resources...)
	for index := range out.Definition.Resources {
		out.Definition.Resources[index].Content = ""
	}
	return out, nil
}

func (s *ConversationService) conversationSkillVersion(ctx context.Context, key, version string, a agentsdk.ConversationAuthority) (agentsdk.ConversationSkillVersion, error) {
	var out agentsdk.ConversationSkillVersion
	for _, skill := range s.options.Skills {
		if skill.Key == key && skill.Version == version {
			now := s.skillDefinitionPublishedAt()
			return agentsdk.ConversationSkillVersion{Definition: skill, Digest: conversationDigest(skill), State: "published", Revision: 1, PublishedAt: &now, CreatedAt: now}, nil
		}
	}
	if repo, ok := s.repo.(persistence.ConversationImprovementRepository); ok {
		return repo.ConversationSkillVersion(ctx, key, version, a)
	}
	return out, conversationFailure("not_found", "skill_not_found")
}

func (s *ConversationService) ConversationSkillResource(ctx context.Context, key, version, resourceKey string, a agentsdk.ConversationAuthority) (agentsdk.SkillResource, error) {
	var out agentsdk.SkillResource
	if err := s.authorizeCollaboration(ctx, "discover", nil, a); err != nil {
		return out, err
	}
	if !conversationKey(key) || !conversationText(version, 128, true) || !conversationKey(resourceKey) {
		return out, conversationFailure("bad_request", "skill_resource_invalid")
	}
	skill, err := s.conversationSkillVersion(ctx, key, version, a)
	if err != nil {
		return out, err
	}
	for _, resource := range skill.Definition.Resources {
		if resource.Key == resourceKey {
			return resource, nil
		}
	}
	return out, conversationFailure("not_found", "skill_resource_not_found")
}

func (s *ConversationService) skillDefinitionPublishedAt() (out time.Time) {
	return time.Unix(0, 0).UTC()
}

func skillOwnerAuthority(a agentsdk.ConversationAuthority, owner string) agentsdk.ConversationAuthority {
	out := a
	out.UserID = owner
	return out
}

func (s *ConversationService) effectiveConversationSkills(ctx context.Context, a agentsdk.ConversationAuthority, owner string) ([]agentsdk.SkillSchema, error) {
	byKey := map[string]agentsdk.SkillSchema{}
	for _, skill := range s.options.Skills {
		byKey[skill.Key] = skill
	}
	if repo, ok := s.repo.(persistence.ConversationImprovementRepository); ok {
		versions, err := repo.PublishedConversationSkills(ctx, skillOwnerAuthority(a, owner))
		if err != nil {
			return nil, err
		}
		for _, version := range versions {
			if version.State != "published" || version.Definition.Key == "" || version.Definition.Version == "" || version.Digest != conversationDigest(version.Definition) {
				return nil, conversationFailure("unavailable", "skill_configuration_invalid")
			}
			byKey[version.Definition.Key] = version.Definition
		}
	}
	out := make([]agentsdk.SkillSchema, 0, len(byKey))
	for _, skill := range byKey {
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *ConversationService) capabilityConfiguration(ctx context.Context, kind, target string, a agentsdk.ConversationAuthority) (agentsdk.ConversationCapabilityConfiguration, bool, error) {
	repo, ok := s.repo.(persistence.ConversationImprovementRepository)
	if !ok {
		return agentsdk.ConversationCapabilityConfiguration{}, false, nil
	}
	return repo.ConversationCapabilityConfiguration(ctx, kind, target, a)
}

func (s *ConversationService) applyConversationPromptConfiguration(ctx context.Context, a agentsdk.ConversationAuthority, snapshot *agentsdk.ConversationAgentSnapshot) error {
	config, found, err := s.capabilityConfiguration(ctx, "agent_prompt", snapshot.ID, skillOwnerAuthority(a, snapshot.OwnerUserID))
	if err != nil || !found {
		return err
	}
	var proposal struct {
		Instructions string `json:"instructions"`
	}
	var fields map[string]any
	if json.Unmarshal(config.Payload, &proposal) != nil || json.Unmarshal(config.Payload, &fields) != nil || len(fields) != 1 || !conversationText(proposal.Instructions, 32768, true) {
		return conversationFailure("unavailable", "prompt_configuration_invalid")
	}
	snapshot.Profile.Instructions = proposal.Instructions
	snapshot.Profile.Version += "+prompt:" + config.Version
	return nil
}

func conversationAgentPromptVersion(snapshot *agentsdk.ConversationAgentSnapshot) string {
	if snapshot == nil {
		return ""
	}
	const marker = "+prompt:"
	if index := strings.LastIndex(snapshot.Profile.Version, marker); index >= 0 {
		if version := snapshot.Profile.Version[index+len(marker):]; version != "" {
			return version
		}
	}
	return fmt.Sprintf("agent-revision-%d", snapshot.Revision)
}

func (s *ConversationService) improvementRepository() (persistence.ConversationImprovementRepository, error) {
	repo, ok := s.repo.(persistence.ConversationImprovementRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "improvements_unavailable")
	}
	return repo, nil
}

func (s *ConversationService) CreateConversationCapabilityFeedback(ctx context.Context, in agentsdk.ConversationCapabilityFeedbackCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationCapabilityFeedback, error) {
	var out agentsdk.ConversationCapabilityFeedback
	if err := s.authorizeCollaboration(ctx, "configure", nil, a); err != nil {
		return out, err
	}
	if !conversationKey(in.ClientID) || !conversationKey(in.TaskID) || !conversationText(in.Reason, 4096, true) || !conversationText(in.Uncertainty, 1024, false) || len(in.SkillKeys) > 32 || in.Outcome != "adopted" && in.Outcome != "revised" && in.Outcome != "failed" || (in.ArtifactID == "") != (in.ArtifactVersion == 0) || in.ArtifactVersion < 0 {
		return out, conversationFailure("bad_request", "feedback_invalid")
	}
	task, err := s.conversationTaskRecord(ctx, in.TaskID, a)
	if err != nil {
		return out, err
	}
	if _, err = s.ConversationTask(ctx, in.TaskID, a); err != nil {
		return out, err
	}
	if task.Agent == nil || task.ExecutionRunID == "" {
		return out, conversationFailure("conflict", "feedback_task_configuration_unavailable")
	}
	if task.Status != "awaiting_review" && task.Status != "completed" && task.Status != "failed" && task.Status != "cancelled" {
		return out, conversationFailure("conflict", "feedback_task_active")
	}
	if in.ArtifactID != "" {
		artifact, artifactErr := s.Artifact(ctx, in.ArtifactID, in.ArtifactVersion, a)
		if artifactErr != nil {
			return out, artifactErr
		}
		if artifact.Artifact.SourceRunID != task.ExecutionRunID {
			return out, conversationFailure("conflict", "feedback_artifact_mismatch")
		}
	}
	available := map[string]string{}
	for _, skill := range task.Agent.Skills {
		available[skill.Key] = skill.Version
	}
	selected, seen := map[string]string{}, map[string]bool{}
	for key, version := range available {
		selected[key] = version
	}
	for _, key := range in.SkillKeys {
		_, ok := available[key]
		if !ok || seen[key] {
			return out, conversationFailure("bad_request", "feedback_skill_mismatch")
		}
		seen[key] = true
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	out = agentsdk.ConversationCapabilityFeedback{ID: "feedback_" + conversationDigest([]string{a.RuntimeID, a.WorkspaceID, a.UserID, in.ClientID})[:32], TaskID: task.ID, RunID: task.ExecutionRunID, ArtifactID: in.ArtifactID, ArtifactVersion: in.ArtifactVersion, Outcome: in.Outcome, Reason: strings.TrimSpace(in.Reason), Uncertainty: strings.TrimSpace(in.Uncertainty), AgentID: task.Agent.ID, AgentRevision: task.Agent.Revision, AgentPromptVersion: conversationAgentPromptVersion(task.Agent), TargetSkillKeys: append([]string(nil), in.SkillKeys...), SkillVersions: selected, CreatedAt: now}
	repo, err := s.improvementRepository()
	if err != nil {
		return agentsdk.ConversationCapabilityFeedback{}, err
	}
	return repo.CreateConversationCapabilityFeedback(ctx, in.ClientID, out, in, a)
}

func uniqueConversationKeys(values []string, limit int) bool {
	if len(values) == 0 || len(values) > limit {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !conversationKey(value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

type conversationDelegationStrategy struct {
	MinimumReviewedDeliveries int  `json:"minimum_reviewed_deliveries"`
	PreferCost                bool `json:"prefer_cost"`
	PreferCoordination        bool `json:"prefer_coordination"`
	PreferDuration            bool `json:"prefer_duration"`
}

func (s *ConversationService) currentDelegationStrategy(ctx context.Context, a agentsdk.ConversationAuthority) (conversationDelegationStrategy, error) {
	out := conversationDelegationStrategy{MinimumReviewedDeliveries: 3, PreferCost: true, PreferCoordination: true, PreferDuration: true}
	config, found, err := s.capabilityConfiguration(ctx, "delegation_strategy", "default", a)
	if err != nil || !found {
		return out, err
	}
	var fields map[string]any
	if json.Unmarshal(config.Payload, &fields) != nil || json.Unmarshal(config.Payload, &out) != nil || len(fields) != 4 || out.MinimumReviewedDeliveries < 1 || out.MinimumReviewedDeliveries > 100 {
		return out, conversationFailure("unavailable", "delegation_strategy_invalid")
	}
	return out, nil
}

func (s *ConversationService) currentImprovementConfiguration(ctx context.Context, kind, target string, a agentsdk.ConversationAuthority) (string, json.RawMessage, error) {
	if config, found, err := s.capabilityConfiguration(ctx, kind, target, a); err != nil || found {
		return config.Version, append(json.RawMessage(nil), config.Payload...), err
	}
	switch kind {
	case "skill":
		skills, err := s.effectiveConversationSkills(ctx, a, a.UserID)
		if err != nil {
			return "", nil, err
		}
		for _, skill := range skills {
			if skill.Key == target {
				raw, marshalErr := json.Marshal(skill)
				return skill.Version, raw, marshalErr
			}
		}
	case "agent_prompt":
		agent, err := s.conversationAgent(ctx, target, a)
		if err != nil {
			return "", nil, err
		}
		if agent.OwnerUserID != "" && agent.OwnerUserID != a.UserID {
			return "", nil, conversationFailure("forbidden", "agent_access_denied")
		}
		raw, marshalErr := json.Marshal(struct {
			Instructions string `json:"instructions"`
		}{Instructions: agent.Instructions})
		return fmt.Sprintf("agent-revision-%d", agent.Revision), raw, marshalErr
	case "delegation_strategy":
		if target == "default" {
			raw, err := json.Marshal(conversationDelegationStrategy{MinimumReviewedDeliveries: 3, PreferCost: true, PreferCoordination: true, PreferDuration: true})
			return "builtin-1", raw, err
		}
	}
	return "", nil, conversationFailure("not_found", "improvement_target_not_found")
}

func (s *ConversationService) currentImprovementBaseline(ctx context.Context, kind, target string, a agentsdk.ConversationAuthority) (string, error) {
	version, _, err := s.currentImprovementConfiguration(ctx, kind, target, a)
	return version, err
}

func (s *ConversationService) validateImprovementProposal(ctx context.Context, in agentsdk.ConversationImprovementCandidateCreate, a agentsdk.ConversationAuthority) error {
	if len(in.Proposal) == 0 || len(in.Proposal) > 262144 || !json.Valid(in.Proposal) {
		return conversationFailure("bad_request", "improvement_proposal_invalid")
	}
	switch in.Kind {
	case "skill":
		var skill agentsdk.SkillSchema
		if json.Unmarshal(in.Proposal, &skill) != nil || skill.Key != in.TargetKey || skill.Version != in.Version {
			return conversationFailure("bad_request", "improvement_proposal_invalid")
		}
		if err := definition.ValidateSkill(skill); err != nil {
			return conversationFailure("bad_request", "improvement_proposal_invalid")
		}
		repo, err := s.collaborationRepository()
		if err != nil {
			return err
		}
		agents, err := repo.ConversationAgents(ctx, a)
		if err != nil {
			return err
		}
		for _, agent := range agents {
			if agent.OwnerUserID != "" && agent.OwnerUserID != a.UserID {
				continue
			}
			uses := false
			for _, key := range agent.SkillKeys {
				uses = uses || key == skill.Key
			}
			if uses {
				spec := agentsdk.AgentSchema{Key: agent.ID, Version: fmt.Sprint(agent.Revision), Name: agent.Name, Instructions: agent.Instructions, Tools: append([]string(nil), agent.Tools...), SkillKeys: []string{skill.Key}}
				if _, err := definition.CompileProfile(spec, []agentsdk.SkillSchema{skill}, agent.Tools); err != nil {
					return conversationFailure("bad_request", "skill_tool_scope_expanded")
				}
			}
		}
		if s.options.Agent != nil {
			uses := false
			for _, key := range s.options.Agent.SkillKeys {
				uses = uses || key == skill.Key
			}
			if uses {
				if _, err := definition.CompileProfile(*s.options.Agent, []agentsdk.SkillSchema{skill}, s.options.Agent.Tools); err != nil {
					return conversationFailure("bad_request", "skill_tool_scope_expanded")
				}
			}
		}
	case "agent_prompt":
		var fields map[string]any
		var proposal struct {
			Instructions string `json:"instructions"`
		}
		if json.Unmarshal(in.Proposal, &fields) != nil || json.Unmarshal(in.Proposal, &proposal) != nil || len(fields) != 1 || !conversationText(proposal.Instructions, 32768, true) {
			return conversationFailure("bad_request", "improvement_proposal_invalid")
		}
	case "delegation_strategy":
		var fields map[string]any
		var strategy conversationDelegationStrategy
		if in.TargetKey != "default" || json.Unmarshal(in.Proposal, &fields) != nil || json.Unmarshal(in.Proposal, &strategy) != nil || len(fields) != 4 || strategy.MinimumReviewedDeliveries < 1 || strategy.MinimumReviewedDeliveries > 100 {
			return conversationFailure("bad_request", "improvement_proposal_invalid")
		}
	default:
		return conversationFailure("bad_request", "improvement_kind_invalid")
	}
	return nil
}

func (s *ConversationService) CreateConversationImprovementCandidate(ctx context.Context, in agentsdk.ConversationImprovementCandidateCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	if err := s.authorizeCollaboration(ctx, "configure", nil, a); err != nil {
		return out, err
	}
	if !conversationKey(in.ClientID) || !conversationKey(in.TargetKey) || !conversationText(in.Version, 128, true) || !conversationText(in.Reason, 4096, true) || !uniqueConversationKeys(in.FeedbackIDs, 64) {
		return out, conversationFailure("bad_request", "improvement_candidate_invalid")
	}
	if err := s.validateImprovementProposal(ctx, in, a); err != nil {
		return out, err
	}
	repo, err := s.improvementRepository()
	if err != nil {
		return out, err
	}
	feedbacks, err := repo.ConversationCapabilityFeedbacks(ctx, in.FeedbackIDs, a)
	if err != nil {
		return out, err
	}
	baseline, baselineProposal, err := s.currentImprovementConfiguration(ctx, in.Kind, in.TargetKey, a)
	if err != nil {
		return out, err
	}
	if baseline == in.Version {
		return out, conversationFailure("conflict", "improvement_version_conflict")
	}
	if !conversationImprovementFeedbackMatches(in.Kind, in.TargetKey, baseline, feedbacks) {
		return out, conversationFailure("conflict", "improvement_feedback_mismatch")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	out = agentsdk.ConversationImprovementCandidate{ID: "improvement_" + conversationDigest([]string{a.RuntimeID, a.WorkspaceID, a.UserID, in.ClientID})[:32], Kind: in.Kind, TargetKey: in.TargetKey, Version: in.Version, BaselineVersion: baseline, BaselineProposal: append(json.RawMessage(nil), baselineProposal...), FeedbackIDs: append([]string(nil), in.FeedbackIDs...), Proposal: append(json.RawMessage(nil), in.Proposal...), Reason: strings.TrimSpace(in.Reason), Status: "candidate", Revision: 1, CreatedAt: now, UpdatedAt: now}
	return repo.CreateConversationImprovementCandidate(ctx, in.ClientID, out, in, a)
}

func conversationImprovementFeedbackMatches(kind, target, baseline string, feedbacks []agentsdk.ConversationCapabilityFeedback) bool {
	for _, feedback := range feedbacks {
		switch kind {
		case "skill":
			targeted := false
			for _, key := range feedback.TargetSkillKeys {
				targeted = targeted || key == target
			}
			if targeted && feedback.SkillVersions[target] == baseline {
				return true
			}
		case "agent_prompt":
			promptVersion := feedback.AgentPromptVersion
			if promptVersion == "" {
				promptVersion = fmt.Sprintf("agent-revision-%d", feedback.AgentRevision)
			}
			if feedback.AgentID == target && baseline == promptVersion {
				return true
			}
		case "delegation_strategy":
			// Delegation strategy feedback is derived from reviewed task outcomes;
			// the configured target is the single workspace default strategy.
			if target == "default" && feedback.TaskID != "" && feedback.RunID != "" {
				return true
			}
		}
	}
	return false
}

func (s *ConversationService) ConversationImprovementCandidates(ctx context.Context, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidatePage, error) {
	out := agentsdk.ConversationImprovementCandidatePage{Items: []agentsdk.ConversationImprovementCandidate{}, Complete: true}
	if err := s.authorizeCollaboration(ctx, "configure", nil, a); err != nil {
		return out, err
	}
	repo, err := s.improvementRepository()
	if err != nil {
		return out, err
	}
	out.Items, err = repo.ConversationImprovementCandidates(ctx, a)
	return out, err
}

func (s *ConversationService) EvaluateConversationImprovementCandidate(ctx context.Context, id string, in agentsdk.ConversationImprovementEvaluationWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	if err := s.authorizeCollaboration(ctx, "configure", nil, a); err != nil {
		return out, err
	}
	if !conversationKey(id) || !conversationKey(in.ClientID) || in.ExpectedRevision < 1 || !conversationText(in.SuiteVersion, 128, true) || !strings.HasPrefix(in.SuiteVersion, "v01-") || !uniqueConversationKeys(in.ScenarioIDs, 128) || in.BaselineCompleted < 0 || in.CandidateCompleted < 0 || in.BaselineOmissions < 0 || in.CandidateOmissions < 0 || len(in.Regressions) > 128 || in.Passed && (in.CandidateCompleted < in.BaselineCompleted || in.CandidateOmissions > in.BaselineOmissions || len(in.Regressions) > 0) || !conversationText(in.Notes, 8192, false) {
		return out, conversationFailure("bad_request", "improvement_evaluation_invalid")
	}
	repo, err := s.improvementRepository()
	if err != nil {
		return out, err
	}
	return repo.EvaluateConversationImprovementCandidate(ctx, id, in, a)
}

func (s *ConversationService) PublishConversationImprovementCandidate(ctx context.Context, id string, in agentsdk.ConversationImprovementPublish, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	if err := s.authorizeCollaboration(ctx, "configure", nil, a); err != nil {
		return out, err
	}
	if !conversationKey(id) || !conversationKey(in.ClientID) || in.ExpectedRevision < 1 {
		return out, conversationFailure("bad_request", "improvement_publish_invalid")
	}
	repo, err := s.improvementRepository()
	if err != nil {
		return out, err
	}
	candidate, err := repo.ConversationImprovementCandidate(ctx, id, a)
	if err != nil {
		return out, err
	}
	baseline, baselineProposal, err := s.currentImprovementConfiguration(ctx, candidate.Kind, candidate.TargetKey, a)
	if err != nil || baseline != candidate.BaselineVersion || conversationDigest(baselineProposal) != conversationDigest(candidate.BaselineProposal) {
		if err != nil {
			return out, err
		}
		return out, conversationFailure("conflict", "improvement_baseline_changed")
	}
	if err := s.validateImprovementProposal(ctx, agentsdk.ConversationImprovementCandidateCreate{Kind: candidate.Kind, TargetKey: candidate.TargetKey, Version: candidate.Version, Proposal: candidate.Proposal}, a); err != nil {
		return out, err
	}
	return repo.PublishConversationImprovementCandidate(ctx, id, in, a)
}

func (s *ConversationService) RollbackConversationImprovement(ctx context.Context, id string, in agentsdk.ConversationImprovementRollback, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	if err := s.authorizeCollaboration(ctx, "configure", nil, a); err != nil {
		return out, err
	}
	if !conversationKey(id) || !conversationKey(in.ClientID) || in.ExpectedRevision < 1 || !conversationText(in.TargetVersion, 128, true) || !conversationText(in.Reason, 4096, true) {
		return out, conversationFailure("bad_request", "improvement_rollback_invalid")
	}
	repo, err := s.improvementRepository()
	if err != nil {
		return out, err
	}
	return repo.RollbackConversationImprovement(ctx, id, in, a)
}

var _ agentsdk.ConversationToolHost = (*skillToolHost)(nil)
var _ agentsdk.ConversationToolResultAuthorizer = (*skillToolHost)(nil)

// Keep the optional interaction port intact through the wrapper.
var _ agentsdk.ConversationInteractionAuthorizer = (*skillToolHost)(nil)

var _ agentsdk.ConversationSkillService = (*ConversationService)(nil)
