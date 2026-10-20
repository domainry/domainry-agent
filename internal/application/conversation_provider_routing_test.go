package application

import (
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestHydratedModelRequestsCarryClaimConversationID(t *testing.T) {
	service := &ConversationService{}
	claim := persistence.ConversationClaim{Run: agentsdk.ConversationRun{ConversationID: "conversation-1"}}

	modelRequest, err := service.hydrateConversationModelRequest(t.Context(), claim, agentsdk.ConversationModelRequest{})
	if err != nil || modelRequest.ConversationID != claim.Run.ConversationID {
		t.Fatalf("model request conversation ID = %q, want %q; err=%v", modelRequest.ConversationID, claim.Run.ConversationID, err)
	}

	stepRequest, err := service.hydrateConversationStepRequest(t.Context(), claim, agentsdk.ConversationStepRequest{})
	if err != nil || stepRequest.ConversationID != claim.Run.ConversationID {
		t.Fatalf("step request conversation ID = %q, want %q; err=%v", stepRequest.ConversationID, claim.Run.ConversationID, err)
	}
}
