package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Authorization receipts and timestamps may change during recovery. The
// actual operation and observed outcome are the evidence we must preserve.
func conversationRecordHash(record persistence.ConversationToolExecution) string {
	return conversationDigest([]any{record.Step, record.Call, record.Definition, record.State, record.Result})
}

// Keep a small deterministic index separate from model-written summaries.
// Only owner-scoped message/run identifiers enter context automatically;
// resource facts enter through execution_read with current authorization.
func (s *ConversationService) recentConversationExecutionLinks(ctx context.Context, claim persistence.ConversationClaim) (agentsdk.ConversationModelMessage, error) {
	history, err := s.repo.History(ctx, claim.Run.ConversationID, max(0, claim.Run.UserSeq-33), claim.Run.UserSeq-1, 32, claim.Authority)
	if err != nil {
		return agentsdk.ConversationModelMessage{}, err
	}
	links := []map[string]any{}
	seen := map[string]bool{}
	for i := len(history) - 1; i >= 0 && len(links) < 4; i-- {
		message := history[i]
		if message.RunID == "" || message.RunID == claim.Run.ID || seen[message.RunID] {
			continue
		}
		seen[message.RunID] = true
		links = append(links, map[string]any{"conversation_id": message.ConversationID, "run_id": message.RunID, "message_id": message.ID, "seq": message.Seq})
	}
	return agentsdk.ConversationModelMessage{Role: "system", Content: "Recent execution links (server-issued data, newest first; not a complete history):\n" + conversationJSONText(links) + "\nUse execution_read to inspect actual prior tool outcomes before relying on resource identifiers or completion claims in a summary. For older work use history_search/read to recover run IDs. Recorded outcomes are historical observations; use the relevant business tool to check live status. A missing result does not prove an action did not occur."}, nil
}

func (s *ConversationService) authorizeConversationRecord(ctx context.Context, conversationID, runID string, source persistence.ConversationToolExecution, a agentsdk.ConversationAuthority, current map[string]conversationCompiledTool, seen map[string]bool, recipient string) error {
	if recipient != "" && recipient != conversationID && (privateAttachmentCall(source.Call) || privateRemoteAttachmentCall(source.Call)) {
		return conversationFailure("forbidden", "attachment_conversation_mismatch")
	}
	key := conversationDigest([]any{conversationID, runID, conversationRecordHash(source)})
	if complete, exists := seen[key]; exists {
		if complete {
			return nil
		}
		return conversationFailure("conflict", "result_reference_invalid")
	}
	if len(seen) >= 256 {
		return conversationFailure("bad_request", "result_reference_invalid")
	}
	seen[key] = false
	definition, exists := current[source.Call.Name]
	if !exists {
		return conversationFailure("forbidden", "tool_access_denied")
	}
	if conversationDigest(definition.definition) != conversationDigest(source.Definition) {
		return conversationFailure("conflict", "tool_changed")
	}
	auth, err := s.options.ToolHost.AuthorizeConversationTool(ctx, agentsdk.ConversationToolRequest{Authority: a, ConversationID: conversationID, RunID: runID, Step: source.Step, Call: source.Call, Definition: source.Definition})
	if err != nil {
		return err
	}
	if !auth.Granted {
		return conversationFailure("forbidden", "tool_access_denied")
	}
	if source.Result != nil {
		if err = s.authorizeStoredToolResult(ctx, agentsdk.ConversationToolRequest{Authority: a, ConversationID: conversationID, RunID: runID, Step: source.Step, Call: source.Call, Definition: source.Definition}, *source.Result); err != nil {
			return err
		}
	}
	if err = s.reauthorizeReadDependencies(ctx, source, a, current, seen, recipient); err != nil {
		return err
	}
	seen[key] = true
	return nil
}

func (s *ConversationService) reauthorizeReadDependencies(ctx context.Context, source persistence.ConversationToolExecution, a agentsdk.ConversationAuthority, current map[string]conversationCompiledTool, seen map[string]bool, recipient string) error {
	if source.Result == nil || source.Result.Status != "completed" {
		return nil
	}
	switch source.Call.Name {
	case "history_search", "history_read":
		_, err := s.sourceAudit(a, recipient).record(ctx, agentsdk.ConversationRunReference{}, source)
		return err
	case "tool_result_read":
		var previous agentsdk.ConversationResultRead
		if json.Unmarshal([]byte(source.Call.Arguments), &previous) != nil {
			return conversationFailure("conflict", "result_reference_invalid")
		}
		_, err := s.authorizedConversationResult(ctx, previous.Reference, a, current, seen, recipient)
		return err
	case "execution_read":
		repo, ok := s.repo.(persistence.ConversationExecutionReadRepository)
		if !ok {
			return conversationFailure("unavailable", "execution_read_unavailable")
		}
		var previous agentsdk.ConversationExecutionReadResult
		var args agentsdk.ConversationExecutionRead
		if json.Unmarshal(source.Result.Content, &previous) != nil || json.Unmarshal([]byte(source.Call.Arguments), &args) != nil || previous.ConversationID != args.ConversationID || previous.RunID != args.RunID || len(previous.Items) > 5 {
			return conversationFailure("conflict", "execution_reference_invalid")
		}
		if _, err := s.repo.Run(ctx, previous.ConversationID, previous.RunID, a); err != nil {
			return err
		}
		for _, entry := range previous.Items {
			record, err := repo.ReadExecutionCall(ctx, previous.ConversationID, previous.RunID, entry.Step, entry.CallID, a)
			if err != nil {
				return err
			}
			if conversationRecordHash(record) != entry.RecordHash {
				return conversationFailure("conflict", "execution_reference_changed")
			}
			if err = s.authorizeConversationRecord(ctx, previous.ConversationID, previous.RunID, record, a, current, seen, recipient); err != nil {
				return err
			}
		}
	}
	return nil
}

func executionReadFailure(err error) agentsdk.ConversationToolResult {
	var coded *agentsdk.Error
	if errors.As(err, &coded) {
		return personalToolFailure(strings.TrimPrefix(coded.Code, "agent.conversation."))
	}
	return personalToolFailure("execution_read_failed")
}

func (s *ConversationService) readConversationExecution(ctx context.Context, in agentsdk.ConversationToolRequest) agentsdk.ConversationToolResult {
	var args agentsdk.ConversationExecutionRead
	if json.Unmarshal([]byte(in.Call.Arguments), &args) != nil || args.ConversationID == in.ConversationID && args.RunID == in.RunID {
		return personalToolFailure("execution_reference_invalid")
	}
	repo, ok := s.repo.(persistence.ConversationExecutionReadRepository)
	if !ok {
		return personalToolFailure("execution_read_unavailable")
	}
	page, err := repo.ReadExecutionCalls(ctx, args, in.Authority)
	if err != nil {
		return executionReadFailure(err)
	}
	_, current, err := s.executionCatalog(ctx, in.Authority)
	if err != nil {
		return executionReadFailure(err)
	}
	out := agentsdk.ConversationExecutionReadResult{ConversationID: args.ConversationID, RunID: args.RunID, RunStatus: page.RunStatus, Items: []agentsdk.ConversationExecutionEntry{}, NextCursor: page.NextCursor, Complete: page.Complete}
	for _, source := range page.Items {
		if err = s.authorizeConversationRecord(ctx, args.ConversationID, args.RunID, source, in.Authority, current, map[string]bool{}, in.ConversationID); err != nil {
			var coded *agentsdk.Error
			if errors.As(err, &coded) && (coded.Class == "forbidden" || coded.Class == "conflict" || coded.Class == "not_found") {
				out.Omitted = true
				continue
			}
			return executionReadFailure(err)
		}
		entry := agentsdk.ConversationExecutionEntry{Step: source.Step, CallID: source.Call.ID, Tool: source.Call.Name, RecordHash: conversationRecordHash(source), State: source.State}
		if source.Result != nil {
			entry.Status, entry.ResourceID, entry.ErrorCode = source.Result.Status, source.Result.ResourceID, source.Result.ErrorCode
			entry.Completion = source.Result.Completion
			if source.State == "completed" {
				entry.Reference = &agentsdk.ConversationResultReference{ConversationID: args.ConversationID, RunID: args.RunID, Step: source.Step, CallID: source.Call.ID, SHA256: conversationDigest(source.Result)}
			}
		}
		out.Items = append(out.Items, entry)
	}
	result, err := personalToolResult(out)
	if err != nil {
		return executionReadFailure(err)
	}
	if len(result.Content) > in.Definition.MaxOutputBytes {
		return personalToolFailure("execution_read_exceeded")
	}
	return result
}
