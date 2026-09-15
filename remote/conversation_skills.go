package remote

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) ConversationSkills(ctx context.Context, a agentsdk.ConversationAuthority) (agentsdk.ConversationSkillPage, error) {
	var out agentsdk.ConversationSkillPage
	err := c.call(ctx, "skills_list", agentsdk.ConversationRPCRequest{Authority: a}, &out)
	return out, err
}

func (c *conversationClient) ConversationSkill(ctx context.Context, key, version string, a agentsdk.ConversationAuthority) (agentsdk.ConversationSkillVersion, error) {
	var out agentsdk.ConversationSkillVersion
	err := c.call(ctx, "skills_get", agentsdk.ConversationRPCRequest{Authority: a, SkillKey: key, SkillVersion: version}, &out)
	return out, err
}

func (c *conversationClient) ConversationSkillResource(ctx context.Context, key, version, resource string, a agentsdk.ConversationAuthority) (agentsdk.SkillResource, error) {
	var out agentsdk.SkillResource
	err := c.call(ctx, "skills_resource_get", agentsdk.ConversationRPCRequest{Authority: a, SkillKey: key, SkillVersion: version, SkillResourceKey: resource}, &out)
	return out, err
}

func (c *conversationClient) CreateConversationCapabilityFeedback(ctx context.Context, in agentsdk.ConversationCapabilityFeedbackCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationCapabilityFeedback, error) {
	var out agentsdk.ConversationCapabilityFeedback
	err := c.call(ctx, "feedback_create", agentsdk.ConversationRPCRequest{Authority: a, CapabilityFeedback: in}, &out)
	return out, err
}

func (c *conversationClient) ConversationImprovementCandidates(ctx context.Context, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidatePage, error) {
	var out agentsdk.ConversationImprovementCandidatePage
	err := c.call(ctx, "improvements_list", agentsdk.ConversationRPCRequest{Authority: a}, &out)
	return out, err
}

func (c *conversationClient) CreateConversationImprovementCandidate(ctx context.Context, in agentsdk.ConversationImprovementCandidateCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	err := c.call(ctx, "improvements_create", agentsdk.ConversationRPCRequest{Authority: a, ImprovementCandidate: in}, &out)
	return out, err
}

func (c *conversationClient) EvaluateConversationImprovementCandidate(ctx context.Context, id string, in agentsdk.ConversationImprovementEvaluationWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	err := c.call(ctx, "improvements_evaluate", agentsdk.ConversationRPCRequest{Authority: a, CandidateID: id, ImprovementEvaluation: in}, &out)
	return out, err
}

func (c *conversationClient) PublishConversationImprovementCandidate(ctx context.Context, id string, in agentsdk.ConversationImprovementPublish, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	err := c.call(ctx, "improvements_publish", agentsdk.ConversationRPCRequest{Authority: a, CandidateID: id, ImprovementPublish: in}, &out)
	return out, err
}

func (c *conversationClient) RollbackConversationImprovement(ctx context.Context, id string, in agentsdk.ConversationImprovementRollback, a agentsdk.ConversationAuthority) (agentsdk.ConversationImprovementCandidate, error) {
	var out agentsdk.ConversationImprovementCandidate
	err := c.call(ctx, "improvements_rollback", agentsdk.ConversationRPCRequest{Authority: a, CandidateID: id, ImprovementRollback: in}, &out)
	return out, err
}

var _ agentsdk.ConversationSkillService = (*conversationClient)(nil)
