package agent

import (
	"context"
	"encoding/hex"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationStore) ConversationResult(ctx context.Context, ref agentsdk.ConversationResultReference, a agentsdk.ConversationAuthority) (persistence.ConversationToolExecution, error) {
	var out persistence.ConversationToolExecution
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	digest, err := hex.DecodeString(ref.SHA256)
	if !personalMemoryKey(ref.ConversationID) || !personalMemoryKey(ref.RunID) || ref.Step < 0 || ref.Step >= 256 || !executionText(ref.CallID, 256, true) || err != nil || len(digest) != 32 {
		return out, conversationError("bad_request", "result_reference_invalid")
	}
	claim := persistence.ConversationClaim{Authority: a, Run: agentsdk.ConversationRun{ID: ref.RunID, ConversationID: ref.ConversationID}}
	found, err := s.readExecutionTool(ctx, s.store.Database(), claim, ref.Step, ref.CallID, &out)
	if err != nil {
		return out, err
	}
	if !found || out.State != "completed" || out.Result == nil {
		return out, conversationError("not_found", "result_not_found")
	}
	if conversationHash(out.Result) != ref.SHA256 {
		return persistence.ConversationToolExecution{}, conversationError("conflict", "result_reference_changed")
	}
	return out, nil
}

var _ persistence.ConversationResultRepository = (*ConversationStore)(nil)
