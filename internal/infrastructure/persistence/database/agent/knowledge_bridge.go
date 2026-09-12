package agent

import (
	"context"
	sdk "github.com/domainry/domainry-agent-sdk"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

func (s *ConversationStore) knowledgeStore() *knowledgemodule.Store {
	return knowledgemodule.NewStore(s.store, knowledgeSources{s})
}

type knowledgeSources struct{ s *ConversationStore }

func (h knowledgeSources) Conversation(ctx context.Context, db knowledgemodule.DB, id string, a sdk.ConversationAuthority) (sdk.Conversation, error) {
	return h.s.get(ctx, db, id, a)
}
func (h knowledgeSources) Run(ctx context.Context, db knowledgemodule.DB, conversation, id string, a sdk.ConversationAuthority) (sdk.ConversationRun, error) {
	r, e := h.s.runRow(ctx, db, conversation, id, a)
	return r.Run, e
}
