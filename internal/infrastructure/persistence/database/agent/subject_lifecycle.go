package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-orm/query"
)

type SubjectLifecycle struct {
	store     *Store
	runtimeID string
}

func NewSubjectLifecycle(store *Store, runtimeID string) SubjectLifecycle {
	return SubjectLifecycle{store: store, runtimeID: strings.TrimSpace(runtimeID)}
}

func (SubjectLifecycle) Owner(context.Context) string { return "agent" }

func (s SubjectLifecycle) authority(workspaceID, subjectID string) (agentsdk.ConversationAuthority, error) {
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: s.runtimeID, WorkspaceID: strings.TrimSpace(workspaceID), UserID: strings.TrimSpace(subjectID)}
	if s.store == nil || s.runtimeID == "" || conversationAuthority(a) != nil {
		return agentsdk.ConversationAuthority{}, fmt.Errorf("Agent subject scope is required")
	}
	return a, nil
}

var agentSubjectOwnerTables = []string{
	"_agent_conversation_interactions", "_agent_conversation_tool_calls", "_agent_conversation_steps",
	"_agent_conversation_events", "_agent_conversation_summaries", "_agent_conversation_messages",
	"_agent_conversation_inputs", "_agent_conversation_runs", conversationFollowUpEventTable,
	conversationFollowUpStateTable, conversationTaskTable, "_agent_conversations", "_agent_user_memories",
}

func (s SubjectLifecycle) PreviewSubject(ctx context.Context, workspaceID, subjectID string) (json.RawMessage, error) {
	a, err := s.authority(workspaceID, subjectID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for _, table := range agentSubjectOwnerTables {
		statement, args, buildErr := query.NewSelectBuilder(s.store.Renderer(), table).Projections(query.Project(query.CountAll())).Where(query.Equal("owner_key", conversationOwner(a))).Build()
		if buildErr != nil {
			return nil, buildErr
		}
		var count int64
		if err := s.store.Database().QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
			return nil, err
		}
		counts[table] = count
	}
	for key, table := range map[string]string{"runtime_states": "_agent_runtime_states", "interactive_runs": "_agent_interactive_runs"} {
		statement, args, buildErr := query.NewWorkspaceSelectBuilder(s.store.Renderer(), table, a.WorkspaceID).Projections(query.Project(query.CountAll())).Where(query.Equal("user_id", a.UserID)).Build()
		if buildErr != nil {
			return nil, buildErr
		}
		var count int64
		if err := s.store.Database().QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
			return nil, err
		}
		counts[key] = count
	}
	return json.Marshal(counts)
}

func (s SubjectLifecycle) ExportSubjectForRequest(ctx context.Context, _ string, workspaceID, subjectID string) (json.RawMessage, error) {
	a, err := s.authority(workspaceID, subjectID)
	if err != nil {
		return nil, err
	}
	export := map[string][]json.RawMessage{}
	for _, table := range agentSubjectOwnerTables {
		items, readErr := s.payloads(ctx, table, query.Equal("owner_key", conversationOwner(a)))
		if readErr != nil {
			return nil, readErr
		}
		if len(items) > 0 {
			export[table] = items
		}
	}
	for _, table := range []string{"_agent_runtime_states", "_agent_interactive_runs"} {
		items, readErr := s.payloads(ctx, table, query.And(query.Equal("workspace_id", a.WorkspaceID), query.Equal("user_id", a.UserID)))
		if readErr != nil {
			return nil, readErr
		}
		if len(items) > 0 {
			export[table] = items
		}
	}
	return json.Marshal(map[string]any{"records": export})
}

func (s SubjectLifecycle) payloads(ctx context.Context, table string, predicate query.Predicate) ([]json.RawMessage, error) {
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), table).Columns("payload_json").Where(predicate).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		items = append(items, append(json.RawMessage(nil), raw...))
	}
	return items, rows.Err()
}

func (s SubjectLifecycle) EraseSubjectForRequest(ctx context.Context, requestID, workspaceID, subjectID string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	if len(holds) > 0 {
		return nil, fmt.Errorf("Agent subject erasure blocked by legal hold")
	}
	a, err := s.authority(workspaceID, subjectID)
	requestID = strings.TrimSpace(requestID)
	if err != nil || requestID == "" || len(requestID) > 96 {
		return nil, fmt.Errorf("Agent subject erasure request is required")
	}
	owner := conversationOwner(a)
	var receipt json.RawMessage
	err = (&ConversationStore{store: s.store}).transaction(ctx, func(tx *sql.Tx) error {
		lookup, args, buildErr := query.NewSelectBuilder(s.store.Renderer(), agentSubjectReceiptTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", owner), query.Equal("request_id", requestID))).Build()
		if buildErr != nil {
			return buildErr
		}
		if scanErr := tx.QueryRowContext(ctx, lookup, args...).Scan(&receipt); scanErr == nil {
			return nil
		} else if !errors.Is(scanErr, sql.ErrNoRows) {
			return scanErr
		}
		changed := map[string]int64{}
		for _, table := range agentSubjectOwnerTables {
			statement, deleteArgs, deleteErr := query.NewDeleteBuilder(s.store.Renderer(), table).Where(query.Equal("owner_key", owner)).Build()
			if deleteErr != nil {
				return deleteErr
			}
			result, deleteErr := tx.ExecContext(ctx, statement, deleteArgs...)
			if deleteErr != nil {
				return deleteErr
			}
			changed[table], _ = result.RowsAffected()
		}
		for key, table := range map[string]string{"runtime_states": "_agent_runtime_states", "interactive_runs": "_agent_interactive_runs"} {
			statement, deleteArgs, deleteErr := query.NewWorkspaceDeleteBuilder(s.store.Renderer(), table, a.WorkspaceID).Where(query.Equal("user_id", a.UserID)).Build()
			if deleteErr != nil {
				return deleteErr
			}
			result, deleteErr := tx.ExecContext(ctx, statement, deleteArgs...)
			if deleteErr != nil {
				return deleteErr
			}
			changed[key], _ = result.RowsAffected()
		}
		receipt, _ = json.Marshal(map[string]any{"request_id": requestID, "changed": changed, "completed_at": time.Now().UTC()})
		statement, insertArgs, buildErr := query.NewInsertBuilder(s.store.Renderer(), agentSubjectReceiptTable).Columns("owner_key", "request_id", "payload_json").Values(owner, requestID, receipt).Build()
		if buildErr != nil {
			return buildErr
		}
		_, buildErr = tx.ExecContext(ctx, statement, insertArgs...)
		return buildErr
	})
	return receipt, err
}

var _ lifecyclecontract.SubjectExecutionHandler = SubjectLifecycle{}
