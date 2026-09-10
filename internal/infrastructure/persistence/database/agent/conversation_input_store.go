package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

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
		p := query.And(conversationScope(claim.Authority, claim.Run.ConversationID), query.Equal("run_id", claim.Run.ID))
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_inputs").Columns("payload_json").Where(p).Build()
		if err != nil {
			return err
		}
		var raw []byte
		err = tx.QueryRowContext(ctx, q, args...).Scan(&raw)
		if err == nil {
			found = true
			return json.Unmarshal(raw, &out)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		found = false
		if input == nil {
			return nil
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), "_agent_conversation_inputs").Columns("owner_key", "conversation_id", "run_id", "payload_json").Values(conversationOwner(claim.Authority), claim.Run.ConversationID, claim.Run.ID, conversationJSON(input)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		if err = s.saveRun(ctx, tx, row, row); err != nil {
			return err
		}
		out = *input
		found = true
		return nil
	})
	return out, found, err
}
