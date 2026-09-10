package agent

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

const todoTable = "_agent_user_todos"

func todoScope(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("todo_id", id))
}

func (s *ConversationStore) Todo(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodo, error) {
	if err := conversationAuthority(a); err != nil {
		return agentsdk.ConversationTodo{}, err
	}
	return s.todo(ctx, s.store.Database(), id, a)
}
func (s *ConversationStore) todo(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodo, error) {
	var out agentsdk.ConversationTodo
	if !personalMemoryKey(id) {
		return out, conversationError("bad_request", "todo_invalid")
	}
	found, err := s.executionRead(ctx, db, todoTable, todoScope(a, id), &out)
	if err == nil && !found {
		err = conversationError("not_found", "todo_not_found")
	}
	return out, err
}

type todoCursor struct {
	Owner, Query, Batch string
	Created, Cutoff     int64
	Position            int
}

func (s *ConversationStore) Todos(ctx context.Context, in agentsdk.ConversationTodoQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodoPage, error) {
	out := agentsdk.ConversationTodoPage{Items: []agentsdk.ConversationTodo{}, Complete: true}
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	if in.Status == "" {
		in.Status = "all"
	}
	if !executionText(in.Query, 256, false) || len(in.Cursor) > 2048 || in.Limit < 1 || in.Limit > 50 || in.Status != "all" && in.Status != "open" && in.Status != "completed" || in.BatchID != "" && !personalMemoryKey(in.BatchID) || in.SourceConversationID != "" && !personalMemoryKey(in.SourceConversationID) {
		return out, conversationError("bad_request", "todo_query_invalid")
	}
	key := in
	key.Cursor = ""
	owner, hash := conversationOwner(a), conversationHash(key)
	cursor := todoCursor{Owner: owner, Query: hash, Cutoff: time.Now().UnixMilli()}
	filters := []query.Predicate{query.Equal("owner_key", owner)}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Owner != owner || cursor.Query != hash || !personalMemoryKey(cursor.Batch) || cursor.Position < 1 || cursor.Created < 1 || cursor.Cutoff < cursor.Created {
			return out, conversationError("bad_request", "todo_cursor_invalid")
		}
		filters = append(filters, query.Or(query.LessThan("created_at", cursor.Created), query.And(query.Equal("created_at", cursor.Created), query.LessThan("batch_id", cursor.Batch)), query.And(query.Equal("created_at", cursor.Created), query.Equal("batch_id", cursor.Batch), query.GreaterThan("position", cursor.Position))))
	}
	filters = append(filters, query.LessThanOrEqual("created_at", cursor.Cutoff))
	if in.Status != "all" {
		filters = append(filters, query.Equal("status", in.Status))
	}
	if in.BatchID != "" {
		filters = append(filters, query.Equal("batch_id", in.BatchID))
	}
	if in.SourceConversationID != "" {
		filters = append(filters, query.Equal("source_conversation_id", in.SourceConversationID))
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), todoTable).Columns("payload_json").Where(query.And(filters...)).OrderBy(query.Descending("created_at"), query.Descending("batch_id"), query.Ascending("position")).Limit(301).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	scanned, size := 0, 0
	needle := strings.ToLower(in.Query)
	for rows.Next() {
		if scanned >= 300 || len(out.Items) >= in.Limit {
			out.Complete = false
			break
		}
		var raw []byte
		var item agentsdk.ConversationTodo
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return out, err
		}
		matches := strings.Contains(strings.ToLower(item.Title+"\n"+item.Description), needle)
		if matches && size+len(raw) > 24576 && len(out.Items) > 0 {
			out.Complete = false
			break
		}
		scanned++
		cursor.Created, cursor.Batch, cursor.Position = item.CreatedAt.UnixMilli(), item.BatchID, item.Position
		if matches {
			out.Items = append(out.Items, item)
			size += len(raw)
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if !out.Complete {
		out.NextCursor = base64.RawURLEncoding.EncodeToString(conversationJSON(cursor))
	}
	return out, nil
}

func validTodo(in agentsdk.ConversationTodoInput) error {
	if !executionText(in.Title, 128, true) || !executionText(in.Description, 1024, false) || !executionText(in.Timezone, 128, true) || in.Timezone == "Local" || len(in.DueDate) > 10 || len(in.DueAt) > 64 || in.DueDate != "" && in.DueAt != "" {
		return conversationError("bad_request", "todo_invalid")
	}
	zone, err := time.LoadLocation(in.Timezone)
	if err != nil {
		return conversationError("bad_request", "todo_timezone_invalid")
	}
	if in.DueDate != "" {
		parsed, err := time.Parse("2006-01-02", in.DueDate)
		if err != nil || parsed.Format("2006-01-02") != in.DueDate {
			return conversationError("bad_request", "todo_date_invalid")
		}
	}
	if in.DueAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, in.DueAt)
		if err != nil {
			return conversationError("bad_request", "todo_date_invalid")
		}
		_, supplied := parsed.Zone()
		_, actual := parsed.In(zone).Zone()
		if supplied != actual {
			return conversationError("bad_request", "todo_timezone_mismatch")
		}
	}
	return nil
}

func (s *ConversationStore) createTodos(ctx context.Context, tx *sql.Tx, items []agentsdk.ConversationTodoInput, source, run, key string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodoBatch, error) {
	out := agentsdk.ConversationTodoBatch{BatchID: "tb_" + conversationHash([]string{conversationOwner(a), key})[:32], Items: []agentsdk.ConversationTodo{}}
	if len(items) < 1 || len(items) > 20 {
		return out, conversationError("bad_request", "todo_batch_invalid")
	}
	for _, item := range items {
		if err := validTodo(item); err != nil {
			return out, err
		}
	}
	if source != "" {
		if _, err := s.get(ctx, tx, source, a); err != nil {
			return out, err
		}
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), todoTable).Projections(query.Project(query.CountAll())).Where(query.Equal("owner_key", conversationOwner(a))).Build()
	if err != nil {
		return out, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
		return out, err
	}
	if count+len(items) > 1000 {
		return out, conversationError("conflict", "todo_limit")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	for index, in := range items {
		item := agentsdk.ConversationTodo{ID: "todo_" + conversationHash([]any{out.BatchID, index})[:32], Title: in.Title, Description: in.Description, Status: "open", DueDate: in.DueDate, DueAt: in.DueAt, Timezone: in.Timezone, Revision: 1, BatchID: out.BatchID, Position: index + 1, SourceConversationID: source, SourceRunID: run, CreatedAt: now, UpdatedAt: now}
		statement, args, err = query.NewInsertBuilder(s.store.Renderer(), todoTable).Columns("owner_key", "todo_id", "batch_id", "position", "created_at", "source_conversation_id", "status", "revision", "payload_json").Values(conversationOwner(a), item.ID, item.BatchID, item.Position, now.UnixMilli(), source, item.Status, item.Revision, conversationJSON(item)).Build()
		if err = conversationExec(ctx, tx, statement, args, err); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *ConversationStore) updateTodo(ctx context.Context, tx *sql.Tx, id string, revision int64, patch agentsdk.ConversationTodoPatch, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodo, error) {
	item, err := s.todo(ctx, tx, id, a)
	if err != nil {
		return item, err
	}
	if revision < 1 || item.Revision != revision {
		return item, conversationError("conflict", "revision_conflict")
	}
	if patch.Title == nil && patch.Description == nil && patch.Status == nil && patch.DueDate == nil && patch.DueAt == nil && patch.Timezone == nil {
		return item, conversationError("bad_request", "todo_patch_invalid")
	}
	if patch.Title != nil {
		item.Title = *patch.Title
	}
	if patch.Description != nil {
		item.Description = *patch.Description
	}
	if patch.Timezone != nil {
		item.Timezone = *patch.Timezone
	}
	if patch.DueDate != nil && patch.DueAt != nil {
		item.DueDate, item.DueAt = *patch.DueDate, *patch.DueAt
	} else if patch.DueDate != nil {
		item.DueDate, item.DueAt = *patch.DueDate, ""
	} else if patch.DueAt != nil {
		item.DueDate, item.DueAt = "", *patch.DueAt
	}
	if patch.DueDate != nil && patch.DueAt != nil && *patch.DueDate != "" && *patch.DueAt != "" {
		return item, conversationError("bad_request", "todo_date_invalid")
	}
	if err = validTodo(agentsdk.ConversationTodoInput{Title: item.Title, Description: item.Description, DueDate: item.DueDate, DueAt: item.DueAt, Timezone: item.Timezone}); err != nil {
		return item, err
	}
	now := time.Now().UTC()
	if patch.Status != nil {
		if *patch.Status != "open" && *patch.Status != "completed" {
			return item, conversationError("bad_request", "todo_status_invalid")
		}
		if item.Status != *patch.Status {
			item.Status = *patch.Status
			item.CompletedAt = nil
			if item.Status == "completed" {
				item.CompletedAt = &now
			}
		}
	}
	item.Revision++
	item.UpdatedAt = now
	statement, args, err := query.NewUpdateBuilder(s.store.Renderer(), todoTable).Set("status", item.Status).Set("revision", item.Revision).Set("payload_json", conversationJSON(item)).Where(query.And(todoScope(a, id), query.Equal("revision", revision))).Build()
	return item, conversationCAS(ctx, tx, statement, args, err)
}

func (s *ConversationStore) deleteTodo(ctx context.Context, tx *sql.Tx, id string, revision int64, a agentsdk.ConversationAuthority) error {
	if !personalMemoryKey(id) || revision < 1 {
		return conversationError("bad_request", "todo_invalid")
	}
	statement, args, err := query.NewDeleteBuilder(s.store.Renderer(), todoTable).Where(query.And(todoScope(a, id), query.Equal("revision", revision))).Build()
	return conversationCAS(ctx, tx, statement, args, err)
}

// UI mutation receipts live independently from conversations so deleting a
// conversation cannot make a retried personal todo creation run again.
func (s *ConversationStore) todoMutation(ctx context.Context, clientID, operation string, input any, a agentsdk.ConversationAuthority, apply func(*sql.Tx) (any, error), out any) error {
	if err := conversationAuthority(a); err != nil {
		return err
	}
	if !personalMemoryKey(clientID) {
		return conversationError("bad_request", "todo_client_id_required")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var receipt struct {
			Hash   string
			Result json.RawMessage
		}
		hash := conversationHash([]any{operation, input})
		found, err := s.executionRead(ctx, tx, "_agent_todo_mutations", query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("client_key", conversationHash(clientID))), &receipt)
		if err != nil {
			return err
		}
		if found {
			if receipt.Hash != hash {
				return conversationError("conflict", "idempotency_conflict")
			}
			return json.Unmarshal(receipt.Result, out)
		}
		value, err := apply(tx)
		if err != nil {
			return err
		}
		receipt.Hash = hash
		receipt.Result = conversationJSON(value)
		statement, args, err := query.NewInsertBuilder(s.store.Renderer(), "_agent_todo_mutations").Columns("owner_key", "client_key", "created_at", "payload_json").Values(conversationOwner(a), conversationHash(clientID), time.Now().UnixMilli(), conversationJSON(receipt)).Build()
		if err = conversationExec(ctx, tx, statement, args, err); err != nil {
			return err
		}
		return json.Unmarshal(receipt.Result, out)
	})
}
func (s *ConversationStore) CreateTodos(ctx context.Context, in agentsdk.ConversationTodoCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodoBatch, error) {
	var out agentsdk.ConversationTodoBatch
	err := s.todoMutation(ctx, in.ClientID, "create", in, a, func(tx *sql.Tx) (any, error) {
		return s.createTodos(ctx, tx, in.Items, in.SourceConversationID, "", "todo-ui:"+in.ClientID, a)
	}, &out)
	return out, err
}
func (s *ConversationStore) UpdateTodo(ctx context.Context, id string, in agentsdk.ConversationTodoUpdate, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodo, error) {
	var out agentsdk.ConversationTodo
	err := s.todoMutation(ctx, in.ClientID, "update", []any{id, in}, a, func(tx *sql.Tx) (any, error) { return s.updateTodo(ctx, tx, id, in.ExpectedRevision, in.Patch, a) }, &out)
	return out, err
}
func (s *ConversationStore) DeleteTodo(ctx context.Context, id string, in agentsdk.ConversationTodoDelete, a agentsdk.ConversationAuthority) error {
	var out map[string]bool
	return s.todoMutation(ctx, in.ClientID, "delete", []any{id, in}, a, func(tx *sql.Tx) (any, error) {
		err := s.deleteTodo(ctx, tx, id, in.ExpectedRevision, a)
		return map[string]bool{"deleted": err == nil}, err
	}, &out)
}

func (s *ConversationStore) applyTodoTool(ctx context.Context, tx *sql.Tx, in agentsdk.ConversationToolRequest, call persistence.ConversationToolExecution) (agentsdk.ConversationToolResult, error) {
	var args struct {
		ID               string                           `json:"id"`
		Items            []agentsdk.ConversationTodoInput `json:"items"`
		ExpectedRevision int64                            `json:"expected_revision"`
		Patch            agentsdk.ConversationTodoPatch   `json:"patch"`
	}
	if err := json.Unmarshal([]byte(call.Call.Arguments), &args); err != nil {
		return agentsdk.ConversationToolResult{}, conversationError("bad_request", "todo_invalid")
	}
	var value any
	var resource string
	var err error
	switch call.Call.Name {
	case "todo_create":
		var batch agentsdk.ConversationTodoBatch
		batch, err = s.createTodos(ctx, tx, args.Items, in.ConversationID, in.RunID, call.IdempotencyKey, in.Authority)
		for index := range batch.Items {
			batch.Items[index].Description = ""
		}
		value, resource = batch, batch.BatchID
	case "todo_update":
		value, err = s.updateTodo(ctx, tx, args.ID, args.ExpectedRevision, args.Patch, in.Authority)
		resource = args.ID
	case "todo_delete":
		err = s.deleteTodo(ctx, tx, args.ID, args.ExpectedRevision, in.Authority)
		value = map[string]any{"id": args.ID, "deleted": err == nil}
		resource = args.ID
	default:
		err = fmt.Errorf("unsupported personal todo tool")
	}
	return agentsdk.ConversationToolResult{Status: "completed", Content: conversationJSON(value), ResourceID: resource}, err
}

var _ persistence.ConversationTodoRepository = (*ConversationStore)(nil)
