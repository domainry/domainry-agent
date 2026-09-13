package application

import (
	"context"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

// Explicit test host policy. Tests of private source boundaries opt into
// collaboration first, so a missing host cannot make a source test pass early.
type allowCollaborationTestPolicy struct{}

func (allowCollaborationTestPolicy) AuthorizeConversationCollaboration(_ context.Context, in sdk.ConversationCollaborationAuthorizationRequest) (sdk.ConversationCollaborationAuthorization, error) {
	return sdk.ConversationCollaborationAuthorization{Allowed: append([]string{}, in.Operations...), Revision: "test-current-policy"}, nil
}

func TestCollaborationRequiresAnExplicitCurrentHostPolicy(t *testing.T) {
	s := &ConversationService{runtimeID: "runtime"}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	if err := s.authorizeCollaboration(t.Context(), "manage", nil, a); err == nil {
		t.Fatal("missing policy granted management")
	}
	s.options.CollaborationAuthorizer = allowCollaborationTestPolicy{}
	if err := s.authorizeCollaboration(t.Context(), "manage", nil, a); err != nil {
		t.Fatal(err)
	}
	if err := s.authorizeCollaboration(t.Context(), "invented-operation", nil, a); err == nil {
		t.Fatal("unknown operation granted")
	}
}

type fixedCollaborationTestPolicy []string

func (p fixedCollaborationTestPolicy) AuthorizeConversationCollaboration(_ context.Context, in sdk.ConversationCollaborationAuthorizationRequest) (sdk.ConversationCollaborationAuthorization, error) {
	return sdk.ConversationCollaborationAuthorization{Allowed: append([]string{}, p...), Revision: "current"}, nil
}
func TestCollaborationDeliverySourcePurposeCannotAuthorizeRawReplay(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	s := &ConversationService{runtimeID: "runtime", options: ConversationOptions{CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read"}}}
	audit := s.sourceAudit(a)
	d := sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "released"}, Task: &sdk.ConversationTaskDetail{}, Messages: []sdk.ConversationAgentMessage{{Content: "private working context"}}}
	if err := audit.collaborationResult(t.Context(), d); !collaborationDenied(err) {
		t.Fatalf("raw context exposed: %v", err)
	}
	if err := audit.collaborationResult(deliverySourceContext(t.Context(), "released"), d); err != nil {
		t.Fatalf("authorized delivery could not verify its own sources: %v", err)
	}
	if err := audit.collaborationResult(deliverySourceContext(t.Context(), "other"), d); !collaborationDenied(err) {
		t.Fatalf("delivery purpose crossed delegation: %v", err)
	}
	if err := audit.collaborationResult(t.Context(), d); !collaborationDenied(err) {
		t.Fatalf("cached delivery audit authorized later raw replay: %v", err)
	}
	d.Task = nil
	d.Messages = nil
	d.Handoff = &sdk.ConversationDelegationHandoff{}
	if err := audit.collaborationResult(t.Context(), d); !collaborationDenied(err) {
		t.Fatalf("handoff exposed raw effects without execution permission: %v", err)
	}
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view"}
	if err := s.sourceAudit(a).collaborationResult(deliverySourceContext(t.Context(), "released"), d); !collaborationDenied(err) {
		t.Fatalf("delivery purpose survived revocation: %v", err)
	}
}

func TestCollaborationInboxRequiresCommunicationButServerStateNoticesDoNot(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	s := &ConversationService{runtimeID: "runtime", options: ConversationOptions{CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "receive"}}}
	message := sdk.ConversationAgentMessage{DelegationID: "d", FromAgentID: "peer", Kind: "message", Content: "private correspondence"}
	if err := s.sourceAudit(a).peerMessage(t.Context(), message); !collaborationDenied(err) {
		t.Fatalf("receive grant exposed correspondence: %v", err)
	}
	message.Kind = "task_completed"
	if err := s.sourceAudit(a).peerMessage(t.Context(), message); err != nil {
		t.Fatalf("server status notice was blocked: %v", err)
	}
	message.FromUserID = "user"
	if err := s.sourceAudit(a).peerMessage(t.Context(), message); !collaborationDenied(err) {
		t.Fatalf("user message became server notice: %v", err)
	}
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"view", "communicate"}
	if err := s.sourceAudit(a).peerMessage(t.Context(), message); err != nil {
		t.Fatal(err)
	}
}
