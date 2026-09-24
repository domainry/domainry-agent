package agent

import (
	"context"
	"database/sql"
	"errors"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

// ImportCompletedConversationRun atomically appends one external user/assistant
// turn and its completed Run projection. It shares the canonical conversation
// tables; no shadow provenance ledger or caller-chosen owner identity exists.
func (store *ConversationStore) ImportCompletedConversationRun(ctx context.Context, id string, publication agentsdk.ConversationProvenancePublication, authority agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	var output agentsdk.ConversationRun
	if err := publication.Validate(); err != nil {
		return output, conversationError("bad_request", "provenance_invalid")
	}
	err := store.transaction(ctx, func(transaction *sql.Tx) error {
		conversation, err := store.get(ctx, transaction, id, authority)
		if err != nil {
			return err
		}
		requestHash := conversationHash(publication)
		queryText, arguments, err := query.NewSelectBuilder(store.store.Renderer(), agentRunTable).
			Columns(conversationRunColumns...).
			Where(query.And(conversationRunScope(authority, id), query.Equal("idempotency_key", publication.ClientID))).
			Build()
		if err != nil {
			return err
		}
		previous, lookupErr := scanConversationRun(transaction.QueryRowContext(ctx, queryText, arguments...))
		if lookupErr == nil {
			if previous.Run.RequestHash != requestHash {
				return conversationError("conflict", "idempotency_conflict")
			}
			output = previous.Run
			return nil
		}
		var coded *agentsdk.Error
		if !errors.As(lookupErr, &coded) || coded.Code != "agent.conversation.run_not_found" {
			return lookupErr
		}
		if conversation.Archived {
			return conversationError("conflict", "archived")
		}
		if conversation.ActiveRunID != "" {
			return conversationError("conflict", "busy")
		}

		now := time.Now().UTC().Truncate(time.Millisecond)
		startedAt, completedAt := now, now
		run := agentsdk.ConversationRun{
			ID: conversationID("crun_"), ConversationID: id,
			ClientMessageID: publication.ClientID, RequestHash: requestHash,
			Status: "completed", UserSeq: conversation.LastSeq + 1, Attempt: 1,
			StartedAt: &startedAt, CompletedAt: &completedAt,
			CreatedAt: now, UpdatedAt: now,
		}
		userMessage := agentsdk.ConversationMessage{
			ID: conversationID("msg_"), ConversationID: id, RunID: run.ID,
			Seq: run.UserSeq, Role: "user", Content: publication.UserMessage, CreatedAt: now,
		}
		assistantMessage := agentsdk.ConversationMessage{
			ID: conversationID("msg_"), ConversationID: id, RunID: run.ID,
			Seq: run.UserSeq + 1, Role: "assistant", Content: publication.AssistantMessage, CreatedAt: now,
		}
		run.AssistantMessageID = assistantMessage.ID
		conversation.LastSeq = assistantMessage.Seq
		if err = store.save(ctx, transaction, conversation, conversation.Revision, authority); err != nil {
			return err
		}
		if err = store.insertMessage(ctx, transaction, userMessage, authority); err != nil {
			return err
		}
		if err = store.insertMessage(ctx, transaction, assistantMessage, authority); err != nil {
			return err
		}
		row := conversationRunRow{Authority: authority, Run: run}
		if err = store.event(ctx, transaction, &row, "run.completed", map[string]any{
			"attempt": run.Attempt, "assistant_message_id": run.AssistantMessageID, "imported": true,
		}); err != nil {
			return err
		}
		queryText, arguments, err = query.NewInsertBuilder(store.store.Renderer(), agentRunTable).
			Columns("run_kind", "scope_key", "workspace_id", "run_id", "idempotency_key", "owner_key", "conversation_id", "runtime_id", "authority_json", "request_hash", "status", "lease_owner", "fencing_token", "lease_expires_at", "event_seq", "created_at", "updated_at", "payload_json").
			Values(agentRunKindConversation, conversationRunScopeKey(authority, id), conversationRunWorkspaceKey(authority), row.Run.ID, publication.ClientID, conversationOwner(authority), id, authority.RuntimeID, conversationJSON(authority), requestHash, row.Run.Status, "", 0, 0, row.EventSeq, now.UnixMilli(), now.UnixMilli(), conversationJSON(row.Run)).
			Build()
		if err = conversationExec(ctx, transaction, queryText, arguments, err); err != nil {
			return err
		}
		output = row.Run
		output.RequestHash = requestHash
		return nil
	})
	return output, err
}
