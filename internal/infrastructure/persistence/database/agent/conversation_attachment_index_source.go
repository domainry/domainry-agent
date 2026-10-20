package agent

import (
	"context"
	"database/sql"
	"errors"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

func attachmentKnowledgeScope(a agentsdk.ConversationAuthority) string {
	return conversationHash([]string{a.RuntimeID, a.WorkspaceID})
}

func (s *ConversationStore) attachmentKnowledgeSourceOwner(ctx context.Context, db conversationDB, source string) (string, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), attachmentKnowledgeSourceTable).Columns("scope_key").Where(query.Equal("source_key", source)).Build()
	if err != nil {
		return "", err
	}
	var owner string
	err = db.QueryRowContext(ctx, q, args...).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return owner, err
}

func (s *ConversationStore) ActivateAttachmentKnowledgeSource(ctx context.Context, runtime, workspace, source string) error {
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: runtime, WorkspaceID: workspace, UserID: "_host"}
	if conversationAuthority(a) != nil || !artifactSHA(source) {
		return conversationError("bad_request", "attachment_source_invalid")
	}
	scope := attachmentKnowledgeScope(a)
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), attachmentKnowledgeSourceTable).Columns("scope_key", "source_key").Where(query.Or(query.Equal("source_key", source), query.Equal("scope_key", scope))).Build()
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		found := false
		for rows.Next() {
			var existingScope, existingSource string
			if err = rows.Scan(&existingScope, &existingSource); err != nil {
				rows.Close()
				return err
			}
			if existingScope != scope || existingSource != source {
				rows.Close()
				return conversationError("conflict", "attachment_source_already_bound")
			}
			found = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil || found {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), attachmentKnowledgeSourceTable).Columns("scope_key", "source_key").Values(scope, source).Build()
		return conversationExec(ctx, tx, q, args, err)
	})
}

func (s *ConversationStore) KnowledgeSourceManaged(ctx context.Context, source string) (bool, error) {
	if !artifactSHA(source) {
		return false, conversationError("bad_request", "attachment_source_invalid")
	}
	owner, err := s.attachmentKnowledgeSourceOwner(ctx, s.store.Database(), source)
	return owner != "", err
}
