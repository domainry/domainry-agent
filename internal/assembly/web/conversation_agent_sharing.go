package web

import (
	"context"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func (h *Host) ValidateConversationAgentSubjects(ctx context.Context, a sdk.ConversationAuthority, users []string) error {
	if len(users) > 64 {
		return &sdk.Error{Class: "bad_request", Code: "agent.conversation.agent_sharing_invalid"}
	}
	if _, known, err := h.resolveConversationPrincipal(ctx, a); err != nil {
		return err
	} else if !known {
		return &sdk.Error{Class: "forbidden", Code: "agent.conversation.agent_sharing_subject_unavailable"}
	}
	for _, user := range users {
		// Resolve membership only; do not use this principal for any operation.
		subject := sdk.ConversationAuthority{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: user}
		if _, known, err := h.resolveConversationPrincipal(ctx, subject); err != nil {
			return err
		} else if !known {
			return &sdk.Error{Class: "forbidden", Code: "agent.conversation.agent_sharing_subject_unavailable"}
		}
	}
	return ctx.Err()
}

var _ sdk.ConversationAgentSubjectValidator = (*Host)(nil)
