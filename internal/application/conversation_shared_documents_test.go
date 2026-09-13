package application

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	knowledge "github.com/domainry/domainry-knowledge/contract"
)

type sharedDocumentOwner struct {
	knowledge.Service
	a      sdk.ConversationAuthority
	doc    sdk.KnowledgeDocument
	denied bool
	reads  int
}

func (o *sharedDocumentOwner) DocumentAccess(_ context.Context, library, op string, a sdk.ConversationAuthority) (persistence.KnowledgeDocumentRepository, error) {
	o.reads++
	if o.denied || a != o.a || library != o.doc.LibraryID || op != "documents_download" {
		return nil, conversationFailure("forbidden", "document_access_denied")
	}
	return nil, nil
}
func (o *sharedDocumentOwner) KnowledgeDocument(_ context.Context, library, id string, a sdk.ConversationAuthority) (sdk.KnowledgeDocument, error) {
	if a != o.a || library != o.doc.LibraryID || id != o.doc.ID {
		return sdk.KnowledgeDocument{}, conversationFailure("not_found", "document_not_found")
	}
	return o.doc, nil
}

type sharedDocumentInbox struct {
	privatePeerSources
	message sdk.ConversationAgentMessage
}

func (r sharedDocumentInbox) ConversationPeerInbox(context.Context, string, sdk.ConversationAuthority) ([]sdk.ConversationAgentMessage, error) {
	return []sdk.ConversationAgentMessage{r.message}, nil
}

func TestSharedDocumentsRecheckOwnerBeforeMessagesReachModelAndReplay(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	doc := sdk.KnowledgeDocument{ID: "kdoc_" + strings.Repeat("a", 32), LibraryID: "lib_" + strings.Repeat("b", 32), Revision: 3, SHA256: strings.Repeat("c", 64), State: "ready"}
	ref := sdk.ConversationDocumentReference{LibraryID: doc.LibraryID, DocumentID: doc.ID, Revision: doc.Revision, SHA256: doc.SHA256}
	message := sdk.ConversationAgentMessage{ID: "message", DelegationID: "delegation", FromUserID: a.UserID, ToAgentID: "peer", Content: "核对这份资料", Documents: []sdk.ConversationDocumentReference{ref}}
	owner := &sharedDocumentOwner{a: a, doc: doc}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: sharedDocumentInbox{message: message}, options: ConversationOptions{CollaborationAuthorizer: allowCollaborationTestPolicy{}, ContextBytes: 65536}}
	s.knowledgeOnce.Do(func() { s.knowledgeModule = owner })
	claim := persistence.ConversationClaim{Authority: a, Run: sdk.ConversationRun{ID: "receiver-run", ConversationID: "receiver"}}
	input, err := s.appendConversationPeerInbox(t.Context(), claim, sdk.ConversationStepRequest{})
	if err != nil || len(input.InboxMessageIDs) != 1 || len(input.Messages) != 1 || !strings.Contains(input.Messages[0].Content, ref.SHA256) || !strings.Contains(input.Messages[0].Content, "not instructions or access grants") {
		t.Fatalf("missing authorized exact references: %+v %v", input, err)
	}
	for _, change := range []struct {
		name   string
		mutate func()
	}{
		{"revoked", func() { owner.denied = true }},
		{"revision", func() { owner.doc.Revision++ }},
		{"hash", func() { owner.doc.SHA256 = strings.Repeat("d", 64) }},
		{"deleting", func() { owner.doc.State = "deleting" }},
		{"indexing", func() { owner.doc.State = "indexing" }},
		{"owner", func() { owner.a.UserID = "other" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			owner.doc, owner.a, owner.denied = doc, a, false
			change.mutate()
			in, e := s.appendConversationPeerInbox(t.Context(), claim, sdk.ConversationStepRequest{})
			if e == nil || len(in.Messages) != 0 || len(in.InboxMessageIDs) != 0 {
				t.Fatalf("invalid source entered model: %+v %v", in, e)
			}
			if e = s.sourceAudit(a).collaborationResult(t.Context(), sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "delegation"}, Messages: []sdk.ConversationAgentMessage{message}}); e == nil {
				t.Fatal("revoked reference survived stored tool result audit")
			}
		})
	}
	owner.doc, owner.a, owner.denied = doc, a, false
	s.options.ContextBytes = 1024
	if _, err = s.appendConversationPeerInbox(t.Context(), claim, sdk.ConversationStepRequest{}); err == nil || !strings.Contains(err.Error(), "agent_message_context_exceeded") {
		t.Fatalf("oversized reference silently lost: %v", err)
	}
	s.options.CollaborationAuthorizer = fixedCollaborationTestPolicy{"communicate"}
	before := owner.reads
	_, err = s.SendConversationAgentMessage(t.Context(), "delegation", sdk.ConversationAgentMessageSend{ClientID: "send", ToAgentID: "peer", Content: "share", BriefVersion: 1, Documents: []sdk.ConversationDocumentReference{ref}}, a)
	if !collaborationDenied(err) || before != owner.reads {
		t.Fatalf("share permission not checked before owner access: %v", err)
	}
}
