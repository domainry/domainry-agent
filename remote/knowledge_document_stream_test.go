package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentserver "github.com/domainry/domainry-agent/server"
)

type testReadSeekCloser struct{ *bytes.Reader }

func (testReadSeekCloser) Close() error { return nil }

type streamDocumentConversation struct {
	agentsdk.ConversationService
	document agentsdk.KnowledgeDocument
	content  []byte
	uploaded []byte
}

func (s *streamDocumentConversation) UploadKnowledgeDocument(_ context.Context, library string, in agentsdk.KnowledgeDocumentUpload, authority agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	if library != s.document.LibraryID || authority.RuntimeID == "" || in.Bytes != int64(len(s.content)) || in.SHA256 != s.document.SHA256 {
		return agentsdk.KnowledgeDocument{}, &agentsdk.Error{Class: "bad_request", Code: "agent.conversation.document_invalid"}
	}
	raw, err := io.ReadAll(in.Content)
	if err != nil {
		return agentsdk.KnowledgeDocument{}, err
	}
	s.uploaded = raw
	return s.document, nil
}
func (s *streamDocumentConversation) KnowledgeDocuments(context.Context, string, string, int, agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentPage, error) {
	return agentsdk.KnowledgeDocumentPage{Items: []agentsdk.KnowledgeDocument{s.document}, Complete: true}, nil
}
func (s *streamDocumentConversation) KnowledgeDocument(_ context.Context, library, id string, _ agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	if library != s.document.LibraryID || id != s.document.ID {
		return agentsdk.KnowledgeDocument{}, &agentsdk.Error{Class: "not_found", Code: "agent.conversation.document_not_found"}
	}
	return s.document, nil
}
func (s *streamDocumentConversation) DownloadKnowledgeDocument(_ context.Context, library, id string, _ agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentDownload, error) {
	if library != s.document.LibraryID || id != s.document.ID {
		return agentsdk.KnowledgeDocumentDownload{}, &agentsdk.Error{Class: "not_found", Code: "agent.conversation.document_not_found"}
	}
	return agentsdk.KnowledgeDocumentDownload{Document: s.document, Content: testReadSeekCloser{bytes.NewReader(s.content)}}, nil
}
func (s *streamDocumentConversation) DeleteKnowledgeDocument(context.Context, string, string, int64, agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	return s.document, nil
}

func TestRemoteAgentDoesNotRepublishKnowledgeDocumentStreams(t *testing.T) {
	content := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	digest := sha256.Sum256(content)
	service := &streamDocumentConversation{content: content, document: agentsdk.KnowledgeDocument{
		ID: "kdoc_" + strings.Repeat("a", 32), LibraryID: "lib_" + strings.Repeat("b", 32), Filename: "remote.txt", Bytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:]), ContentVersion: 1, IndexGeneration: 1, State: "ready", SecurityStatus: "clean", Revision: 1,
	}}
	server, err := agentserver.New(agentserver.Config{APIKey: "secret", Conversations: service, ConversationRuntimeID: "runtime", ConversationWorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client := &conversationClient{client: newClient(Options{BaseURL: httpServer.URL, APIKey: "secret", Client: httpServer.Client()}), runtimeID: "runtime"}
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "alice"}

	uploaded, err := client.UploadKnowledgeDocument(t.Context(), service.document.LibraryID, agentsdk.KnowledgeDocumentUpload{ClientID: "upload", Filename: service.document.Filename, Bytes: int64(len(content)), SHA256: service.document.SHA256, Content: bytes.NewReader(content)}, authority)
	if err == nil || uploaded.ID != "" || len(service.uploaded) != 0 {
		t.Fatalf("retired Agent upload route remained reachable: document=%+v bytes=%q err=%v", uploaded, service.uploaded, err)
	}
	download, err := client.DownloadKnowledgeDocument(t.Context(), service.document.LibraryID, service.document.ID, authority)
	if err == nil || download.Content != nil {
		t.Fatalf("retired Agent download route remained reachable: download=%+v err=%v", download.Document, err)
	}
}
