package agent

import (
	"context"
	"encoding/json"

	"github.com/domainry/domainry-orm/query"
)

// Retention exports preserve physical identities and catalog links. This cold
// path is intentionally separate from the bounded authorization read model.
func (s *ConversationStore) executionRetentionPayloads(ctx context.Context, db conversationDB, owner, conversationID string) (map[string]any, error) {
	out := map[string]any{}
	for _, spec := range []struct {
		table   string
		columns []string
	}{
		{conversationStepInputTable, []string{"run_id", "step_no", "tools_hash", "payload_json", "source_context_json"}},
		{conversationToolCatalogTable, []string{"catalog_hash", "payload_json"}},
		{conversationStepSourceTable, []string{"run_id", "step_no", "source_no", "source_conversation_id", "source_run_id", "before_step"}},
	} {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), spec.table).Columns(spec.columns...).
			Where(query.And(query.Equal("owner_key", owner), query.Equal("conversation_id", conversationID))).Build()
		if err != nil {
			return nil, err
		}
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		items := []map[string]any{}
		for rows.Next() {
			values := make([]any, len(spec.columns))
			targets := make([]any, len(values))
			for index := range values {
				targets[index] = &values[index]
			}
			if err = rows.Scan(targets...); err != nil {
				rows.Close()
				return nil, err
			}
			item := map[string]any{}
			for index, column := range spec.columns {
				value := values[index]
				if raw, ok := value.([]byte); ok {
					value = string(raw)
				}
				if column == "payload_json" || column == "source_context_json" {
					value = json.RawMessage(value.(string))
				}
				item[column] = value
			}
			items = append(items, item)
		}
		readErr := rows.Err()
		rows.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(items) > 0 {
			out[spec.table] = items
		}
	}
	return out, nil
}
