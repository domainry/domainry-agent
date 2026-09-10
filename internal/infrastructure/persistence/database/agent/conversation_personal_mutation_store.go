package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

// The call ledger is also the receipt for local effects. Saving it and the
// memory in the same transaction closes the effect/result crash window without
// a second idempotency table. Never take mutation arguments from the caller.
func (s *ConversationStore) ApplyPersonalTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return s.applyLocalTool(ctx, in, agentsdk.PersonalConversationTools(), func(tx *sql.Tx, claim persistence.ConversationClaim, call persistence.ConversationToolExecution) (agentsdk.ConversationToolResult, error) {
		if strings.HasPrefix(call.Call.Name, "todo_") {
			return s.applyTodoTool(ctx, tx, in, call)
		}
		return s.applyPersonalMemory(ctx, tx, claim.Authority, call)
	})
}

func (s *ConversationStore) applyLocalTool(ctx context.Context, in agentsdk.ConversationToolRequest, definitions []agentsdk.ConversationToolDefinition, apply func(*sql.Tx, persistence.ConversationClaim, persistence.ConversationToolExecution) (agentsdk.ConversationToolResult, error)) (agentsdk.ConversationToolResult, error) {
	var result agentsdk.ConversationToolResult
	if err := conversationAuthority(in.Authority); err != nil {
		return result, err
	}
	claim := persistence.ConversationClaim{Authority: in.Authority, Run: agentsdk.ConversationRun{ID: in.RunID, ConversationID: in.ConversationID}, Owner: in.LeaseOwner, Fence: in.Fence}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		var call persistence.ConversationToolExecution
		found, err := s.readExecutionTool(ctx, tx, claim, in.Step, in.Call.ID, &call)
		if err != nil {
			return err
		}
		if !found || in.IdempotencyKey == "" || in.IdempotencyKey != call.IdempotencyKey || conversationHash(in.Call) != conversationHash(call.Call) || conversationHash(in.Definition) != conversationHash(call.Definition) {
			return conversationError("conflict", "tool_input_conflict")
		}
		known := false
		for _, definition := range definitions {
			if definition.Effect == "write" && conversationHash(definition) == conversationHash(call.Definition) {
				known = true
			}
		}
		if !known {
			return conversationError("forbidden", "tool_access_denied")
		}
		if !row.Run.WriteScope.Allows(call.Call.Name) {
			record, found, err := s.readInteraction(ctx, tx, claim, in.Step, in.Call.ID, "confirmation")
			if err != nil {
				return err
			}
			i := record.Interaction
			if !found || i.Status != "approved" || i.RespondedBy != in.Authority.UserID || i.RespondedAt == nil || i.DefinitionHash != conversationHash(call.Definition) || i.ArgumentsHash != conversationHash(call.Call.Arguments) {
				return conversationError("forbidden", "tool_confirmation_required")
			}
		}
		if call.State == "completed" && call.Result != nil {
			result = *call.Result
			return nil
		}
		result, err = apply(tx, claim, call)
		if err != nil {
			var coded *agentsdk.Error
			if !errors.As(err, &coded) || coded.Class != "bad_request" && coded.Class != "conflict" && coded.Class != "not_found" {
				return err
			}
			code := strings.TrimPrefix(coded.Code, "agent.conversation.")
			result = agentsdk.ConversationToolResult{Status: "failed", ErrorCode: code, Content: conversationJSON(map[string]string{"error": code})}
		}
		return s.finishExecutionTool(ctx, tx, claim, in.Step, in.Call.ID, result)
	})
	return result, err
}

func (s *ConversationStore) applyPersonalMemory(ctx context.Context, tx *sql.Tx, a agentsdk.ConversationAuthority, call persistence.ConversationToolExecution) (agentsdk.ConversationToolResult, error) {
	var result agentsdk.ConversationToolResult
	var in agentsdk.ConversationMemoryWrite
	if err := json.Unmarshal([]byte(call.Call.Arguments), &in); err != nil {
		return result, conversationError("bad_request", "memory_invalid")
	}
	if call.Call.Name == "memory_forget" {
		if !personalMemoryKey(in.ID) || in.ExpectedRevision < 1 {
			return result, conversationError("bad_request", "memory_invalid")
		}
		q, args, err := query.NewDeleteBuilder(s.store.Renderer(), "_agent_user_memories").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("memory_id", in.ID), query.Equal("revision", in.ExpectedRevision))).Build()
		if err = conversationCAS(ctx, tx, q, args, err); err != nil {
			return result, err
		}
		return agentsdk.ConversationToolResult{Status: "completed", ResourceID: in.ID, Content: conversationJSON(map[string]any{"id": in.ID, "deleted": true})}, nil
	}
	if call.Call.Name != "memory_save" || !executionText(in.Title, 128, true) || !executionText(in.Content, 512, true) || in.ExpectedRevision < 0 || in.ID != "" && (!personalMemoryKey(in.ID) || in.ExpectedRevision == 0) || in.ID == "" && in.ExpectedRevision != 0 {
		return result, conversationError("bad_request", "memory_invalid")
	}
	if in.ID == "" {
		in.ID = "mem_" + conversationHash(call.IdempotencyKey)[:32]
	}
	var memory agentsdk.ConversationMemory
	if err := s.writeMemory(ctx, tx, in, a, &memory, true); err != nil {
		return result, err
	}
	return agentsdk.ConversationToolResult{Status: "completed", ResourceID: memory.ID, Content: conversationJSON(map[string]any{"memory": memory})}, nil
}

func personalMemoryKey(value string) bool {
	if len(value) == 0 || len(value) > 96 {
		return false
	}
	for _, c := range value {
		if c != '_' && c != '-' && c != '.' && c != ':' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

var _ persistence.ConversationPersonalMutationRepository = (*ConversationStore)(nil)
