package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) conversationAssignments(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, a sdk.ConversationAuthority) ([]sdk.ConversationDelegationAssignment, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(delegationHistoryPredicate(conversationOwner(delegationRecordAuthority(d, a)), conversationItemDelegationAssignment, d.ID, "")).OrderBy(query.Ascending("seq")).Limit(17).Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out := []sdk.ConversationDelegationAssignment{}
	for rows.Next() {
		var raw []byte
		var v sdk.ConversationDelegationAssignment
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal(raw, &v)
		}
		if err != nil {
			break
		}
		out = append(out, v)
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(out) > 16 {
		return nil, conversationError("conflict", "delegation_assignment_invalid")
	}
	return out, nil
}
func (s *ConversationStore) ConversationDelegationAssignments(ctx context.Context, id string, a sdk.ConversationAuthority) ([]sdk.ConversationDelegationAssignment, error) {
	d, _, err := s.participantDelegation(ctx, s.store.Database(), id, a, "view")
	if err != nil {
		return nil, err
	}
	executor, err := s.delegationExecutionAuthority(ctx, s.store.Database(), d, a)
	if err != nil {
		return nil, err
	}
	items, err := s.conversationAssignments(ctx, s.store.Database(), d, executor)
	if err != nil {
		return nil, err
	}
	for i := range items {
		original, err := s.assignmentExecutionAuthority(ctx, s.store.Database(), d, items[i], executor)
		if err != nil {
			return nil, err
		}
		items[i].ExecutionSubject = &sdk.ConversationExecutionSubject{RuntimeID: original.RuntimeID, WorkspaceID: original.WorkspaceID, UserID: original.UserID}
	}
	return items, nil
}
func (s *ConversationStore) insertConversationAssignment(ctx context.Context, tx *sql.Tx, id string, in sdk.ConversationDelegationAssignment, a sdk.ConversationAuthority) error {
	return s.insertConversationAssignmentForExecutor(ctx, tx, id, in, a, a)
}

func (s *ConversationStore) insertConversationAssignmentForExecutor(ctx context.Context, tx *sql.Tx, id string, in sdk.ConversationDelegationAssignment, owner, executor sdk.ConversationAuthority) error {
	if !sameConversationWorkspace(owner, executor) {
		return conversationError("forbidden", "execution_subject_mismatch")
	}
	original, err := s.assignmentTaskAuthority(ctx, tx, id, in, executor)
	if err != nil {
		return err
	}
	record := conversationAssignmentRecord{ConversationDelegationAssignment: in, ExecutionAuthority: &original}
	d, err := s.ownedConversationDelegation(ctx, tx, id, owner)
	if err != nil {
		return err
	}
	return s.insertDelegationHistory(ctx, tx, conversationItemDelegationAssignment, d, "", in.Number, in.Source, record, owner)
}

func (s *ConversationStore) prepareDelegationHandoff(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, remainingWork string, a sdk.ConversationAuthority) (sdk.ConversationDelegationHandoff, error) {
	out := sdk.ConversationDelegationHandoff{RemainingWork: remainingWork, Runs: []sdk.ConversationRunReference{}, Effects: []sdk.ConversationDelegationEffect{}}
	var err error
	a, err = s.delegationExecutionAuthority(ctx, tx, d, a)
	if err != nil {
		return out, err
	}
	assignments, err := s.conversationAssignments(ctx, tx, d, a)
	if err != nil {
		return out, err
	}
	for _, assignment := range assignments {
		executor, err := s.assignmentExecutionAuthority(ctx, tx, d, assignment, a)
		if err != nil {
			return out, err
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), agentRunTable).Columns("payload_json").Where(conversationRunScope(executor, assignment.ConversationID)).OrderBy(query.Ascending("created_at"), query.Ascending("run_id")).Limit(129).Build()
		if err != nil {
			return out, err
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return out, err
		}
		runs := []sdk.ConversationRun{}
		for rows.Next() {
			var raw []byte
			var run sdk.ConversationRun
			if err = rows.Scan(&raw); err == nil {
				err = json.Unmarshal(raw, &run)
			}
			if err != nil {
				break
			}
			runs = append(runs, run)
		}
		if e := rows.Err(); err == nil {
			err = e
		}
		rows.Close()
		if err != nil {
			return out, err
		}
		for _, run := range runs {
			if !run.Terminal() {
				return out, conversationError("conflict", "delegation_still_running")
			}
			if len(out.Runs) >= 128 {
				return out, conversationError("bad_request", "delegation_handoff_exceeded")
			}
			if run.BackgroundTask == nil || run.BackgroundTask.DelegationID != d.ID || run.BackgroundTask.TaskID != assignment.TaskID {
				return out, conversationError("conflict", "delegation_assignment_invalid")
			}
			out.Runs = append(out.Runs, sdk.ConversationRunReference{ConversationID: run.ConversationID, RunID: run.ID})
			q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationRunStepTable).Columns("payload_json").Where(query.And(conversationRunStepKindPredicate(conversationRunStepKindTool), conversationScope(executor, run.ConversationID), query.Equal("run_id", run.ID))).Build()
			if err != nil {
				return out, err
			}
			rows, err = tx.QueryContext(ctx, q, args...)
			if err != nil {
				return out, err
			}
			calls := []persistence.ConversationToolExecution{}
			for rows.Next() {
				var raw []byte
				var call persistence.ConversationToolExecution
				if err = rows.Scan(&raw); err == nil {
					err = json.Unmarshal(raw, &call)
				}
				if err != nil {
					break
				}
				calls = append(calls, call)
			}
			if e := rows.Err(); err == nil {
				err = e
			}
			rows.Close()
			if err != nil {
				return out, err
			}
			sort.Slice(calls, func(i, j int) bool {
				if calls[i].Step != calls[j].Step {
					return calls[i].Step < calls[j].Step
				}
				return calls[i].Call.ID < calls[j].Call.ID
			})
			for _, call := range calls {
				if call.Definition.Effect != "write" {
					continue
				}
				if call.ReusedFrom != nil {
					continue
				}
				if call.State != "completed" || call.Result == nil || call.Result.Status == "uncertain" {
					return out, conversationError("conflict", "delegation_reconciliation_required")
				}
				out.Effects = append(out.Effects, sdk.ConversationDelegationEffect{Tool: call.Call.Name, Arguments: call.Call.Arguments, Status: call.Result.Status, Completion: call.Result.Completion, ResourceID: call.Result.ResourceID, Reference: sdk.ConversationResultReference{ConversationID: run.ConversationID, RunID: run.ID, Step: call.Step, CallID: call.Call.ID, SHA256: conversationHash(call.Result)}})
			}
		}
	}
	return out, nil
}
func (s *ConversationStore) PrepareConversationDelegationHandoff(ctx context.Context, id string, revision int64, remainingWork string, a sdk.ConversationAuthority) (sdk.ConversationDelegationHandoff, error) {
	var out sdk.ConversationDelegationHandoff
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		d, _, err := s.transferDelegationActor(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if d.Revision != revision {
			return conversationError("conflict", "revision_conflict")
		}
		out, err = s.prepareDelegationHandoff(ctx, tx, d, remainingWork, a)
		return err
	})
	return out, err
}
