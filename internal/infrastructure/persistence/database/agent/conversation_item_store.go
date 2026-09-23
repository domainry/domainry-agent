package agent

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

func conversationItemScope(authority agentsdk.ConversationAuthority, conversationID, kind string) query.Predicate {
	return query.And(conversationScope(authority, conversationID), query.Equal("item_kind", kind))
}

func conversationRunEventItemKey(runID string, sequence int64) string {
	return runID + ":" + strconv.FormatInt(sequence, 10)
}

func conversationTaskItemReference(taskID, clientID string) string {
	return taskID + ":" + clientID
}

func conversationTaskCompletionItemKey(taskID string, revision int64) string {
	return taskID + ":" + strconv.FormatInt(revision, 10)
}

func delegationHistorySubject(delegationID, discriminator string) string {
	if discriminator == "" {
		return delegationID
	}
	return conversationHash([]string{delegationID, discriminator})
}

func delegationHistoryReference(kind, delegationID, discriminator string, sequence int64) string {
	return conversationHash([]string{kind, delegationID, discriminator, strconv.FormatInt(sequence, 10)})
}

func delegationHistoryPredicate(owner, kind, delegationID, discriminator string) query.Predicate {
	return query.And(
		query.Equal("owner_key", owner),
		query.Equal("item_kind", kind),
		query.Equal("subject_id", delegationHistorySubject(delegationID, discriminator)),
	)
}

// insertDelegationHistory stores immutable collaboration facts in the common
// conversation journal. The current delegation aggregate remains authoritative
// for current state; these rows exist only for bounded history and provenance.
func (s *ConversationStore) insertDelegationHistory(ctx context.Context, tx *sql.Tx, kind string, d agentsdk.ConversationDelegation, discriminator string, sequence int64, source *agentsdk.ConversationRunReference, value any, a agentsdk.ConversationAuthority) error {
	record := delegationRecordAuthority(d, a)
	reference := delegationHistoryReference(kind, d.ID, discriminator, sequence)
	runID := d.SourceRunID
	if source != nil && source.RunID != "" {
		runID = source.RunID
	}
	statement, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationItemTable).
		Columns("owner_key", "conversation_id", "item_kind", "item_key", "reference_id", "subject_id", "run_id", "seq", "payload_json").
		Values(conversationOwner(record), d.SourceConversationID, kind, reference, reference, delegationHistorySubject(d.ID, discriminator), runID, sequence, conversationJSON(value)).
		OnConflictDoNothing("owner_key", "item_kind", "reference_id").Build()
	if err = conversationExec(ctx, tx, statement, args, err); err != nil {
		return err
	}
	statement, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(
		query.Equal("owner_key", conversationOwner(record)),
		query.Equal("item_kind", kind),
		query.Equal("reference_id", reference),
	)).Build()
	if err != nil {
		return err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, statement, args...).Scan(&raw); err != nil {
		return err
	}
	if !bytes.Equal(raw, conversationJSON(value)) {
		return conversationError("conflict", "delegation_history_conflict")
	}
	return nil
}
