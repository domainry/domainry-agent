package agent

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

// The call ledger is also the receipt for local effects. Saving it and the
// memory in the same transaction closes the effect/result crash window without
// a second idempotency table. Never take mutation arguments from the caller.
func (s *ConversationStore) ApplyPersonalTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	if strings.HasPrefix(in.Call.Name, "todo_") && s.todoTransactions == nil && s.todoMutations != nil {
		return s.applyRemoteTodoTool(ctx, in)
	}
	return s.applyLocalTool(ctx, in, agentsdk.PersonalConversationTools(), func(tx *sql.Tx, claim persistence.ConversationClaim, call persistence.ConversationToolExecution) (agentsdk.ConversationToolResult, error) {
		if strings.HasPrefix(call.Call.Name, "todo_") {
			return s.applyTodoTool(ctx, tx, in, call)
		}
		return s.applyPersonalMemory(ctx, tx, claim, call)
	})
}

// applyRemoteTool separates the Agent ledger transaction from an independently
// deployed module transaction. A crash after the remote effect is recovered by
// replaying the same domain idempotency key, then committing the returned
// receipt to the Agent ledger. It never holds an Agent SQL transaction across
// the network.
func (s *ConversationStore) applyRemoteTool(ctx context.Context, in agentsdk.ConversationToolRequest, definitions []agentsdk.ConversationToolDefinition, apply func(persistence.ConversationClaim, persistence.ConversationToolExecution) (agentsdk.ConversationToolResult, error)) (agentsdk.ConversationToolResult, error) {
	var result agentsdk.ConversationToolResult
	if err := conversationAuthority(in.Authority); err != nil {
		return result, err
	}
	var claim persistence.ConversationClaim
	var call persistence.ConversationToolExecution
	completed := false
	inspect := func(tx *sql.Tx) error {
		claim = persistence.ConversationClaim{Authority: in.Authority, Run: agentsdk.ConversationRun{ID: in.RunID, ConversationID: in.ConversationID}, Owner: in.LeaseOwner, Fence: in.Fence}
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		claim.Run = row.Run
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
			interaction := record.Interaction
			if !found || interaction.Status != "approved" || interaction.RespondedBy != in.Authority.UserID || interaction.RespondedAt == nil || interaction.DefinitionHash != conversationHash(call.Definition) || interaction.ArgumentsHash != conversationHash(call.Call.Arguments) {
				return conversationError("forbidden", "tool_confirmation_required")
			}
		}
		completed = call.State == "completed" && call.Result != nil
		if completed {
			result = *call.Result
		}
		return nil
	}
	if err := s.transaction(ctx, inspect); err != nil || completed {
		return result, err
	}
	result, err := apply(claim, call)
	if err != nil {
		var coded *agentsdk.Error
		if !errors.As(err, &coded) || coded.Class != "bad_request" && coded.Class != "conflict" && coded.Class != "not_found" {
			return result, err
		}
		code := strings.TrimPrefix(coded.Code, "agent.conversation.")
		result = agentsdk.ConversationToolResult{Status: "failed", ErrorCode: code, Content: conversationJSON(map[string]string{"error": code})}
	}
	remoteResult := result
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		completed = false
		if err := inspect(tx); err != nil {
			return err
		}
		if completed {
			return nil
		}
		result = remoteResult
		return s.finishExecutionTool(ctx, tx, claim, in.Step, in.Call.ID, result)
	})
	return result, err
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
		claim.Run = row.Run
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

func (s *ConversationStore) applyPersonalMemory(ctx context.Context, tx *sql.Tx, claim persistence.ConversationClaim, call persistence.ConversationToolExecution) (agentsdk.ConversationToolResult, error) {
	var result agentsdk.ConversationToolResult
	var args struct {
		ID               string   `json:"id"`
		Kind             string   `json:"kind"`
		Title            string   `json:"title"`
		Content          string   `json:"content"`
		Enabled          bool     `json:"enabled"`
		Scope            string   `json:"scope"`
		AppliesTo        []string `json:"applies_to"`
		Uncertainty      string   `json:"uncertainty"`
		CorrectionReason string   `json:"correction_reason"`
		ExpectedRevision int64    `json:"expected_revision"`
	}
	if err := unmarshalDurableJSON([]byte(call.Call.Arguments), &args); err != nil {
		return result, conversationError("bad_request", "memory_invalid")
	}
	if call.Call.Name == "memory_forget" {
		if !personalMemoryKey(args.ID) || args.ExpectedRevision < 1 {
			return result, conversationError("bad_request", "memory_invalid")
		}
		if err := s.deleteMemory(ctx, tx, args.ID, args.ExpectedRevision, claim.Authority); err != nil {
			return result, err
		}
		return agentsdk.ConversationToolResult{Status: "completed", ResourceID: args.ID, Content: conversationJSON(map[string]any{"id": args.ID, "deleted": true})}, nil
	}
	if args.Kind == "" {
		args.Kind = agentsdk.ConversationMemoryKindUserPreference
	}
	if args.Scope == "" {
		args.Scope = agentsdk.ConversationMemoryScopeWorkspace
	}
	args.Title = strings.TrimSpace(args.Title)
	args.Content = strings.TrimSpace(args.Content)
	args.Uncertainty = strings.TrimSpace(args.Uncertainty)
	args.CorrectionReason = strings.TrimSpace(args.CorrectionReason)
	args.AppliesTo = normalizeStoredMemoryTopics(args.AppliesTo)
	if call.Call.Name != "memory_save" || !executionText(args.Title, 128, true) || !executionText(args.Content, 512, true) || !executionText(args.Uncertainty, 512, false) || !executionText(args.CorrectionReason, 512, false) || args.ExpectedRevision < 0 || args.ID != "" && (!personalMemoryKey(args.ID) || args.ExpectedRevision == 0) || args.ID == "" && args.ExpectedRevision != 0 || len(args.AppliesTo) > 16 {
		return result, conversationError("bad_request", "memory_invalid")
	}
	if args.Kind != agentsdk.ConversationMemoryKindUserPreference && args.Kind != agentsdk.ConversationMemoryKindProjectFact && args.Kind != agentsdk.ConversationMemoryKindTaskContext || args.Scope != agentsdk.ConversationMemoryScopeWorkspace && args.Scope != agentsdk.ConversationMemoryScopeConversation && args.Scope != agentsdk.ConversationMemoryScopeTask || args.Kind == agentsdk.ConversationMemoryKindTaskContext && args.Scope == agentsdk.ConversationMemoryScopeWorkspace {
		return result, conversationError("bad_request", "memory_scope_invalid")
	}
	for _, topic := range args.AppliesTo {
		if !executionText(topic, 64, true) {
			return result, conversationError("bad_request", "memory_invalid")
		}
	}
	if args.ID == "" {
		args.ID = "mem_" + conversationHash(call.IdempotencyKey)[:32]
	}
	scope := agentsdk.ConversationMemoryScope{Kind: args.Scope}
	switch args.Scope {
	case agentsdk.ConversationMemoryScopeConversation:
		scope.ConversationID = claim.Run.ConversationID
	case agentsdk.ConversationMemoryScopeTask:
		if claim.Run.BackgroundTask == nil || claim.Run.BackgroundTask.TaskID == "" {
			return result, conversationError("bad_request", "memory_scope_invalid")
		}
		scope.ConversationID = claim.Run.ConversationID
		scope.TaskID = claim.Run.BackgroundTask.TaskID
	}
	source, err := s.personalMemoryToolSource(ctx, tx, claim)
	if err != nil {
		return result, err
	}
	if args.CorrectionReason != "" {
		source.Kind = "user_correction"
	}
	in := agentsdk.ConversationMemoryWrite{ID: args.ID, Kind: args.Kind, Title: args.Title, Content: args.Content, Enabled: args.Enabled, Scope: scope, AppliesTo: args.AppliesTo, Source: &source, Uncertainty: args.Uncertainty, CorrectionReason: args.CorrectionReason, ExpectedRevision: args.ExpectedRevision}
	var memory agentsdk.ConversationMemory
	if err := s.writeMemory(ctx, tx, in, claim.Authority, &memory, true); err != nil {
		return result, err
	}
	return agentsdk.ConversationToolResult{Status: "completed", ResourceID: memory.ID, Content: conversationAPIJSON(map[string]any{"memory": memory})}, nil
}

func (s *ConversationStore) personalMemoryToolSource(ctx context.Context, tx *sql.Tx, claim persistence.ConversationClaim) (agentsdk.ConversationMemorySource, error) {
	source := agentsdk.ConversationMemorySource{Kind: "user_request", ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, CapturedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if claim.Run.BackgroundTask != nil {
		source.TaskID = claim.Run.BackgroundTask.TaskID
	}
	if claim.Run.UserSeq < 1 {
		return source, nil
	}
	q, queryArgs, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(conversationItemScope(claim.Authority, claim.Run.ConversationID, conversationItemMessage), query.Equal("seq", claim.Run.UserSeq))).Build()
	if err != nil {
		return source, err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, q, queryArgs...).Scan(&raw); err != nil {
		return source, err
	}
	var message agentsdk.ConversationMessage
	if err = unmarshalDurableJSON(raw, &message); err != nil {
		return source, err
	}
	if message.Role != "user" || message.RunID != claim.Run.ID {
		return source, conversationError("conflict", "memory_source_invalid")
	}
	source.MessageID = message.ID
	return source, nil
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
