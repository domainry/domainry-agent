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

const sharedSubjectStepsTable = "_subject_steps"

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
	conversationContractPublicationTable, conversationSourceReleaseTable, conversationAgentMessageTable, conversationAgreementTable, conversationAssignmentTable, conversationDeliveryRecordTable, conversationDisagreementTable, conversationDelegationTable, conversationAgentTable, conversationCollaborationMutationTable,
	"_agent_conversation_interactions", conversationRunStepTable,
	conversationItemTable, agentRunTable, conversationFollowUpEventTable,
	conversationFollowUpStateTable, conversationTaskPlanTable, conversationTaskTable, "_agent_conversations", conversationMemoryChangeTable, "_agent_user_memories",
}

func agentSubjectOwnerPredicate(table, owner string) query.Predicate {
	owned := query.Equal("owner_key", owner)
	if table == agentRunTable {
		return query.And(agentRunKindPredicate(agentRunKindConversation), owned)
	}
	if table == conversationSourceReleaseTable {
		return query.Or(owned, query.Equal("producer_key", owner))
	}
	return owned
}

func agentSubjectIndexedTables() map[string][]string {
	return map[string][]string{
		conversationDelegationSubjectTable: {"owner_key", "delegation_id", "execution_owner_key", "source_authority_json", "execution_authority_json", "created_at"},
		conversationParticipantTable:       {"owner_key", "delegation_id", "viewer_key", "owner_user_id", "revision", "created_at"},
		conversationAgentGrantTable:        {"owner_key", "agent_id", "viewer_key", "owner_user_id"},
	}
}

func agentSubjectIndexPredicate(table, owner string) query.Predicate {
	other := "viewer_key"
	if table == conversationDelegationSubjectTable {
		other = "execution_owner_key"
	}
	return query.Or(query.Equal("owner_key", owner), query.Equal(other, owner))
}

func (s SubjectLifecycle) PreviewSubject(ctx context.Context, workspaceID, subjectID string) (json.RawMessage, error) {
	a, err := s.authority(workspaceID, subjectID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for table := range agentSubjectIndexedTables() {
		statement, args, err := query.NewSelectBuilder(s.store.Renderer(), table).Projections(query.Project(query.CountAll())).Where(agentSubjectIndexPredicate(table, conversationOwner(a))).Build()
		if err != nil {
			return nil, err
		}
		var count int64
		if err := s.store.Database().QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
			return nil, err
		}
		counts[table] = count
	}
	for _, table := range agentSubjectOwnerTables {
		statement, args, buildErr := query.NewSelectBuilder(s.store.Renderer(), table).Projections(query.Project(query.CountAll())).Where(agentSubjectOwnerPredicate(table, conversationOwner(a))).Build()
		if buildErr != nil {
			return nil, buildErr
		}
		var count int64
		if err := s.store.Database().QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
			return nil, err
		}
		counts[table] = count
	}
	for key, table := range map[string]string{"runtime_states": "_agent_runtime_states", "interactive_runs": agentRunTable} {
		predicate := query.Equal("user_id", a.UserID)
		if table == agentRunTable {
			predicate = query.And(agentRunKindPredicate(agentRunKindInteractive), predicate)
		}
		statement, args, buildErr := query.NewWorkspaceSelectBuilder(s.store.Renderer(), table, a.WorkspaceID).Projections(query.Project(query.CountAll())).Where(predicate).Build()
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
	for table, columns := range agentSubjectIndexedTables() {
		statement, args, err := query.NewSelectBuilder(s.store.Renderer(), table).Columns(columns...).Where(agentSubjectIndexPredicate(table, conversationOwner(a))).Build()
		if err != nil {
			return nil, err
		}
		rows, err := s.store.Database().QueryContext(ctx, statement, args...)
		if err != nil {
			return nil, err
		}
		items, readErr := subjectIndexRecords(rows, columns)
		rows.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(items) > 0 {
			export[table] = items
		}
	}
	for _, table := range agentSubjectOwnerTables {
		items, readErr := s.payloads(ctx, table, agentSubjectOwnerPredicate(table, conversationOwner(a)))
		if readErr != nil {
			return nil, readErr
		}
		if len(items) > 0 {
			export[table] = items
		}
	}
	for key, table := range map[string]string{"runtime_states": "_agent_runtime_states", "interactive_runs": agentRunTable} {
		predicate := query.And(query.Equal("workspace_id", a.WorkspaceID), query.Equal("user_id", a.UserID))
		if table == agentRunTable {
			predicate = query.And(agentRunKindPredicate(agentRunKindInteractive), predicate)
		}
		items, readErr := s.payloads(ctx, table, predicate)
		if readErr != nil {
			return nil, readErr
		}
		if len(items) > 0 {
			export[key] = items
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
		collaboration := &ConversationStore{store: s.store}
		if err := collaboration.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
			return err
		}
		lookup, args, buildErr := query.NewWorkspaceSelectBuilder(s.store.Renderer(), sharedSubjectStepsTable, a.WorkspaceID).
			Columns("payload_json").
			Where(query.And(query.Equal("request_id", requestID), query.Equal("owner", "agent"), query.Equal("operation", "erase"))).Build()
		if buildErr != nil {
			return buildErr
		}
		var stored string
		if scanErr := tx.QueryRowContext(ctx, lookup, args...).Scan(&stored); scanErr == nil {
			var step lifecyclemodel.SubjectExecutionStep
			if json.Unmarshal([]byte(stored), &step) != nil || step.WorkspaceID != a.WorkspaceID || step.RequestID != requestID || step.Owner != "agent" || step.Operation != "erase" || !json.Valid(step.Payload) {
				return fmt.Errorf("Agent shared subject execution step is invalid")
			}
			receipt = append(json.RawMessage(nil), step.Payload...)
			return nil
		} else if !errors.Is(scanErr, sql.ErrNoRows) {
			return scanErr
		}
		changed := map[string]int64{}
		if err := collaboration.eraseSubjectCollaboration(ctx, tx, a, changed); err != nil {
			return err
		}
		for table := range agentSubjectIndexedTables() {
			statement, args, err := query.NewDeleteBuilder(s.store.Renderer(), table).Where(agentSubjectIndexPredicate(table, owner)).Build()
			if err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, statement, args...)
			if err != nil {
				return err
			}
			changed[table], _ = result.RowsAffected()
		}
		for _, table := range agentSubjectOwnerTables {
			statement, deleteArgs, deleteErr := query.NewDeleteBuilder(s.store.Renderer(), table).Where(agentSubjectOwnerPredicate(table, owner)).Build()
			if deleteErr != nil {
				return deleteErr
			}
			result, deleteErr := tx.ExecContext(ctx, statement, deleteArgs...)
			if deleteErr != nil {
				return deleteErr
			}
			changed[table], _ = result.RowsAffected()
		}
		for key, table := range map[string]string{"runtime_states": "_agent_runtime_states", "interactive_runs": agentRunTable} {
			predicate := query.Equal("user_id", a.UserID)
			if table == agentRunTable {
				predicate = query.And(agentRunKindPredicate(agentRunKindInteractive), predicate)
			}
			statement, deleteArgs, deleteErr := query.NewWorkspaceDeleteBuilder(s.store.Renderer(), table, a.WorkspaceID).Where(predicate).Build()
			if deleteErr != nil {
				return deleteErr
			}
			result, deleteErr := tx.ExecContext(ctx, statement, deleteArgs...)
			if deleteErr != nil {
				return deleteErr
			}
			changed[key], _ = result.RowsAffected()
		}
		completedAt := time.Now().UTC()
		receipt, _ = json.Marshal(map[string]any{"request_id": requestID, "changed": changed, "completed_at": completedAt})
		step, marshalErr := json.Marshal(lifecyclemodel.SubjectExecutionStep{WorkspaceID: a.WorkspaceID, RequestID: requestID, Owner: "agent", Operation: "erase", Payload: append(json.RawMessage(nil), receipt...), CompletedAt: completedAt})
		if marshalErr != nil {
			return marshalErr
		}
		statement, insertArgs, buildErr := query.NewWorkspaceInsertBuilder(s.store.Renderer(), sharedSubjectStepsTable, a.WorkspaceID).
			Columns("request_id", "owner", "operation", "payload_json", "completed_at").
			Values(requestID, "agent", "erase", string(step), completedAt.Format(time.RFC3339Nano)).Build()
		if buildErr != nil {
			return buildErr
		}
		_, buildErr = tx.ExecContext(ctx, statement, insertArgs...)
		return buildErr
	})
	return receipt, err
}

var _ lifecyclecontract.SubjectExecutionHandler = SubjectLifecycle{}
