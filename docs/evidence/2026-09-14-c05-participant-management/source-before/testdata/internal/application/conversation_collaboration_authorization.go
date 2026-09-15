package application

import (
	"context"
	"errors"
	"slices"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) ConversationCollaborationAccess(ctx context.Context, a sdk.ConversationAuthority) (sdk.ConversationCollaborationAuthorization, error) {
	return s.collaborationAuthorization(ctx, sdk.ConversationCollaborationOperations(), nil, a)
}

func (s *ConversationService) collaborationAuthorization(ctx context.Context, operations []string, d *sdk.ConversationDelegation, a sdk.ConversationAuthority) (sdk.ConversationCollaborationAuthorization, error) {
	var denied sdk.ConversationCollaborationAuthorization
	if err := s.authorize(a); err != nil {
		return denied, err
	}
	for _, operation := range operations {
		if sdk.ConversationCollaborationPermission(operation) == nil {
			return denied, conversationFailure("forbidden", "collaboration_access_denied")
		}
	}
	policy := s.options.CollaborationAuthorizer
	if policy == nil {
		return denied, conversationFailure("unavailable", "collaboration_authorization_unavailable")
	}
	if d != nil && d.OwnerUserID != "" && d.OwnerUserID != a.UserID && !delegationExecutor(*d, a) && len(participantOperations(*d, a.UserID)) > 0 {
		if err := s.authorizeParticipantPublisher(ctx, *d, a); err != nil {
			return denied, err
		}
	}
	in := sdk.ConversationCollaborationAuthorizationRequest{Authority: a, Operations: append([]string{}, operations...), OwnerUserID: a.UserID}
	if d != nil {
		if d.OwnerUserID != "" {
			in.OwnerUserID = d.OwnerUserID
		}
		in.DelegationID = d.ID
		in.FromAgentID = d.FromAgentID
		in.ToAgentID = d.ToAgentID
	}
	if peer, ok := ctx.Value(conversationPeerRequestKey{}).(conversationPeerRequest); ok && peer.ConversationID != "" {
		c, err := s.repo.Get(ctx, peer.ConversationID, a)
		if err != nil {
			return denied, err
		}
		in.AgentID = c.AgentID
		if in.AgentID == "" {
			in.AgentID = "default"
		}
	}
	ctx, cancel := s.externalCallContext(ctx, 5*time.Second)
	defer cancel()
	decision, err := policy.AuthorizeConversationCollaboration(ctx, in)
	if err != nil || ctx.Err() != nil {
		return denied, conversationFailure("unavailable", "collaboration_authorization_unavailable")
	}
	if d != nil && d.OwnerUserID != "" && d.OwnerUserID != a.UserID {
		granted := participantOperations(*d, a.UserID)
		if delegationExecutor(*d, a) {
			granted = []string{"view", "receive", "communicate", "execution_read", "delivery_read"}
		}
		allowed := []string{}
		for _, op := range decision.Allowed {
			if slices.Contains(granted, op) {
				allowed = append(allowed, op)
			}
		}
		decision.Allowed = allowed
	}
	return decision, nil
}

func (s *ConversationService) authorizeCollaborationConversation(ctx context.Context, id, operation string, a sdk.ConversationAuthority) error {
	if _, ok := s.repo.(persistence.ConversationCollaborationRepository); !ok {
		return nil
	}
	if err := s.authorize(a); err != nil {
		return err
	}
	c, err := s.repo.Get(ctx, id, a)
	if err != nil {
		return err
	}
	if c.DelegationID == "" {
		return nil
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return err
	}
	d, err := repo.ConversationDelegation(ctx, c.DelegationID, a)
	if err != nil {
		return err
	}
	return s.authorizeCollaboration(ctx, operation, &d, a)
}

func (audit *conversationSourceAudit) collaborationResult(ctx context.Context, d sdk.ConversationDelegationDetail) error {
	access, ok := audit.collaboration[d.ID]
	if !ok {
		var err error
		access, err = audit.s.collaborationAccess(ctx, d.ConversationDelegation, audit.a)
		if err != nil {
			return err
		}
		audit.collaboration[d.ID] = access
	}
	// A delivery reader receives the deliberately submitted projection. Its
	// provenance may include the same delegation's working context, which is
	// checked without exposing those raw execution or communication records.
	deliveryScope, _ := ctx.Value(conversationDeliverySourceKey{}).(string)
	projectedDelivery := deliveryScope != "" && deliveryScope == d.ID && access.DeliveryRead && !rawExecutionSource(ctx)
	if !access.View || !projectedDelivery && ((d.Task != nil || d.Handoff != nil) && !access.ExecutionRead || len(d.Messages) > 0 && !access.Communicate) {
		return conversationFailure("forbidden", "collaboration_access_denied")
	}
	delivery := d.Delivery != nil || d.Verification != nil || d.Handoff != nil || len(d.Disagreements) > 0
	for _, assignment := range d.Assignments {
		delivery = delivery || assignment.PreviousDelivery != nil
	}
	if delivery && !access.DeliveryRead {
		return conversationFailure("forbidden", "collaboration_access_denied")
	}
	for _, message := range d.Messages {
		if err := audit.s.checkSharedDocuments(ctx, message.Documents, audit.a); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationService) authorizeCollaboration(ctx context.Context, operation string, d *sdk.ConversationDelegation, a sdk.ConversationAuthority) error {
	decision, err := s.collaborationAuthorization(ctx, []string{operation}, d, a)
	if err != nil {
		return err
	}
	if !slices.Contains(decision.Allowed, operation) {
		return conversationFailure("forbidden", "collaboration_access_denied")
	}
	return nil
}

func collaborationUpdateOperation(in sdk.ConversationDelegationUpdate) string {
	switch in.Action {
	case "set_participants":
		return "share"
	case "deliver", "reject":
		return "receive"
	case "republish_delivery", "republish_contract":
		return "share"
	case "disagreement":
		if in.Disagreement != nil && in.Disagreement.Operation != "decide" {
			return "communicate"
		}
		return "manage"
	case "pause", "cancel", "resume", "update_brief", "update_input", "set_dependencies", "request_changes", "accept_delivery", "review_delivery", "inspect_outcome", "transfer":
		return "manage"
	default:
		return ""
	}
}

func (s *ConversationService) collaborationAccess(ctx context.Context, d sdk.ConversationDelegation, a sdk.ConversationAuthority) (sdk.ConversationCollaborationAccess, error) {
	var out sdk.ConversationCollaborationAccess
	fields := map[string]*bool{"view": &out.View, "receive": &out.Receive, "manage": &out.Manage, "communicate": &out.Communicate, "execution_read": &out.ExecutionRead, "delivery_read": &out.DeliveryRead, "share": &out.Share}
	operations := []string{"view", "receive", "manage", "communicate", "execution_read", "delivery_read", "share"}
	decision, err := s.collaborationAuthorization(ctx, operations, &d, a)
	if err != nil {
		return out, err
	}
	for _, operation := range decision.Allowed {
		if target, ok := fields[operation]; ok {
			*target = true
		}
	}
	return out, nil
}

// The actual delegation supplies policy facts, including historical assignments.
func (s *ConversationService) authorizeCollaborationTask(ctx context.Context, task sdk.ConversationTask, operation string, a sdk.ConversationAuthority) error {
	if task.DelegationID == "" {
		return nil
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return err
	}
	d, err := repo.ConversationDelegation(ctx, task.DelegationID, a)
	if err != nil {
		return err
	}
	return s.authorizeCollaboration(ctx, operation, &d, a)
}
func collaborationDenied(err error) bool {
	var coded *sdk.Error
	return errors.As(err, &coded) && coded.Class == "forbidden"
}

// Only service-owned delivery projections introduce this read purpose. It
// never authorizes a direct execution/result endpoint or relaxes owner source
// authorization and private-attachment boundaries.
type conversationDeliverySourceKey struct{}

type conversationRawExecutionSourceKey struct{}

func rawExecutionSource(ctx context.Context) bool {
	value, _ := ctx.Value(conversationRawExecutionSourceKey{}).(bool)
	return value
}

// History messages and task details expose execution-derived text. Keep the
// delivery's independent resource readers, but require raw collaboration data
// permissions throughout that text's provenance. Never share cached decisions
// made under the narrower submitted-result projection.
func (audit *conversationSourceAudit) rawExecutionSourceAudit(ctx context.Context) (context.Context, *conversationSourceAudit) {
	if rawExecutionSource(ctx) {
		return ctx, audit
	}
	child := audit.childSourceAudit()
	return context.WithValue(ctx, conversationRawExecutionSourceKey{}, true), child
}

func deliverySourceContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, conversationDeliverySourceKey{}, id)
}
func (audit *conversationSourceAudit) authorizeExecutionSource(ctx context.Context, conversationID string) error {
	scope, _ := ctx.Value(conversationDeliverySourceKey{}).(string)
	if scope != "" && !rawExecutionSource(ctx) {
		c, err := audit.s.repo.Get(ctx, conversationID, audit.a)
		if err != nil {
			return err
		}
		if c.DelegationID == scope {
			return audit.s.authorizeCollaborationConversation(ctx, conversationID, "delivery_read", audit.a)
		}
	}
	return audit.s.authorizeCollaborationConversation(ctx, conversationID, "execution_read", audit.a)
}

func (audit *conversationSourceAudit) deliveryAudit(ctx context.Context, id string) (context.Context, *conversationSourceAudit) {
	if scope, _ := ctx.Value(conversationDeliverySourceKey{}).(string); scope == id {
		return ctx, audit
	}
	child := audit.childSourceAudit()
	// The completed-proof key separates read purposes as well as ledger and
	// inherited publication boundaries; a delivery cannot authorize raw replay.
	return deliverySourceContext(ctx, id), child
}

func (s *ConversationService) authorizeCollaborationID(ctx context.Context, id, operation string, a sdk.ConversationAuthority) error {
	if err := s.authorize(a); err != nil {
		return err
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return err
	}
	d, err := repo.ConversationDelegation(ctx, id, a)
	if err != nil {
		return err
	}
	return s.authorizeCollaboration(ctx, operation, &d, a)
}
func peerMessageRequiresCommunication(message sdk.ConversationAgentMessage) bool {
	return message.FromUserID != "" || message.Kind == "message" || message.Kind == "question" || message.Kind == "reply" || message.Kind == "" && message.FromAgentID != ""
}
func (audit *conversationSourceAudit) peerMessage(ctx context.Context, message sdk.ConversationAgentMessage) error {
	if !peerMessageRequiresCommunication(message) {
		return nil
	}
	return audit.collaborationResult(ctx, sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: message.DelegationID, FromAgentID: message.FromAgentID, ToAgentID: message.ToAgentID}, Messages: []sdk.ConversationAgentMessage{message}})
}
