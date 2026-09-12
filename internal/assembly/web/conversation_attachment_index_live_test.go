package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	agentmodule "github.com/domainry/domainry-agent/module"
	connector "github.com/domainry/domainry-connector-sdk"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

func TestLivePrivateAttachmentIndexIdentityHTTP(t *testing.T) {
	if os.Getenv("AGENT_ATTACHMENT_INDEX_LIVE") != "1" {
		t.Skip("requires explicit private attachment live acceptance")
	}
	c := provider.KnowledgeConfigFromEnvironment()
	// This KB was created specifically for synthetic attachment acceptance.
	// Do not silently use the existing default/personal/shared KB configuration.
	if c.BaseURL != "https://api.verdent.ai" || c.TeamID != "1470194374940573696" || c.KBID != "kb-1a07a88ed780" {
		t.Fatal("dedicated attachment acceptance KB required")
	}
	metadata := "/data"
	c.ResponseMapping = &provider.KnowledgeResponseMapping{Search: &provider.KnowledgeCitationMapping{Items: "/data/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/snippet"}, Fetch: &provider.KnowledgeCitationMapping{Items: "/data/chunks", Many: true, MetadataObject: &metadata, DocumentID: "/doc_id", Title: "/title", Excerpt: "/content"}}
	runPrivateAttachmentIndexIdentityHTTP(t, &c, os.Getenv("AGENT_LIVE_EVIDENCE_DIR"))
}

func TestPrivateAttachmentIndexLiveHarnessProtocol(t *testing.T) {
	runPrivateAttachmentIndexIdentityHTTP(t, &agentmodule.KnowledgeConfig{}, t.TempDir())
}

// Instrumentation wraps the actual host transport. It never reads credentials
// out of requests, and permits document writes only for this fixture's bytes.
type attachmentLiveAudit struct {
	connector.Transport
	mu                                                       sync.Mutex
	config                                                   provider.KnowledgeConfig
	file                                                     string
	doc, permission, request, hash, conversation, attachment string
	preflight                                                map[string]bool
	uploads, deletes                                         int
	lostUpload, privateVerified, cleanupVerified             bool
	ownerIsolationVerified                                   bool
	deletionReceipts                                         []json.RawMessage
}

func newAttachmentLiveAudit(c provider.KnowledgeConfig, dir string) (*attachmentLiveAudit, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("persistent attachment evidence directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	transport, err := knowledgemodule.NewKnowledgeDocumentHTTP(c.BaseURL, c.KBID, c.Client)
	if err != nil {
		return nil, err
	}
	a := &attachmentLiveAudit{Transport: transport, config: c, file: filepath.Join(dir, "attachment-manifest.json"), preflight: map[string]bool{}}
	return a, a.save()
}
func (a *attachmentLiveAudit) saveLocked() error {
	raw, err := json.MarshalIndent(map[string]any{"origin": a.config.BaseURL, "team_id": a.config.TeamID, "kb_id": a.config.KBID, "doc_id": a.doc, "permission_id": a.permission, "request_id": a.request, "original_sha256": a.hash, "conversation_id": a.conversation, "attachment_id": a.attachment, "preflight_missing": a.preflight[a.doc], "uploads": a.uploads, "deletes": a.deletes, "upload_response_lost": a.lostUpload, "private_visibility_verified": a.privateVerified, "owner_isolation_verified": a.ownerIsolationVerified, "cleanup_verified": a.cleanupVerified, "delete_receipts": a.deletionReceipts}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.file, raw, 0600)
}
func (a *attachmentLiveAudit) save() error { a.mu.Lock(); defer a.mu.Unlock(); return a.saveLocked() }
func (a *attachmentLiveAudit) markOwnerIsolation() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ownerIsolationVerified = true
}
func (a *attachmentLiveAudit) expectOriginal(raw []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sum := sha256.Sum256(raw)
	a.hash = hex.EncodeToString(sum[:])
}

func attachmentAuditHeader(headers map[string][]string, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) == 1 {
			return values[0]
		}
	}
	return ""
}

func (a *attachmentLiveAudit) RoundTripHTTP(ctx context.Context, in connector.HTTPRequest) (connector.HTTPResponse, error) {
	u, err := url.Parse(in.URL)
	if err != nil {
		return connector.HTTPResponse{}, err
	}
	write := u.Path == "/v1/kb/kbs/"+a.config.KBID+"/documents"
	if write {
		a.mu.Lock()
		id := u.Query().Get("doc_id")
		if id == "" || a.doc != "" && id != a.doc {
			a.mu.Unlock()
			return connector.HTTPResponse{}, errors.New("fixture document target changed")
		}
		if in.Method == http.MethodPost {
			var ids []string
			sum := sha256.Sum256(in.Body)
			if !a.preflight[id] || a.hash == "" || hex.EncodeToString(sum[:]) != a.hash || json.Unmarshal([]byte(attachmentAuditHeader(in.Headers, "X-KB-Permission-Ids")), &ids) != nil || len(ids) != 1 || !strings.HasPrefix(ids[0], "scope:agent:attachment:") {
				a.mu.Unlock()
				return connector.HTTPResponse{}, errors.New("unverified fixture upload")
			}
			request := attachmentAuditHeader(in.Headers, "X-KB-Request-ID")
			if request == "" || a.uploads != 0 {
				a.mu.Unlock()
				return connector.HTTPResponse{}, errors.New("duplicate or unscoped fixture upload")
			}
			a.doc, a.permission, a.request = id, ids[0], request
			a.uploads++
		} else if in.Method == http.MethodDelete && a.doc == id && a.uploads == 1 {
			a.deletes++
		} else {
			a.mu.Unlock()
			return connector.HTTPResponse{}, errors.New("unowned fixture write")
		}
		err = a.saveLocked()
		a.mu.Unlock()
		if err != nil {
			return connector.HTTPResponse{}, err
		}
	}
	out, err := a.Transport.RoundTripHTTP(ctx, in)
	if err != nil {
		return out, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var result struct {
		Code *int `json:"err_code"`
	}
	valid := json.Unmarshal(out.Body, &result) == nil && result.Code != nil
	if u.Path == "/v1/kb/fetch" && out.StatusCode == 200 && valid && *result.Code == 1004 {
		var request struct {
			DocID string `json:"doc_id"`
		}
		if json.Unmarshal(in.Body, &request) == nil && request.DocID != "" {
			a.preflight[request.DocID] = true
		}
	}
	if write && in.Method == http.MethodDelete {
		a.deletionReceipts = append(a.deletionReceipts, append(json.RawMessage(nil), out.Body...))
	}
	if write && in.Method == http.MethodPost && out.StatusCode == 200 && valid && *result.Code == 0 && !a.lostUpload {
		a.lostUpload = true
		if err := a.saveLocked(); err != nil {
			return connector.HTTPResponse{}, err
		}
		return connector.HTTPResponse{}, errors.New("synthetic response loss after actual private upload")
	}
	if err := a.saveLocked(); err != nil {
		return connector.HTTPResponse{}, err
	}
	return out, nil
}

func (a *attachmentLiveAudit) readSource(ids []string) (*provider.Knowledge, agentsdk.ConversationAuthority, error) {
	c := a.config
	c.Transport = a
	c.WorkspaceID = "attachment-visibility-probe"
	c.DocumentManagement = false
	c.DocumentPermissionIDs = nil
	if ids != nil {
		c.PermissionIDs = func(context.Context, agentsdk.ConversationAuthority) ([]string, error) {
			return append([]string(nil), ids...), nil
		}
	} else {
		c.PermissionIDs = nil
	}
	k, err := provider.NewKnowledge(c)
	return k, agentsdk.ConversationAuthority{Known: true, RuntimeID: "attachment-probe", WorkspaceID: c.WorkspaceID, UserID: "synthetic-probe"}, err
}

func (a *attachmentLiveAudit) verifyPrivateVisibility(ctx context.Context, att agentsdk.ConversationAttachment, conversation string) error {
	a.mu.Lock()
	doc, permission := a.doc, a.permission
	a.conversation, a.attachment = conversation, att.ID
	a.mu.Unlock()
	for i, ids := range [][]string{nil, {permission + ":other"}, {permission}} {
		source, actor, err := a.readSource(ids)
		if err != nil {
			return err
		}
		state, err := source.InspectKnowledgeDocument(ctx, doc, actor)
		if err != nil || state.Exists != (i == 2) {
			return fmt.Errorf("private attachment scope %d did not isolate the document: %w", i, err)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.privateVerified = true
	return a.saveLocked()
}

func (a *attachmentLiveAudit) verifyCleanup(ctx context.Context, r persistence.ConversationAttachmentRecord) error {
	a.mu.Lock()
	doc, permission, conversation, uploads, deletes, lost := a.doc, a.permission, a.conversation, a.uploads, a.deletes, a.lostUpload
	a.mu.Unlock()
	if uploads != 1 || deletes != 1 || !lost || r.Source.DocID != doc || r.Source.PermissionID != permission {
		return errors.New("attachment lifecycle identity/count mismatch")
	}
	source, actor, err := a.readSource([]string{permission})
	if err != nil {
		return err
	}
	state, err := source.InspectKnowledgeDocument(ctx, doc, actor)
	if err != nil || state.Exists {
		return errors.New("remote attachment deletion not confirmed")
	}
	raw, err := source.Search(ctx, conversation, actor)
	if err != nil {
		return err
	}
	if strings.Contains(string(raw), doc) {
		return errors.New("deleted attachment still searchable")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cleanupVerified = true
	return a.saveLocked()
}

func (a *attachmentLiveAudit) cleanupFailedFixture() error {
	a.mu.Lock()
	id, known := a.doc, a.uploads == 1 && a.preflight[a.doc]
	a.mu.Unlock()
	if !known {
		return nil
	}
	c := a.config
	c.Transport = a
	c.WorkspaceID = "attachment-cleanup"
	c.DocumentManagement = true
	k, err := provider.NewKnowledge(c)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return k.RecoverKnowledgeDocumentDelete(ctx, id, agentsdk.ConversationAuthority{Known: true, RuntimeID: "attachment-cleanup", WorkspaceID: c.WorkspaceID, UserID: "synthetic-cleanup"})
}
