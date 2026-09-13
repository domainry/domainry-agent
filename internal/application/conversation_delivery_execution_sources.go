package application

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (audit *conversationSourceAudit) readDeliveredExecution(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) ([]sdk.ConversationRunReference, error) {
	repo, ok := audit.s.repo.(persistence.ConversationExecutionReadRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "source_read_unavailable")
	}
	readCall := func(conversation, run string, step int, id string) (persistence.ConversationToolExecution, error) {
		if conversation == owner.ConversationID && run == owner.RunID && (record.Call.Name == "execution_read" || step >= record.Step) {
			return persistence.ConversationToolExecution{}, invalidPersonalReceipt()
		}
		original, err := repo.ReadExecutionCall(ctx, conversation, run, step, id, audit.a)
		if err != nil {
			return original, err
		}
		if original.Step != step || original.Call.ID != id {
			return original, invalidPersonalReceipt()
		}
		return original, nil
	}
	if record.Call.Name == "tool_result_read" {
		var args sdk.ConversationResultRead
		var saved sdk.ConversationResultSlice
		if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil || decodePersonalReceipt(record.Result.Content, &saved) != nil || saved.Reference != args.Reference || saved.Offset != args.Offset {
			return nil, invalidPersonalReceipt()
		}
		ref := args.Reference
		if err := audit.authorizeDeliveredExecutionData(ctx, ref.ConversationID); err != nil {
			return nil, err
		}
		if _, err := audit.s.repo.Run(ctx, ref.ConversationID, ref.RunID, audit.a); err != nil {
			return nil, err
		}
		original, err := readCall(ref.ConversationID, ref.RunID, ref.Step, ref.CallID)
		if err != nil {
			return nil, err
		}
		if original.State != "completed" || original.Result == nil || conversationDigest(original.Result) != ref.SHA256 {
			return nil, invalidPersonalReceipt()
		}
		raw, err := json.Marshal(original.Result)
		if err != nil {
			return nil, err
		}
		if args.MaxBytes == 0 {
			args.MaxBytes = 4096
		}
		// Validate the original page boundary. A later model context budget
		// must not re-cut or silently replace a previously delivered slice.
		if saved.TotalBytes != len(raw) || saved.Offset < 0 || saved.Offset > len(raw) || saved.NextOffset < saved.Offset || saved.NextOffset > min(len(raw), saved.Offset+args.MaxBytes) || saved.NextOffset == saved.Offset && !saved.Complete || saved.Complete != (saved.NextOffset == len(raw)) {
			return nil, invalidPersonalReceipt()
		}
		if saved.Offset < len(raw) && !utf8.RuneStart(raw[saved.Offset]) || saved.NextOffset < len(raw) && !utf8.RuneStart(raw[saved.NextOffset]) || saved.JSONText != string(raw[saved.Offset:saved.NextOffset]) {
			return nil, invalidPersonalReceipt()
		}
		return audit.deliveredOriginalExecution(ctx, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID}, original)
	}
	var args sdk.ConversationExecutionRead
	var saved sdk.ConversationExecutionReadResult
	if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil || decodePersonalReceipt(record.Result.Content, &saved) != nil || saved.ConversationID != args.ConversationID || saved.RunID != args.RunID || len(saved.Items) > 5 || args.Limit > 0 && len(saved.Items) > args.Limit {
		return nil, invalidPersonalReceipt()
	}
	if args.ConversationID == owner.ConversationID && args.RunID == owner.RunID {
		return nil, invalidPersonalReceipt()
	}
	if err := audit.authorizeDeliveredExecutionData(ctx, args.ConversationID); err != nil {
		return nil, err
	}
	if _, err := audit.s.repo.Run(ctx, args.ConversationID, args.RunID, audit.a); err != nil {
		return nil, err
	}
	var roots []sdk.ConversationRunReference
	seen := map[string]bool{}
	for _, item := range saved.Items {
		key := conversationDigest([]any{item.Step, item.CallID})
		if seen[key] {
			return nil, invalidPersonalReceipt()
		}
		seen[key] = true
		original, err := readCall(args.ConversationID, args.RunID, item.Step, item.CallID)
		if err != nil {
			return nil, err
		}
		if conversationDigest(item) != conversationDigest(conversationExecutionEntry(args.ConversationID, args.RunID, original)) {
			return nil, conversationFailure("conflict", "source_snapshot_changed")
		}
		part, err := audit.deliveredOriginalExecution(ctx, sdk.ConversationRunReference{ConversationID: args.ConversationID, RunID: args.RunID}, original)
		if err != nil {
			return nil, err
		}
		roots = mergeConversationSources(roots, part)
	}
	return roots, nil
}

func (audit *conversationSourceAudit) deliveredOriginalExecution(ctx context.Context, owner sdk.ConversationRunReference, original persistence.ConversationToolExecution) ([]sdk.ConversationRunReference, error) {
	reader := audit
	if _, rawCollaboration := collaborationTool(original.Call.Name); rawCollaboration {
		// Wrapping a raw collaboration receipt in tool_result_read or an
		// execution index cannot acquire the provenance-only exception.
		ctx, reader = audit.deliveryAudit(ctx, "")
	}
	if original.State != "completed" || original.Result == nil || original.Result.Status != "completed" || original.Result.ErrorCode != "" {
		// An unfinished/error result has no independent completed receipt.
		// Keep its existing execution/source authorization instead of using
		// record(), which intentionally ignores unsuccessful tool output.
		_, current, err := reader.s.executionCatalog(ctx, reader.a)
		if err != nil {
			return nil, err
		}
		if err := reader.s.authorizeConversationRecord(ctx, owner.ConversationID, owner.RunID, original, reader.a, current, map[string]bool{}, reader.conversationID); err != nil {
			return nil, err
		}
	}
	ref := sdk.ConversationRunReference{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: original.Step + 2}
	roots, err := reader.run(ctx, ref)
	if err != nil {
		return nil, err
	}
	part, err := reader.record(ctx, owner, original)
	return mergeConversationSources(roots, part), err
}
