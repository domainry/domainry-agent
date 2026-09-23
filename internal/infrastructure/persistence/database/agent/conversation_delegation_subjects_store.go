package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

// These authorities route storage for an already admitted delegation. They
// never replace the caller passed to application or host policy evaluation.
type conversationDelegationSubjects struct {
	source    sdk.ConversationAuthority
	execution sdk.ConversationAuthority
}

func sameConversationWorkspace(a, b sdk.ConversationAuthority) bool {
	return a.RuntimeID == b.RuntimeID && a.WorkspaceID == b.WorkspaceID
}

func delegationRecordAuthority(d sdk.ConversationDelegation, a sdk.ConversationAuthority) sdk.ConversationAuthority {
	if d.OwnerUserID != "" {
		a.UserID = d.OwnerUserID
	}
	return a
}

func (s *ConversationStore) delegationSubjects(ctx context.Context, db conversationDB, id string, a sdk.ConversationAuthority) (conversationDelegationSubjects, bool, error) {
	out := conversationDelegationSubjects{source: a, execution: a}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDelegationSubjectTable).Columns("owner_key", "execution_owner_key", "source_authority_json", "execution_authority_json").Where(query.And(query.Equal("delegation_id", id), query.Or(query.Equal("owner_key", conversationOwner(a)), query.Equal("execution_owner_key", conversationOwner(a))))).Build()
	if err != nil {
		return out, false, err
	}
	var sourceKey, executionKey string
	var source, execution []byte
	if err = db.QueryRowContext(ctx, q, args...).Scan(&sourceKey, &executionKey, &source, &execution); errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	} else if err != nil {
		return out, false, err
	}
	if err = json.Unmarshal(source, &out.source); err != nil {
		return out, false, err
	}
	if err = json.Unmarshal(execution, &out.execution); err != nil {
		return out, false, err
	}
	if conversationAuthority(out.source) != nil || conversationAuthority(out.execution) != nil || !sameConversationWorkspace(a, out.source) || !sameConversationWorkspace(a, out.execution) || conversationOwner(out.source) != sourceKey || conversationOwner(out.execution) != executionKey {
		return conversationDelegationSubjects{}, false, conversationError("forbidden", "execution_subject_mismatch")
	}
	return out, true, nil
}

func (s *ConversationStore) executionDelegation(ctx context.Context, db conversationDB, id string, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	subjects, found, err := s.delegationSubjects(ctx, db, id, a)
	if err != nil {
		return sdk.ConversationDelegation{}, err
	}
	if !found || conversationOwner(subjects.execution) != conversationOwner(a) {
		return sdk.ConversationDelegation{}, conversationError("not_found", "delegation_not_found")
	}
	d, err := s.ownedConversationDelegation(ctx, db, id, subjects.source)
	if err != nil {
		return d, err
	}
	if d.ExecutionSubject == nil || *d.ExecutionSubject != (sdk.ConversationExecutionSubject{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: a.UserID}) {
		return sdk.ConversationDelegation{}, conversationError("not_found", "delegation_not_found")
	}
	return d, nil
}

func (s *ConversationStore) saveDelegationSubjects(ctx context.Context, tx *sql.Tx, id string, source, execution sdk.ConversationAuthority, createdAt time.Time) error {
	if conversationAuthority(source) != nil || conversationAuthority(execution) != nil || !sameConversationWorkspace(source, execution) {
		return conversationError("forbidden", "execution_subject_mismatch")
	}
	if source == execution {
		return nil
	}
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationDelegationSubjectTable).Columns("owner_key", "delegation_id", "execution_owner_key", "source_authority_json", "execution_authority_json", "created_at").Values(conversationOwner(source), id, conversationOwner(execution), conversationJSON(source), conversationJSON(execution), createdAt.UnixMilli()).Build()
	return conversationExec(ctx, tx, q, args, err)
}

func delegationMessageScope(d sdk.ConversationDelegation, a sdk.ConversationAuthority) query.Predicate {
	owner := delegationRecordAuthority(d, a)
	users := query.Equal("owner_key", conversationOwner(owner))
	if d.ExecutionSubject != nil {
		executor := sdk.ConversationAuthority{RuntimeID: d.ExecutionSubject.RuntimeID, WorkspaceID: d.ExecutionSubject.WorkspaceID, UserID: d.ExecutionSubject.UserID}
		users = query.Or(users, query.Equal("owner_key", conversationOwner(executor)))
	}
	return query.And(users, query.Equal("delegation_id", d.ID))
}

func (s *ConversationStore) delegationMessageAuthority(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, conversationID string, fallback sdk.ConversationAuthority) (sdk.ConversationAuthority, error) {
	subjects, err := s.delegationAuthorities(ctx, db, d, delegationRecordAuthority(d, fallback))
	if err != nil {
		return sdk.ConversationAuthority{}, err
	}
	switch conversationID {
	case d.SourceConversationID:
		return subjects.source, nil
	case d.ConversationID:
		return subjects.execution, nil
	default:
		return sdk.ConversationAuthority{}, conversationError("forbidden", "agent_message_recipient_invalid")
	}
}

func (s *ConversationStore) executionDelegations(ctx context.Context, a sdk.ConversationAuthority) ([]sdk.ConversationDelegation, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDelegationSubjectTable).Columns("delegation_id").Where(query.Equal("execution_owner_key", conversationOwner(a))).OrderBy(query.Descending("created_at"), query.Descending("delegation_id")).Limit(101).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []sdk.ConversationDelegation{}
	for _, id := range ids {
		d, err := s.executionDelegation(ctx, s.store.Database(), id, a)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *ConversationStore) delegationExecutionAuthority(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, a sdk.ConversationAuthority) (sdk.ConversationAuthority, error) {
	subjects, err := s.delegationAuthorities(ctx, db, d, delegationRecordAuthority(d, a))
	return subjects.execution, err
}

func (s *ConversationStore) ConversationDelegationAuthorities(ctx context.Context, id string, a sdk.ConversationAuthority) (persistence.ConversationDelegationAuthorities, error) {
	if err := conversationAuthority(a); err != nil {
		return persistence.ConversationDelegationAuthorities{}, err
	}
	d, _, err := s.participantDelegation(ctx, s.store.Database(), id, a, "manage")
	if err != nil {
		return persistence.ConversationDelegationAuthorities{}, err
	}
	subjects, err := s.delegationAuthorities(ctx, s.store.Database(), d, delegationRecordAuthority(d, a))
	return persistence.ConversationDelegationAuthorities{Issuer: subjects.source, Executor: subjects.execution}, err
}

// Resolve immutable admission identities inside the caller's transaction as
// well as on reads. A notice's routing header is never execution authority.
func (s *ConversationStore) delegationAuthorities(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, a sdk.ConversationAuthority) (conversationDelegationSubjects, error) {
	subjects, found, err := s.delegationSubjects(ctx, db, d.ID, a)
	if err != nil {
		return conversationDelegationSubjects{}, err
	}
	if !found {
		// Same-identity/legacy assignments have no separate subject mapping.
		// Their original task authority is immutable; a new reading role must
		// not become the original executor or publisher by virtue of ownership.
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(delegationRecordAuthority(d, a)), d.TaskID)).Build()
		if err != nil {
			return conversationDelegationSubjects{}, err
		}
		row, err := scanConversationTask(db.QueryRowContext(ctx, q, args...))
		if err != nil {
			return conversationDelegationSubjects{}, err
		}
		if row.task.DelegationID != d.ID || row.task.ExecutionConversationID != d.ConversationID || conversationAuthority(row.authority) != nil || conversationOwner(row.authority) != conversationOwner(a) {
			return conversationDelegationSubjects{}, conversationError("forbidden", "execution_subject_mismatch")
		}
		subjects.source, subjects.execution = row.authority, row.authority
		if d.SourceRunID != "" {
			run, err := s.runRow(ctx, db, d.SourceConversationID, d.SourceRunID, subjects.source)
			if err != nil {
				return conversationDelegationSubjects{}, err
			}
			if conversationAuthority(run.Authority) != nil || conversationOwner(run.Authority) != conversationOwner(subjects.source) {
				return conversationDelegationSubjects{}, conversationError("forbidden", "execution_subject_mismatch")
			}
			subjects.source = run.Authority
		}
	}
	return subjects, nil
}

func (s *ConversationStore) ConversationDelegationTask(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTask, error) {
	d, _, err := s.participantDelegation(ctx, s.store.Database(), id, a, "manage")
	if err != nil {
		return sdk.ConversationTask{}, err
	}
	executor, err := s.delegationExecutionAuthority(ctx, s.store.Database(), d, a)
	if err != nil {
		return sdk.ConversationTask{}, err
	}
	return s.ConversationTask(ctx, d.TaskID, executor)
}

var _ persistence.ConversationDelegationExecutionRepository = (*ConversationStore)(nil)
