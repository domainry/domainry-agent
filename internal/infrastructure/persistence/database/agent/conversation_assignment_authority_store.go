package agent

import (
	"context"
	"database/sql"
	"errors"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

// Routing proof belongs to the persisted assignment, not the public history
// DTO. A later assignee or reading role must not replace the original executor.
type conversationAssignmentRecord struct {
	sdk.ConversationDelegationAssignment
	ExecutionAuthority *sdk.ConversationAuthority `json:"execution_authority,omitempty"`
}

func assignmentFactHash(in sdk.ConversationDelegationAssignment) string {
	in.ExecutionSubject = nil // This public field is projected from the private task proof.
	return conversationHash(in)
}

func (s *ConversationStore) assignmentTaskAuthority(ctx context.Context, db conversationDB, id string, assignment sdk.ConversationDelegationAssignment, routing sdk.ConversationAuthority) (sdk.ConversationAuthority, error) {
	if conversationAuthority(routing) != nil {
		return sdk.ConversationAuthority{}, conversationError("forbidden", "execution_subject_mismatch")
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(routing), assignment.TaskID)).Build()
	if err != nil {
		return sdk.ConversationAuthority{}, err
	}
	row, err := scanConversationTask(db.QueryRowContext(ctx, q, args...))
	if err != nil {
		return sdk.ConversationAuthority{}, err
	}
	if conversationAuthority(row.authority) != nil || conversationOwner(row.authority) != conversationOwner(routing) || row.task.DelegationID != id || row.task.ExecutionConversationID != assignment.ConversationID || row.task.Agent == nil || row.task.Agent.ID != assignment.AgentID || row.task.Agent.Revision != assignment.AgentRevision {
		return sdk.ConversationAuthority{}, conversationError("conflict", "delegation_assignment_invalid")
	}
	return row.authority, nil
}

func (s *ConversationStore) assignmentExecutionAuthority(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, assignment sdk.ConversationDelegationAssignment, a sdk.ConversationAuthority) (sdk.ConversationAuthority, error) {
	owner := delegationRecordAuthority(d, a)
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(
		delegationHistoryPredicate(conversationOwner(owner), conversationItemDelegationAssignment, d.ID, ""),
		query.Equal("seq", assignment.Number),
	)).Build()
	if err != nil {
		return sdk.ConversationAuthority{}, err
	}
	var raw []byte
	var record conversationAssignmentRecord
	err = db.QueryRowContext(ctx, q, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return sdk.ConversationAuthority{}, conversationError("conflict", "delegation_assignment_invalid")
	} else if err != nil {
		return sdk.ConversationAuthority{}, err
	} else {
		if err := unmarshalDurableJSON(raw, &record); err != nil {
			return sdk.ConversationAuthority{}, err
		}
		if assignmentFactHash(record.ConversationDelegationAssignment) != assignmentFactHash(assignment) {
			return sdk.ConversationAuthority{}, conversationError("conflict", "delegation_assignment_invalid")
		}
	}
	if record.ExecutionAuthority == nil {
		return sdk.ConversationAuthority{}, conversationError("conflict", "delegation_assignment_invalid")
	}
	routing := *record.ExecutionAuthority
	if !sameConversationWorkspace(owner, routing) {
		return sdk.ConversationAuthority{}, conversationError("forbidden", "execution_subject_mismatch")
	}
	original, err := s.assignmentTaskAuthority(ctx, db, d.ID, assignment, routing)
	if err != nil {
		return sdk.ConversationAuthority{}, err
	}
	if original != *record.ExecutionAuthority {
		return sdk.ConversationAuthority{}, conversationError("forbidden", "execution_subject_mismatch")
	}
	if assignment.ExecutionSubject != nil && *assignment.ExecutionSubject != (sdk.ConversationExecutionSubject{RuntimeID: original.RuntimeID, WorkspaceID: original.WorkspaceID, UserID: original.UserID}) {
		return sdk.ConversationAuthority{}, conversationError("forbidden", "execution_subject_mismatch")
	}
	return original, nil
}

func (s *ConversationStore) assignmentRunAuthority(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, conversationID string, a sdk.ConversationAuthority) (sdk.ConversationAuthority, error) {
	assignments, err := s.conversationAssignments(ctx, db, d, a)
	if err != nil {
		return sdk.ConversationAuthority{}, err
	}
	for _, assignment := range assignments {
		if assignment.ConversationID == conversationID {
			return s.assignmentExecutionAuthority(ctx, db, d, assignment, a)
		}
	}
	return sdk.ConversationAuthority{}, conversationError("conflict", "delegation_assignment_invalid")
}
