package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

// Real Agent/Identity/SQLite/worker/confirmation/delivery; Runtime's own record,
// receipt permission and execution-ledger policies have a separate real RPC test.
type peerBusinessWriteReadSource struct{ *peerBusinessReadSource }

func (s *peerBusinessWriteReadSource) AuthorizeBusinessAction(context.Context, sdk.ConversationBusinessAction, sdk.ConversationAuthority) (sdk.ConversationToolAuthorization, error) {
	return sdk.ConversationToolAuthorization{Granted: !s.executionDenied.Load()}, nil
}
func (s *peerBusinessWriteReadSource) AuthorizeWorkflowStart(context.Context, sdk.ConversationWorkflowStart, sdk.ConversationAuthority) (sdk.ConversationToolAuthorization, error) {
	return sdk.ConversationToolAuthorization{Granted: !s.executionDenied.Load()}, nil
}
func (s *peerBusinessWriteReadSource) attestWrite(operation, arguments string, result any, a sdk.ConversationAuthority) {
	raw, _ := json.Marshal([]string{s.BusinessSourceIdentity(), a.RuntimeID, a.WorkspaceID, a.UserID})
	hash := sha256.Sum256(raw)
	data, _ := json.Marshal(result)
	s.attest("business", sdk.ConversationBusinessEvidence{Version: 1, Operation: operation, Source: s.BusinessSourceIdentity(), ScopeSHA256: hex.EncodeToString(hash[:]), Input: json.RawMessage(arguments), Data: data}, a)
}
func (s *peerBusinessWriteReadSource) InvokeBusinessAction(_ context.Context, in sdk.ConversationBusinessActionRequest) (sdk.ConversationBusinessActionResult, error) {
	if in.Confirmation == nil || in.Confirmation.UserID != in.Authority.UserID || in.IdempotencyKey == "" {
		return sdk.ConversationBusinessActionResult{}, &sdk.Error{Class: "forbidden", Code: "confirmation_missing"}
	}
	if err := s.invoke(); err != nil {
		return sdk.ConversationBusinessActionResult{}, err
	}
	out := sdk.ConversationBusinessActionResult{Status: "completed", InvocationID: in.IdempotencyKey, ObjectKey: in.Action.ObjectKey, ActionKey: in.Action.ActionKey, ReferenceCount: 1, CreatedRecords: []sdk.ConversationBusinessRecordReference{{ObjectKey: in.Action.ObjectKey, RecordID: "created-once"}}}
	s.attestWrite("invoke_action", in.Arguments, out, in.Authority)
	return out, nil
}
func (s *peerBusinessWriteReadSource) StartBusinessWorkflow(_ context.Context, in sdk.ConversationWorkflowStartRequest) (sdk.ConversationWorkflowReceipt, error) {
	if in.Confirmation == nil || in.Confirmation.UserID != in.Authority.UserID || in.IdempotencyKey == "" {
		return sdk.ConversationWorkflowReceipt{}, &sdk.Error{Class: "forbidden", Code: "confirmation_missing"}
	}
	if err := s.invoke(); err != nil {
		return sdk.ConversationWorkflowReceipt{}, err
	}
	out := sdk.ConversationWorkflowReceipt{Status: "accepted", InvocationID: in.IdempotencyKey, WorkflowKey: in.Start.WorkflowKey, ExecutionID: "execution-once", ProcessID: "process-once"}
	s.attestWrite("workflow_start", in.Arguments, out, in.Authority)
	return out, nil
}
func (s *peerBusinessWriteReadSource) ReconcileBusinessAction(context.Context, sdk.ConversationBusinessActionRequest) (sdk.ConversationBusinessActionResult, error) {
	return sdk.ConversationBusinessActionResult{}, &sdk.Error{Class: "unavailable", Code: "unexpected_reconciliation"}
}
func (s *peerBusinessWriteReadSource) ReconcileBusinessWorkflow(context.Context, sdk.ConversationWorkflowStartRequest) (sdk.ConversationWorkflowReceipt, error) {
	return sdk.ConversationWorkflowReceipt{}, &sdk.Error{Class: "unavailable", Code: "unexpected_reconciliation"}
}
func (s *peerBusinessWriteReadSource) RevalidateBusinessAction(ctx context.Context, e sdk.ConversationBusinessEvidence, a sdk.ConversationAuthority) error {
	return s.RevalidateBusiness(ctx, e, a)
}

func TestPeerBusinessWriteDeliveryReadsOriginalReceiptsWithoutExecution(t *testing.T) {
	testPeerBusinessDeliveryReading(t, true)
}

var _ sdk.ConversationBusinessActionSource = (*peerBusinessWriteReadSource)(nil)
var _ sdk.ConversationBusinessWorkflowSource = (*peerBusinessWriteReadSource)(nil)
