package application

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func executionSubject(a sdk.ConversationAuthority) *sdk.ConversationExecutionSubject {
	return &sdk.ConversationExecutionSubject{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: a.UserID}
}

func (s *ConversationService) validateAgentSharingSubjects(ctx context.Context, a sdk.ConversationAuthority, users []string) error {
	if len(users) == 0 {
		return nil
	}
	validator, ok := s.options.CollaborationAuthorizer.(sdk.ConversationAgentSubjectValidator)
	if !ok {
		return conversationFailure("unavailable", "agent_sharing_subjects_unavailable")
	}
	ctx, cancel := s.externalCallContext(ctx, 20*time.Second)
	defer cancel()
	if err := validator.ValidateConversationAgentSubjects(ctx, a, users); err != nil {
		return err
	}
	return ctx.Err()
}

func (s *ConversationService) prepareAgentSharing(ctx context.Context, id string, in *sdk.ConversationAgentWrite, a sdk.ConversationAuthority) error {
	var prior []string
	if id != "" {
		agent, err := s.conversationAgent(ctx, id, a)
		if err != nil {
			return err
		}
		if agent.Shared || agent.OwnerUserID != "" && agent.OwnerUserID != a.UserID {
			return conversationFailure("forbidden", "agent_configuration_owner_required")
		}
		prior = agent.SharedWithUserIDs
	}
	if in.SharedWithUserIDs == nil {
		return nil
	}
	users := append([]string{}, (*in.SharedWithUserIDs)...)
	if len(users) > 64 {
		return conversationFailure("bad_request", "agent_sharing_invalid")
	}
	seen := map[string]bool{}
	for _, user := range users {
		if user == "" || len(user) > 255 || strings.TrimSpace(user) != user || user == a.UserID || seen[user] || strings.ContainsAny(user, "\x00\r\n\t") {
			return conversationFailure("bad_request", "agent_sharing_invalid")
		}
		seen[user] = true
	}
	sort.Strings(users)
	prior = append([]string{}, prior...)
	sort.Strings(prior)
	in.SharedWithUserIDs = &users
	if slices.Equal(users, prior) {
		return nil
	}
	if err := s.authorizeCollaboration(ctx, "share", nil, a); err != nil {
		return err
	}
	return s.validateAgentSharingSubjects(ctx, a, users)
}
