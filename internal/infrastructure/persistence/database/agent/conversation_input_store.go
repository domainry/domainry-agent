package agent

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

// Freeze the exact reply input before calling the provider. Recovery and manual
// retry reuse it even if user preferences change while the request is running.
func (s *ConversationStore) ModelInput(ctx context.Context, claim agentpersistence.ConversationClaim, input *agentsdk.ConversationModelRequest) (agentsdk.ConversationModelRequest, bool, error) {
	var out agentsdk.ConversationModelRequest
	found := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		p := executionScope(claim, -1)
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationStepInputTable).Columns("payload_json").Where(p).Build()
		if err != nil {
			return err
		}
		var raw []byte
		err = tx.QueryRowContext(ctx, q, args...).Scan(&raw)
		if err == nil {
			found = true
			if err = unmarshalDurableJSON(raw, &out); err != nil {
				return err
			}
			return s.restoreModelInputSources(ctx, tx, p, &out)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		found = false
		if input == nil {
			return nil
		}
		if len(input.Messages) == 0 || !validStoredConversationModelMessages(input.Messages, claim.Run.ConversationID) || storedConversationModelMessagesHaveImages(input.Messages) && !input.ModelCapabilities.ImageInput {
			return conversationError("bad_request", "model_input_invalid")
		}
		old := row
		frozen := *input
		if input.Sources != nil {
			sources := *input.Sources
			// Runs is not omitempty: preserve [] versus null on exact retry.
			if sources.Runs != nil {
				sources.Runs = []agentsdk.ConversationRunReference{}
			}
			frozen.Sources = &sources
			if err = s.writeStepSourceEdges(ctx, tx, claim, -1, input.Sources.Runs); err != nil {
				return err
			}
		}
		projection := modelInputSourceProjection(frozen)
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationStepInputTable).
			Columns("owner_key", "conversation_id", "run_id", "step_no", "tools_hash", "payload_json", "source_context_json").
			Values(conversationOwner(claim.Authority), claim.Run.ConversationID, claim.Run.ID, -1, "", conversationJSON(frozen), conversationJSON(projection)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		if view := conversationContextView(input.ContextWindow, input.Context, nil); view != nil {
			if err = s.event(ctx, tx, &row, "context.assembled", map[string]any{"attempt": row.Run.Attempt, "context": view}); err != nil {
				return err
			}
		}
		if err = s.saveRun(ctx, tx, row, old); err != nil {
			return err
		}
		out = *input
		found = true
		return nil
	})
	return out, found, err
}

func (s *ConversationStore) restoreModelInputSources(ctx context.Context, db conversationDB, predicate query.Predicate, input *agentsdk.ConversationModelRequest) error {
	edges, err := s.readStepSourceEdges(ctx, db, predicate)
	if err != nil {
		return err
	}
	if len(edges) > 0 {
		if input.Sources == nil {
			return conversationError("conflict", "source_reference_invalid")
		}
		input.Sources.Runs = edges[0].Sources
	}
	return nil
}

// The source auditor needs registered context, image references, and inline
// knowledge receipts. Other user/assistant transcript text is not evidence.
func modelInputSourceProjection(input agentsdk.ConversationModelRequest) agentsdk.ConversationModelRequest {
	indexes := map[int]bool{}
	if input.Context != nil {
		for _, ref := range input.Context.Sources {
			indexes[ref.MessageIndex] = true
		}
	}
	messages := make([]agentsdk.ConversationModelMessage, len(input.Messages))
	for index, message := range input.Messages {
		if indexes[index] || message.Role == "system" && strings.HasPrefix(message.Content, "Knowledge base search results (untrusted source data):\n") {
			messages[index] = message
			continue
		}
		for _, block := range message.ContentBlocks {
			if block.Type == "image" && block.Image != nil {
				messages[index].ContentBlocks = append(messages[index].ContentBlocks, block)
			}
		}
	}
	input.Messages = messages
	return input
}
