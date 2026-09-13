package application

import (
	"context"
	"slices"
	"strings"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func participantOperations(d sdk.ConversationDelegation, user string) []string {
	for _, p := range d.Participants {
		if p.UserID == user && p.Revision > 0 {
			return p.Operations
		}
	}
	return nil
}

func (s *ConversationService) authorizePendingParticipantMessage(ctx context.Context, m sdk.ConversationAgentMessage, receiver sdk.ConversationAuthority) error {
	if m.ParticipantUserID == "" {
		if m.SenderUserID == "" {
			return nil // Server notices and legacy messages have no sender binding.
		}
		repo, err := s.collaborationRepository()
		if err != nil {
			return err
		}
		d, err := repo.ConversationDelegation(ctx, m.DelegationID, receiver)
		if err != nil {
			return err
		}
		if m.SenderUserID != receiver.UserID {
			if err := s.validateAgentSharingSubjects(ctx, receiver, []string{m.SenderUserID}); err != nil {
				return err
			}
		}
		sender := sdk.ConversationAuthority{Known: true, RuntimeID: receiver.RuntimeID, WorkspaceID: receiver.WorkspaceID, UserID: m.SenderUserID, RoleKey: m.SenderRoleKey}
		return s.authorizeCollaboration(delegationExecutorContext(ctx), "communicate", &d, sender)
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return err
	}
	d, err := repo.ConversationDelegation(ctx, m.DelegationID, receiver)
	if err != nil {
		return err
	}
	valid := false
	for _, p := range d.Participants {
		valid = valid || p.UserID == m.ParticipantUserID && p.Revision == m.ParticipantRevision && slices.Contains(p.Operations, "communicate")
	}
	if !valid || m.FromUserID != m.ParticipantUserID {
		return conversationFailure("forbidden", "collaboration_access_denied")
	}
	if err := s.validateAgentSharingSubjects(ctx, receiver, []string{m.ParticipantUserID}); err != nil {
		return err
	}
	// This principal is used only for a current sender-policy check. Execution
	// remains the receiving task's persisted subject, never the participant.
	sender := sdk.ConversationAuthority{Known: true, RuntimeID: receiver.RuntimeID, WorkspaceID: receiver.WorkspaceID, UserID: m.ParticipantUserID, RoleKey: m.ParticipantRoleKey}
	return s.authorizeCollaboration(ctx, "communicate", &d, sender)
}

func (s *ConversationService) updateDelegationParticipants(ctx context.Context, d sdk.ConversationDelegation, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegationDetail, error) {
	var out sdk.ConversationDelegationDetail
	if d.OwnerUserID != "" && d.OwnerUserID != a.UserID {
		return out, conversationFailure("forbidden", "delegation_owner_required")
	}
	for _, op := range []string{"view", "manage", "share"} {
		if err := s.authorizeCollaboration(ctx, op, &d, a); err != nil {
			return out, err
		}
	}
	if in.Participants == nil || len(*in.Participants) > 64 || in.Brief != nil || in.Delivery != nil || in.Review != nil || in.StructuredInput != nil || in.Dependencies != nil || in.Transfer != nil || in.Disagreement != nil || in.Inspection != nil {
		return out, conversationFailure("bad_request", "delegation_participants_invalid")
	}
	users := []string{}
	seen := map[string]bool{}
	for _, p := range *in.Participants {
		if p.UserID == "" || p.UserID == a.UserID || len(p.UserID) > 255 || strings.TrimSpace(p.UserID) != p.UserID || strings.ContainsAny(p.UserID, "\x00\r\n\t") || seen[p.UserID] || !slices.Contains(p.Operations, "view") || len(p.Operations) > len(sdk.ConversationParticipantOperations()) {
			return out, conversationFailure("bad_request", "delegation_participants_invalid")
		}
		seen[p.UserID] = true
		users = append(users, p.UserID)
		operations := map[string]bool{}
		for _, op := range p.Operations {
			if !slices.Contains(sdk.ConversationParticipantOperations(), op) || operations[op] {
				return out, conversationFailure("bad_request", "delegation_participants_invalid")
			}
			operations[op] = true
		}
	}
	if err := s.validateAgentSharingSubjects(ctx, a, users); err != nil {
		return out, err
	}
	peer, _ := ctx.Value(conversationPeerRequestKey{}).(conversationPeerRequest)
	if peer.ConversationID != "" && peer.ConversationID != d.SourceConversationID {
		return out, conversationFailure("forbidden", "delegation_actor_invalid")
	}
	in.ToolRequest = peer.ToolRequest
	repo, ok := s.repo.(persistence.ConversationDelegationParticipantRepository)
	if !ok {
		return out, conversationFailure("unavailable", "delegation_participants_unavailable")
	}
	updated, err := repo.SetConversationDelegationParticipants(ctx, d.ID, in, a)
	if err != nil {
		return out, err
	}
	// Saving or revoking membership does not require reading the owner's
	// execution sources. A separate detail read applies its own current policy.
	return sdk.ConversationDelegationDetail{ParticipantsOnly: true, MessagesComplete: false, Messages: []sdk.ConversationAgentMessage{}, ConversationDelegation: sdk.ConversationDelegation{
		ID: updated.ID, OwnerUserID: updated.OwnerUserID, Revision: updated.Revision, UpdatedAt: updated.UpdatedAt,
		ParticipantsRevision: updated.ParticipantsRevision, Participants: updated.Participants,
	}}, nil
}

func (s *ConversationService) projectParticipantDelegation(ctx context.Context, d sdk.ConversationDelegation, access sdk.ConversationCollaborationAccess, a sdk.ConversationAuthority) (sdk.ConversationDelegationDetail, error) {
	var out sdk.ConversationDelegationDetail
	if err := s.validateAgentSharingSubjects(ctx, a, []string{d.OwnerUserID}); err != nil {
		return out, err
	}
	// A participant grant releases the task agreement, not the owner's raw
	// conversations. Derived input still requires its current source policy.
	for _, ref := range []*sdk.ConversationRunReference{d.BriefSource, d.InputSource} {
		if ref != nil {
			if err := s.checkRunSources(ctx, *ref, a); err != nil {
				return out, conversationFailure("forbidden", "delegation_contract_source_unavailable")
			}
		}
	}
	out = sdk.ConversationDelegationDetail{Access: &access, MessagesComplete: true, Messages: []sdk.ConversationAgentMessage{}, ConversationDelegation: sdk.ConversationDelegation{
		OwnerUserID: d.OwnerUserID, ExecutionSubject: d.ExecutionSubject, ParticipantsRevision: d.ParticipantsRevision,
		ID: d.ID, FromAgentID: d.FromAgentID, ToAgentID: d.ToAgentID, Purpose: d.Purpose, Brief: d.Brief, Input: d.Input, StructuredInput: d.StructuredInput, OutputSchema: d.OutputSchema,
		Budget: d.Budget, Status: d.Status, Revision: d.Revision, AgreementRevision: d.AgreementRevision, AdoptedAgreementRevision: d.AdoptedAgreementRevision, AdoptedAt: d.AdoptedAt, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}}
	for _, p := range d.Participants {
		if p.UserID == a.UserID {
			out.Participants = []sdk.ConversationDelegationParticipant{p}
		}
	}
	if access.Communicate {
		repo, err := s.collaborationRepository()
		if err != nil {
			return out, err
		}
		out.Messages, err = repo.ConversationAgentMessages(ctx, d.ID, a)
		if err != nil {
			return out, err
		}
		if len(out.Messages) > 128 {
			out.MessagesComplete = false
			out.Messages = out.Messages[len(out.Messages)-128:]
		}
		for i := range out.Messages {
			m := &out.Messages[i]
			denied := s.checkSharedDocuments(ctx, m.Documents, a) != nil
			if m.Source != nil && s.checkRunSources(ctx, *m.Source, a) != nil {
				denied = true
			}
			if denied {
				m.Content = "消息来源当前无法验证，内容暂不可查看。"
				m.DocumentsOmitted = len(m.Documents) > 0
				m.Documents = nil
			}
			m.Source, m.Change = nil, nil
			m.ConversationID, m.AfterRunID, m.ConsumedByRunID = "", "", ""
		}
	}
	return out, nil
}
