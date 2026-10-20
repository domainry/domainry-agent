package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

// The mutable header is deliberately independent of the immutable request.
// Updating a result never serializes or writes the transcript/catalog again.
type executionStepHeader struct {
	Number           int                         `json:"number"`
	Result           *sdk.ConversationStepResult `json:"result,omitempty"`
	InputHash        string                      `json:"input_hash"`
	ToolsHash        string                      `json:"tools_hash"`
	MaxOutputBytes   int                         `json:"max_output_bytes"`
	MaxArgumentBytes int                         `json:"max_argument_bytes"`
	MaxToolCalls     int                         `json:"max_tool_calls"`
	MaxParallelTools int                         `json:"max_parallel_tools"`
	CreatedAt        time.Time                   `json:"created_at"`
	UpdatedAt        time.Time                   `json:"updated_at"`
}

// Registered context messages retain their ORIGINAL indexes and hashes.
// Images in other messages remain available to current image authorization,
// without loading unrelated text, provider state, or tool definitions.
type executionSourceContext struct {
	Context  *sdk.ConversationContextManifest    `json:"context,omitempty"`
	Messages map[int]sdk.ConversationStepMessage `json:"messages,omitempty"`
}

func executionJSONHash(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func frozenExecutionInputHash(input sdk.ConversationStepRequest) (string, error) {
	raw, err := marshalDurableJSON(input)
	return executionJSONHash(raw), err
}

func stepSourceContext(input sdk.ConversationStepRequest) executionSourceContext {
	out := executionSourceContext{Context: input.Context}
	for index, message := range input.Messages {
		projected := sdk.ConversationStepMessage{}
		if message.ContextSourceKey != "" {
			projected = message
		} else {
			for _, block := range message.ContentBlocks {
				if block.Type == "image" && block.Image != nil {
					projected.ContentBlocks = append(projected.ContentBlocks, block)
				}
			}
		}
		if projected.ContextSourceKey != "" || len(projected.ContentBlocks) > 0 {
			if out.Messages == nil {
				out.Messages = map[int]sdk.ConversationStepMessage{}
			}
			out.Messages[index] = projected
		}
	}
	return out
}

func (value executionSourceContext) stepContext(number int) (persistence.ConversationStepContext, error) {
	out := persistence.ConversationStepContext{Step: number, Context: value.Context}
	for index := range value.Messages {
		// Every message occupies at least this many bytes in the <=4 MiB
		// frozen request. Do not impose a smaller, unrelated message limit.
		if index < 0 || index > 4*1024*1024/len(`{"role":""}`) {
			return out, conversationError("conflict", "step_input_conflict")
		}
		if index >= len(out.Messages) {
			out.Messages = append(out.Messages, make([]sdk.ConversationStepMessage, index+1-len(out.Messages))...)
		}
		out.Messages[index] = value.Messages[index]
	}
	return out, nil
}

func (s *ConversationStore) writeSplitExecutionStep(ctx context.Context, tx *sql.Tx, claim persistence.ConversationClaim, number int, step persistence.ConversationExecutionStep, insert bool) error {
	var header executionStepHeader
	if insert {
		var err error
		header.InputHash, err = frozenExecutionInputHash(step.Input)
		if err != nil {
			return err
		}
		tools, err := marshalDurableJSON(step.Input.Tools)
		if err != nil {
			return err
		}
		hash := executionJSONHash(tools)
		header.ToolsHash = hash
		header.Number, header.CreatedAt = number, step.CreatedAt
		header.MaxOutputBytes, header.MaxArgumentBytes = step.Input.MaxOutputBytes, step.Input.MaxArgumentBytes
		header.MaxToolCalls, header.MaxParallelTools = step.Input.MaxToolCalls, step.Input.MaxParallelTools
		q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationToolCatalogTable).
			Columns("owner_key", "conversation_id", "catalog_hash", "payload_json").
			Values(conversationOwner(claim.Authority), claim.Run.ConversationID, hash, tools).
			OnConflictDoNothing("owner_key", "conversation_id", "catalog_hash").Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		// An existing/concurrently inserted row must still match its content
		// hash. Never overwrite what an earlier frozen step references.
		if _, err = s.readExecutionCatalog(ctx, tx, conversationOwner(claim.Authority), claim.Run.ConversationID, hash); err != nil {
			return err
		}
		input := step.Input
		input.Tools, input.ContextSources = nil, nil
		raw, err := marshalDurableJSON(input)
		if err != nil || len(raw)+len(tools) > 4*1024*1024 {
			return conversationError("bad_request", "execution_payload_invalid")
		}
		projection, err := marshalDurableJSON(stepSourceContext(step.Input))
		if err != nil {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationStepInputTable).
			Columns("owner_key", "conversation_id", "run_id", "step_no", "tools_hash", "payload_json", "source_context_json").
			Values(conversationOwner(claim.Authority), claim.Run.ConversationID, claim.Run.ID, number, hash, raw, projection).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
	} else {
		var saved executionStepHeader
		found, err := s.payloadRead(ctx, tx, conversationRunStepTable, executionScope(claim, number), &saved)
		if err != nil {
			return err
		}
		if !found || saved.Number != number {
			return conversationError("conflict", "step_input_conflict")
		}
		header = saved
	}
	header.Result, header.UpdatedAt = step.Result, step.UpdatedAt
	raw, err := marshalDurableJSON(header)
	if err != nil || len(raw) > 4*1024*1024 {
		return conversationError("bad_request", "execution_payload_invalid")
	}
	if insert {
		q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationRunStepTable).
			Columns("owner_key", "conversation_id", "run_id", "step_no", "payload_json").
			Values(conversationOwner(claim.Authority), claim.Run.ConversationID, claim.Run.ID, number, raw).Build()
		return conversationExec(ctx, tx, q, args, err)
	}
	q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationRunStepTable).Set("payload_json", raw).Where(executionScope(claim, number)).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

// Tool authorization/completion only needs the frozen tool policy and result,
// not the transcript. The public ExecutionStep recovery port uses the full
// reader below instead. No authority or authorization decision is cached here.
func (s *ConversationStore) readExecutionStepPolicy(ctx context.Context, db conversationDB, predicate query.Predicate, out *persistence.ConversationExecutionStep) (bool, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationRunStepTable).Columns("owner_key", "conversation_id", "step_no", "payload_json").Where(predicate).Build()
	if err != nil {
		return false, err
	}
	var owner, conversationID string
	var number int
	var raw []byte
	if err = db.QueryRowContext(ctx, q, args...).Scan(&owner, &conversationID, &number, &raw); err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var header executionStepHeader
	if err = unmarshalDurableJSON(raw, &header); err != nil {
		return false, err
	}
	if header.Number != number {
		return false, conversationError("conflict", "step_input_conflict")
	}
	tools, err := s.readExecutionCatalog(ctx, db, owner, conversationID, header.ToolsHash)
	if err != nil {
		return false, err
	}
	*out = persistence.ConversationExecutionStep{Number: number, Result: header.Result, CreatedAt: header.CreatedAt, UpdatedAt: header.UpdatedAt,
		Input: sdk.ConversationStepRequest{Tools: tools, MaxOutputBytes: header.MaxOutputBytes, MaxArgumentBytes: header.MaxArgumentBytes, MaxToolCalls: header.MaxToolCalls, MaxParallelTools: header.MaxParallelTools}}
	return true, nil
}

func (s *ConversationStore) readExecutionCatalog(ctx context.Context, db conversationDB, owner, conversationID, hash string) ([]sdk.ConversationToolDefinition, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationToolCatalogTable).Columns("payload_json").
		Where(query.And(query.Equal("owner_key", owner), query.Equal("conversation_id", conversationID), query.Equal("catalog_hash", hash))).Build()
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err = db.QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
		return nil, err
	}
	if !artifactSHA(hash) || executionJSONHash(raw) != hash {
		return nil, conversationError("conflict", "tool_catalog_changed")
	}
	var tools []sdk.ConversationToolDefinition
	if err = unmarshalDurableJSON(raw, &tools); err != nil {
		return nil, err
	}
	return tools, nil
}

func (s *ConversationStore) readFrozenExecutionStep(ctx context.Context, db conversationDB, predicate query.Predicate, out *persistence.ConversationExecutionStep) (bool, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationRunStepTable).
		Columns("owner_key", "conversation_id", "run_id", "step_no", "payload_json").Where(predicate).Build()
	if err != nil {
		return false, err
	}
	var owner, conversationID, runID string
	var number int
	var raw []byte
	if err = db.QueryRowContext(ctx, q, args...).Scan(&owner, &conversationID, &runID, &number, &raw); err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var header executionStepHeader
	if err = unmarshalDurableJSON(raw, &header); err != nil {
		return false, err
	}
	identity := query.And(query.Equal("owner_key", owner), query.Equal("conversation_id", conversationID), query.Equal("run_id", runID), query.Equal("step_no", number))
	q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationStepInputTable).Columns("tools_hash", "payload_json").Where(identity).Build()
	if err != nil {
		return false, err
	}
	var hash string
	if err = db.QueryRowContext(ctx, q, args...).Scan(&hash, &raw); err != nil {
		return false, err
	}
	if hash != header.ToolsHash {
		return false, conversationError("conflict", "tool_catalog_changed")
	}
	var input sdk.ConversationStepRequest
	if err = unmarshalDurableJSON(raw, &input); err != nil {
		return false, err
	}
	if input.Tools, err = s.readExecutionCatalog(ctx, db, owner, conversationID, hash); err != nil {
		return false, err
	}
	edges, err := s.readStepSourceEdges(ctx, db, identity)
	if err != nil {
		return false, err
	}
	if len(edges) > 0 {
		input.ContextSources = edges[0].Sources
	}
	actual, err := frozenExecutionInputHash(input)
	if err != nil {
		return false, err
	}
	if header.Number != number || actual != header.InputHash {
		return false, conversationError("conflict", "step_input_conflict")
	}
	*out = persistence.ConversationExecutionStep{Number: number, Input: input, Result: header.Result, CreatedAt: header.CreatedAt, UpdatedAt: header.UpdatedAt}
	return true, nil
}

func (s *ConversationStore) writeStepSourceEdges(ctx context.Context, tx *sql.Tx, claim persistence.ConversationClaim, number int, sources []sdk.ConversationRunReference) error {
	limit := 64
	if number == -1 {
		limit = 256
	}
	if len(sources) > limit {
		return conversationError("bad_request", "source_limit_exceeded")
	}
	for index, ref := range sources {
		if !personalMemoryKey(ref.ConversationID) || !personalMemoryKey(ref.RunID) || ref.BeforeStep < 0 || ref.BeforeStep > 257 {
			return conversationError("bad_request", "source_reference_invalid")
		}
		q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationStepSourceTable).
			Columns("owner_key", "conversation_id", "run_id", "step_no", "source_no", "source_conversation_id", "source_run_id", "before_step").
			Values(conversationOwner(claim.Authority), claim.Run.ConversationID, claim.Run.ID, number, index, ref.ConversationID, ref.RunID, ref.BeforeStep).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationStore) readStepSourceEdges(ctx context.Context, db conversationDB, predicate query.Predicate) ([]persistence.ConversationStepSources, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationStepSourceTable).
		Columns("step_no", "source_no", "source_conversation_id", "source_run_id", "before_step").Where(predicate).
		OrderBy(query.Ascending("step_no"), query.Ascending("source_no")).Limit(256*64 + 257).Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []persistence.ConversationStepSources
	for rows.Next() {
		var number, index int
		var ref sdk.ConversationRunReference
		if err = rows.Scan(&number, &index, &ref.ConversationID, &ref.RunID, &ref.BeforeStep); err != nil {
			return nil, err
		}
		if number < -1 || number > 255 || !personalMemoryKey(ref.ConversationID) || !personalMemoryKey(ref.RunID) || ref.BeforeStep < 0 || ref.BeforeStep > 257 {
			return nil, conversationError("conflict", "source_reference_invalid")
		}
		if len(out) == 0 || out[len(out)-1].Step != number {
			if len(out) == 257 {
				return nil, conversationError("unavailable", "source_limit_exceeded")
			}
			out = append(out, persistence.ConversationStepSources{Step: number})
		}
		last := &out[len(out)-1]
		limit := 64
		if number == -1 {
			limit = 256
		}
		if index != len(last.Sources) || index >= limit {
			return nil, conversationError("conflict", "source_reference_invalid")
		}
		last.Sources = append(last.Sources, ref)
	}
	return out, rows.Err()
}
