package agent

import (
	"context"
	"encoding/json"

	sdk "github.com/domainry/domainry-agent-sdk"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

func (s *ConversationStore) knowledgeStore() *knowledgemodule.Store {
	return knowledgemodule.NewStore(s.store, knowledgeSources{s})
}

// DeleteConversationReferencesForRequest is the shared-database adapter for
// Knowledge's public lifecycle repository port. Knowledge owns the transaction,
// cleanup state and receipt; Agent only supplies the composed backend.
func (s *ConversationStore) DeleteConversationReferencesForRequest(ctx context.Context, requestID, conversationID string, a sdk.ConversationAuthority) (json.RawMessage, error) {
	return s.knowledgeStore().DeleteConversationReferencesForRequest(ctx, requestID, conversationID, a)
}

type knowledgeSources struct{ s *ConversationStore }

func (h knowledgeSources) Conversation(ctx context.Context, db knowledgemodule.DB, id string, a sdk.ConversationAuthority) (sdk.Conversation, error) {
	return h.s.get(ctx, db, id, a)
}
func (h knowledgeSources) Run(ctx context.Context, db knowledgemodule.DB, conversation, id string, a sdk.ConversationAuthority) (sdk.ConversationRun, error) {
	r, e := h.s.runRow(ctx, db, conversation, id, a)
	return r.Run, e
}
