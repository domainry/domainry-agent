package agent

import (
	"context"
	"database/sql"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	"github.com/domainry/domainry-orm/query"
)

// Legacy API forwarding. Knowledge owns the data implementation.
func attachmentScope(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return knowledgemodule.CompatAttachmentScope(a, id)
}

func attachmentDeleted(state string) bool { return knowledgemodule.CompatAttachmentDeleted(state) }

var attachmentErrorCodePattern = knowledgemodule.CompatAttachmentErrorCodePattern

func (s *ConversationStore) attachment(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	return s.knowledgeStore().CompatAttachment(ctx, db, id, a)
}

func (s *ConversationStore) AttachmentRecord(ctx context.Context, id string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	return s.knowledgeStore().AttachmentRecord(ctx, id, a)
}

func validAttachmentReserve(in persistence.ConversationAttachmentReserve) bool {
	return knowledgemodule.CompatValidAttachmentReserve(in)
}

func (s *ConversationStore) ReserveAttachment(ctx context.Context, in persistence.ConversationAttachmentReserve, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	return s.knowledgeStore().ReserveAttachment(ctx, in, a)
}

func (s *ConversationStore) Attachments(ctx context.Context, conversationID, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentPage, error) {
	return s.knowledgeStore().Attachments(ctx, conversationID, after, limit, a)
}

func (s *ConversationStore) saveAttachment(ctx context.Context, tx *sql.Tx, record persistence.ConversationAttachmentRecord, expected int64, a agentsdk.ConversationAuthority) error {
	return s.knowledgeStore().CompatSaveAttachment(ctx, tx, record, expected, a)
}

func (s *ConversationStore) TransitionAttachment(ctx context.Context, id string, expected int64, in persistence.ConversationAttachmentTransition, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	return s.knowledgeStore().TransitionAttachment(ctx, id, expected, in, a)
}

// Called in the parent deletion transaction. Never discard cleanup references
// or permit a late upload/index worker to restore access after conversation deletion.
func (s *ConversationStore) deleteConversationAttachments(ctx context.Context, tx *sql.Tx, conversationID string, a agentsdk.ConversationAuthority) error {
	return s.knowledgeStore().CompatDeleteConversationAttachments(ctx, tx, conversationID, a)
}
