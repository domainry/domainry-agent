// Package documentstorage uses the private immutable file engine in a separate
// library namespace. Storage namespaces are not Identity principals.
package documentstorage

import (
	"context"
	"errors"
	"regexp"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/attachmentstorage"
)

type Files struct{ files *attachmentstorage.Files }

func NewFiles(path string) (*Files, error) {
	f, e := attachmentstorage.NewFiles(path)
	if e != nil {
		return nil, e
	}
	return &Files{files: f}, nil
}
func (f *Files) Close() error { return f.files.Close() }

var libraryPattern = regexp.MustCompile(`^lib_[a-f0-9]{32}$`)
var documentPattern = regexp.MustCompile(`^kdoc_[a-f0-9]{32}$`)

func namespace(s agentsdk.KnowledgeDocumentStorageScope, id string) (agentsdk.ConversationAuthority, string, error) {
	if !libraryPattern.MatchString(s.LibraryID) || !documentPattern.MatchString(id) {
		return agentsdk.ConversationAuthority{}, "", &agentsdk.Error{Class: "bad_request", Code: "agent.conversation.document_reference_invalid"}
	}
	return agentsdk.ConversationAuthority{Known: true, RuntimeID: s.RuntimeID, WorkspaceID: s.WorkspaceID, UserID: "knowledge-library:" + s.LibraryID}, "att_" + strings.TrimPrefix(id, "kdoc_"), nil
}
func documentError(e error) error {
	var coded *agentsdk.Error
	if errors.As(e, &coded) {
		copy := *coded
		copy.Code = strings.Replace(copy.Code, "attachment_", "document_", 1)
		return &copy
	}
	return e
}
func (f *Files) PutKnowledgeDocumentContent(ctx context.Context, s agentsdk.KnowledgeDocumentStorageScope, id, hash string, raw []byte) (string, error) {
	a, key, e := namespace(s, id)
	if e != nil {
		return "", e
	}
	ref, e := f.files.PutAttachmentContent(ctx, key, hash, raw, a)
	return ref, documentError(e)
}
func (f *Files) ReadKnowledgeDocumentContent(ctx context.Context, s agentsdk.KnowledgeDocumentStorageScope, id, ref string) ([]byte, error) {
	a, key, e := namespace(s, id)
	if e != nil {
		return nil, e
	}
	raw, e := f.files.ReadAttachmentContent(ctx, key, ref, a)
	return raw, documentError(e)
}
func (f *Files) DeleteKnowledgeDocumentContent(ctx context.Context, s agentsdk.KnowledgeDocumentStorageScope, id string) error {
	a, key, e := namespace(s, id)
	if e != nil {
		return e
	}
	return documentError(f.files.DeleteAttachmentContent(ctx, key, a))
}

var _ agentsdk.KnowledgeDocumentStorage = (*Files)(nil)
