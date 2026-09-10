package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

type executionReadCursor struct {
	Owner, Query, CallKey string
	Step                  int
	EventSeq              int64
}

func (s *ConversationStore) ReadExecutionCall(ctx context.Context, conversationID, runID string, step int, callID string, a agentsdk.ConversationAuthority) (persistence.ConversationToolExecution, error) {
	var out persistence.ConversationToolExecution
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if !personalMemoryKey(conversationID) || !personalMemoryKey(runID) || step < 0 || step >= 256 || !executionText(callID, 256, true) {
		return out, conversationError("bad_request", "execution_reference_invalid")
	}
	claim := persistence.ConversationClaim{Authority: a, Run: agentsdk.ConversationRun{ID: runID, ConversationID: conversationID}}
	found, err := s.readExecutionTool(ctx, s.store.Database(), claim, step, callID, &out)
	if err != nil {
		return out, err
	}
	if !found {
		return out, conversationError("not_found", "tool_call_not_found")
	}
	return out, nil
}

func (s *ConversationStore) ReadExecutionCalls(ctx context.Context, in agentsdk.ConversationExecutionRead, a agentsdk.ConversationAuthority) (persistence.ConversationExecutionCallPage, error) {
	out := persistence.ConversationExecutionCallPage{Items: []persistence.ConversationToolExecution{}}
	if !personalMemoryKey(in.ConversationID) || !personalMemoryKey(in.RunID) || in.Limit < 0 || in.Limit > 5 || len(in.Cursor) > 2048 {
		return out, conversationError("bad_request", "execution_reference_invalid")
	}
	run, err := s.Run(ctx, in.ConversationID, in.RunID, a)
	if err != nil {
		return out, err
	}
	if in.Limit == 0 {
		in.Limit = 3
	}
	keyInput := in
	keyInput.Cursor = ""
	cursor := executionReadCursor{Owner: conversationOwner(a), Query: conversationHash(keyInput), EventSeq: run.LastEventSeq}
	predicate := query.And(conversationScope(a, in.ConversationID), query.Equal("run_id", in.RunID))
	if in.Cursor != "" {
		var previous executionReadCursor
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || json.Unmarshal(raw, &previous) != nil || previous.Owner != cursor.Owner || previous.Query != cursor.Query || previous.Step < 0 || previous.Step >= 256 || len(previous.CallKey) != 64 {
			return out, conversationError("bad_request", "execution_cursor_invalid")
		}
		if previous.EventSeq != cursor.EventSeq {
			return out, conversationError("conflict", "execution_cursor_changed")
		}
		predicate = query.And(predicate, query.Or(query.GreaterThan("step_no", previous.Step), query.And(query.Equal("step_no", previous.Step), query.GreaterThan("call_key", previous.CallKey))))
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_tool_calls").Columns("payload_json", "call_key").Where(predicate).OrderBy(query.Ascending("step_no"), query.Ascending("call_key")).Limit(in.Limit + 1).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	out.Complete, out.RunStatus = true, run.Status
	for rows.Next() {
		if len(out.Items) == in.Limit {
			out.Complete = false
			break
		}
		var raw []byte
		var call persistence.ConversationToolExecution
		if err = rows.Scan(&raw, &cursor.CallKey); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &call); err != nil {
			break
		}
		cursor.Step = call.Step
		out.Items = append(out.Items, call)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close() // Release the connection before checking the snapshot again.
	if err != nil {
		return out, err
	}
	latest, err := s.Run(ctx, in.ConversationID, in.RunID, a)
	if err != nil {
		return out, err
	}
	if latest.LastEventSeq != run.LastEventSeq {
		return out, conversationError("conflict", "execution_cursor_changed")
	}
	if !out.Complete {
		out.NextCursor = base64.RawURLEncoding.EncodeToString(conversationJSON(cursor))
	}
	return out, nil
}

var _ persistence.ConversationExecutionReadRepository = (*ConversationStore)(nil)
