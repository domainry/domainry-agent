package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	knowledgeartifact "github.com/domainry/domainry-agent-sdk/artifact"
	"github.com/domainry/domainry-agent-sdk/persistence"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	"github.com/domainry/domainry-orm/query"
)

const attachmentArtifactKind = "attachment"

type attachmentArtifactMetadata struct {
	RequestSHA256 string                                    `json:"request_sha256"`
	Visibility    string                                    `json:"visibility"`
	State         string                                    `json:"state"`
	Revision      int64                                     `json:"revision"`
	IndexStatus   string                                    `json:"index_status,omitempty"`
	ErrorCode     string                                    `json:"error_code,omitempty"`
	Source        *persistence.ConversationAttachmentSource `json:"source,omitempty"`
	Index         *persistence.ConversationAttachmentIndex  `json:"index,omitempty"`
}

// attachmentScope remains the exact owner/job predicate used by the
// retained index-work table. Core attachment metadata no longer has an
// Agent-private SQL table.
func attachmentScope(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("attachment_id", id))
}

func attachmentDeleted(state string) bool { return state == "deleting" || state == "deleted" }

// Public metadata carries stable host error codes, never raw upstream errors.
// Callers must classify failures before persisting them.
var attachmentErrorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,127}$`)

func attachmentArtifactStatus(state string) (sharedartifact.Status, bool) {
	switch state {
	case "stored", "indexing", "ready", "failed", "needs_reconcile":
		return sharedartifact.StatusAvailable, true
	case "deleting":
		return sharedartifact.StatusExpired, true
	case "deleted":
		return sharedartifact.StatusDeleted, true
	default:
		return "", false
	}
}

func decodeAttachmentArtifact(value sharedartifact.Artifact, conversationID string) (persistence.ConversationAttachmentRecord, error) {
	var metadata attachmentArtifactMetadata
	if err := json.Unmarshal(value.Metadata, &metadata); err != nil || !artifactSHA(metadata.RequestSHA256) || metadata.Visibility != "conversation_private" || metadata.Revision < 1 {
		return persistence.ConversationAttachmentRecord{}, conversationError("unavailable", "attachment_metadata_invalid")
	}
	expectedStatus, valid := attachmentArtifactStatus(metadata.State)
	if !valid || value.Status != expectedStatus || value.ScanStatus != sharedartifact.ScanNotRequired || !personalMemoryKey(conversationID) || value.SizeBytes < 1 || value.SizeBytes > agentsdk.ConversationAttachmentMaxBytes || !artifactSHA(value.ContentSHA256) {
		return persistence.ConversationAttachmentRecord{}, conversationError("unavailable", "attachment_metadata_invalid")
	}
	if (metadata.Source == nil) != (metadata.Index == nil) {
		return persistence.ConversationAttachmentRecord{}, conversationError("unavailable", "attachment_metadata_invalid")
	}
	if metadata.ErrorCode != "" && !attachmentErrorCodePattern.MatchString(metadata.ErrorCode) || metadata.State == "failed" && metadata.ErrorCode == "" || metadata.State != "failed" && metadata.State != "needs_reconcile" && metadata.State != "deleting" && metadata.ErrorCode != "" {
		return persistence.ConversationAttachmentRecord{}, conversationError("unavailable", "attachment_metadata_invalid")
	}
	return persistence.ConversationAttachmentRecord{
		Attachment: agentsdk.ConversationAttachment{
			ID: value.ID, ConversationID: conversationID, Filename: value.Filename,
			ContentType: value.MediaType, Bytes: value.SizeBytes, SHA256: value.ContentSHA256,
			Visibility: metadata.Visibility, State: metadata.State, Revision: metadata.Revision,
			IndexStatus: metadata.IndexStatus, ErrorCode: metadata.ErrorCode, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		},
		RequestSHA256: metadata.RequestSHA256, Source: metadata.Source, Index: metadata.Index,
	}, nil
}

func attachmentMetadata(record persistence.ConversationAttachmentRecord) ([]byte, error) {
	return json.Marshal(attachmentArtifactMetadata{
		RequestSHA256: record.RequestSHA256, Visibility: record.Attachment.Visibility,
		State: record.Attachment.State, Revision: record.Attachment.Revision,
		IndexStatus: record.Attachment.IndexStatus, ErrorCode: record.Attachment.ErrorCode, Source: record.Source, Index: record.Index,
	})
}

func attachmentArtifactOwnedBy(value sharedartifact.Artifact, a agentsdk.ConversationAuthority) bool {
	return value.WorkspaceID == a.WorkspaceID && value.Owner == sharedartifact.OwnerAgent && value.Kind == attachmentArtifactKind && value.CreatedBy == a.UserID && value.AuthorizationScopeSHA256 == conversationOwner(a)
}

func (s *ConversationStore) attachmentConversationBinding(ctx context.Context, db conversationDB, value sharedartifact.Artifact, a agentsdk.ConversationAuthority) (string, error) {
	bindings, err := s.artifactStore.Bindings(sharedartifact.WithExecutor(ctx, db), value.WorkspaceID, value.ID)
	if err != nil {
		return "", err
	}
	subject, conversation := false, ""
	for _, binding := range bindings {
		if binding.Owner != sharedartifact.OwnerAgent || binding.ArtifactID != value.ID {
			continue
		}
		switch {
		case binding.Kind == sharedartifact.BindingSubject && binding.ResourceType == "agent_user" && binding.ResourceID == a.UserID:
			subject = true
		case binding.Kind == sharedartifact.BindingConversation && binding.ResourceType == "agent_conversation":
			if conversation != "" && conversation != binding.ResourceID {
				return "", conversationError("unavailable", "attachment_metadata_invalid")
			}
			conversation = binding.ResourceID
		}
	}
	if !subject || !personalMemoryKey(conversation) {
		return "", conversationError("not_found", "attachment_not_found")
	}
	return conversation, nil
}

func (s *ConversationStore) attachmentArtifact(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (sharedartifact.Artifact, persistence.ConversationAttachmentRecord, error) {
	if !personalMemoryKey(id) {
		return sharedartifact.Artifact{}, persistence.ConversationAttachmentRecord{}, conversationError("bad_request", "attachment_invalid")
	}
	if err := s.sharedArtifactsReady(); err != nil {
		return sharedartifact.Artifact{}, persistence.ConversationAttachmentRecord{}, err
	}
	artifactContext := sharedartifact.WithExecutor(ctx, db)
	value, found, err := s.artifactStore.ByID(artifactContext, a.WorkspaceID, id)
	if err != nil {
		return value, persistence.ConversationAttachmentRecord{}, err
	}
	if !found || !attachmentArtifactOwnedBy(value, a) {
		return value, persistence.ConversationAttachmentRecord{}, conversationError("not_found", "attachment_not_found")
	}
	conversationID, err := s.attachmentConversationBinding(ctx, db, value, a)
	if err != nil {
		return value, persistence.ConversationAttachmentRecord{}, err
	}
	record, err := decodeAttachmentArtifact(value, conversationID)
	return value, record, err
}

func (s *ConversationStore) attachmentRecord(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	_, record, err := s.attachmentArtifact(ctx, db, id, a)
	return record, err
}

func (s *ConversationStore) AttachmentRecord(ctx context.Context, id string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	if err := conversationAuthority(a); err != nil {
		return persistence.ConversationAttachmentRecord{}, err
	}
	return s.attachmentRecord(ctx, s.store.Database(), id, a)
}

func attachmentNotFound(err error) bool {
	var coded *agentsdk.Error
	return errors.As(err, &coded) && coded.Code == "agent.conversation.attachment_not_found"
}

func validAttachmentReserve(in persistence.ConversationAttachmentReserve) bool {
	mediaType, params, err := mime.ParseMediaType(in.ContentType)
	return personalMemoryKey(in.ClientID) && personalMemoryKey(in.ConversationID) && artifactSHA(in.SHA256) && in.Bytes > 0 && in.Bytes <= agentsdk.ConversationAttachmentMaxBytes &&
		executionText(in.Filename, 255, true) && in.Filename != "." && in.Filename != ".." && strings.TrimSpace(in.Filename) == in.Filename && !strings.ContainsAny(in.Filename, "/\\") && strings.IndexFunc(in.Filename, unicode.IsControl) < 0 &&
		err == nil && len(params) == 0 && len(in.ContentType) <= 128 && strings.Contains(mediaType, "/") && in.ContentType == mediaType
}

func (s *ConversationStore) attachmentArtifactsForConversation(ctx context.Context, db conversationDB, conversationID string, a agentsdk.ConversationAuthority) ([]sharedartifact.Artifact, error) {
	items, err := s.artifactStore.List(sharedartifact.WithExecutor(ctx, db), a.WorkspaceID, sharedartifact.Query{
		Owner: sharedartifact.OwnerAgent, Kind: attachmentArtifactKind,
		Binding: &sharedartifact.BindingQuery{Owner: sharedartifact.OwnerAgent, Kind: sharedartifact.BindingConversation, ResourceType: "agent_conversation", ResourceID: conversationID},
		Limit:   1000,
	})
	if err != nil {
		return nil, err
	}
	out := make([]sharedartifact.Artifact, 0, len(items))
	for _, item := range items {
		if attachmentArtifactOwnedBy(item, a) {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *ConversationStore) enforceAttachmentQuota(ctx context.Context, tx *sql.Tx, conversationID string, incoming int64, a agentsdk.ConversationAuthority) error {
	artifactContext := sharedartifact.WithExecutor(ctx, tx)
	items, err := s.artifactStore.List(artifactContext, a.WorkspaceID, sharedartifact.Query{Owner: sharedartifact.OwnerAgent, Kind: attachmentArtifactKind, CreatedBy: []string{a.UserID}, Limit: 1000})
	if err != nil {
		return err
	}
	count := 0
	var total int64
	for _, item := range items {
		if !attachmentArtifactOwnedBy(item, a) {
			continue
		}
		count++
		if item.Status != sharedartifact.StatusDeleted {
			total += item.SizeBytes
		}
	}
	conversationItems, err := s.attachmentArtifactsForConversation(ctx, tx, conversationID, a)
	if err != nil {
		return err
	}
	conversationCount := 0
	for _, item := range conversationItems {
		if item.Status != sharedartifact.StatusDeleted {
			conversationCount++
		}
	}
	if count >= 1000 || conversationCount >= 50 || total+incoming > 256<<20 {
		return conversationError("conflict", "attachment_limit")
	}
	return nil
}

func (s *ConversationStore) ReserveAttachment(ctx context.Context, in persistence.ConversationAttachmentReserve, a agentsdk.ConversationAuthority) (out persistence.ConversationAttachmentRecord, err error) {
	if err = conversationAuthority(a); err != nil {
		return out, err
	}
	if !validAttachmentReserve(in) || int64(len(in.Content)) != in.Bytes || knowledgeartifact.Hash(in.Content) != in.SHA256 {
		return out, conversationError("bad_request", "attachment_invalid")
	}
	if err = s.sharedArtifactsReady(); err != nil {
		return out, err
	}
	id := "att_" + conversationHash([]string{conversationOwner(a), in.ConversationID, in.ClientID})[:32]
	digest := conversationHash(in)
	if current, readErr := s.attachmentRecord(ctx, s.store.Database(), id, a); readErr == nil {
		if current.RequestSHA256 != digest || current.Attachment.ConversationID != in.ConversationID {
			return out, conversationError("conflict", "idempotency_conflict")
		}
		return current, nil
	} else if !attachmentNotFound(readErr) {
		return out, readErr
	}
	info, err := s.artifactWriter.PutImmutable(ctx, a.WorkspaceID, "agent-attachment:"+id, in.Content)
	if err != nil {
		return out, err
	}
	defer func() {
		if err == nil {
			return
		}
		current, found, readErr := s.artifactStore.ByID(context.WithoutCancel(ctx), a.WorkspaceID, id)
		if readErr == nil && (!found || current.StorageReference != info.Reference) {
			_ = s.artifactContent.Delete(context.WithoutCancel(ctx), a.WorkspaceID, info.Reference)
		}
	}()
	if strings.TrimSpace(info.Reference) == "" || !strings.EqualFold(info.SHA256, in.SHA256) || info.Size != in.Bytes {
		return out, conversationError("unavailable", "attachment_content_mismatch")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if _, txErr := s.get(ctx, tx, in.ConversationID, a); txErr != nil {
			return txErr
		}
		if current, txErr := s.attachmentRecord(ctx, tx, id, a); txErr == nil {
			if current.RequestSHA256 != digest || current.Attachment.ConversationID != in.ConversationID {
				return conversationError("conflict", "idempotency_conflict")
			}
			out = current
			return nil
		} else if !attachmentNotFound(txErr) {
			return txErr
		}
		if quotaErr := s.enforceAttachmentQuota(ctx, tx, in.ConversationID, in.Bytes, a); quotaErr != nil {
			return quotaErr
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		metadata, marshalErr := json.Marshal(attachmentArtifactMetadata{RequestSHA256: digest, Visibility: "conversation_private", State: "stored", Revision: 1})
		if marshalErr != nil {
			return marshalErr
		}
		artifactContext := sharedartifact.WithExecutor(ctx, tx)
		value := sharedartifact.Artifact{
			ID: id, WorkspaceID: a.WorkspaceID, Owner: sharedartifact.OwnerAgent, Kind: attachmentArtifactKind,
			IdempotencyKey: conversationHash([]string{"agent-attachment", conversationOwner(a), in.ConversationID, in.ClientID}), CreatedBy: a.UserID,
			Filename: in.Filename, MediaType: in.ContentType, ContentSHA256: in.SHA256, SizeBytes: in.Bytes, StorageReference: info.Reference,
			Status: sharedartifact.StatusAvailable, ScanStatus: sharedartifact.ScanNotRequired,
			AuthorizationScopeSHA256: conversationOwner(a), Metadata: metadata, CreatedAt: now, UpdatedAt: now,
		}
		registered, _, registerErr := s.artifactStore.Register(artifactContext, value)
		if registerErr != nil {
			registered, _, registerErr = s.artifactStore.ByID(artifactContext, a.WorkspaceID, id)
		}
		if registerErr != nil || !attachmentArtifactOwnedBy(registered, a) || registered.Filename != in.Filename || registered.MediaType != in.ContentType || registered.ContentSHA256 != in.SHA256 || registered.SizeBytes != in.Bytes || registered.StorageReference != info.Reference {
			if registerErr != nil {
				return registerErr
			}
			return conversationError("conflict", "idempotency_conflict")
		}
		for _, binding := range []sharedartifact.Binding{
			{ID: id + ":subject", WorkspaceID: a.WorkspaceID, ArtifactID: id, Owner: sharedartifact.OwnerAgent, Kind: sharedartifact.BindingSubject, ResourceType: "agent_user", ResourceID: a.UserID, CreatedAt: now},
			{ID: id + ":conversation", WorkspaceID: a.WorkspaceID, ArtifactID: id, Owner: sharedartifact.OwnerAgent, Kind: sharedartifact.BindingConversation, ResourceType: "agent_conversation", ResourceID: in.ConversationID, CreatedAt: now},
		} {
			if _, _, bindErr := s.artifactStore.Bind(artifactContext, binding); bindErr != nil {
				return bindErr
			}
		}
		var loadErr error
		_, out, loadErr = s.attachmentArtifact(ctx, tx, id, a)
		if loadErr == nil && out.RequestSHA256 != digest {
			loadErr = conversationError("conflict", "idempotency_conflict")
		}
		return loadErr
	})
	return out, err
}

func (s *ConversationStore) Attachments(ctx context.Context, conversationID, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentPage, error) {
	out := agentsdk.ConversationAttachmentPage{Items: []agentsdk.ConversationAttachment{}, Complete: true}
	if _, err := s.Get(ctx, conversationID, a); err != nil {
		return out, err
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 50 || after != "" && !personalMemoryKey(after) {
		return out, conversationError("bad_request", "attachment_query_invalid")
	}
	items, err := s.attachmentArtifactsForConversation(ctx, s.store.Database(), conversationID, a)
	if err != nil {
		return out, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	for _, item := range items {
		if item.ID <= after || item.Status == sharedartifact.StatusDeleted || item.Status == sharedartifact.StatusExpired {
			continue
		}
		record, decodeErr := decodeAttachmentArtifact(item, conversationID)
		if decodeErr != nil {
			return out, decodeErr
		}
		if len(out.Items) == limit {
			out.Complete = false
			out.NextAfter = out.Items[len(out.Items)-1].ID
			break
		}
		out.Items = append(out.Items, record.Attachment)
	}
	return out, nil
}

func (s *ConversationStore) saveAttachmentRecord(ctx context.Context, tx *sql.Tx, record persistence.ConversationAttachmentRecord, expected int64, a agentsdk.ConversationAuthority) error {
	value, current, err := s.attachmentArtifact(ctx, tx, record.Attachment.ID, a)
	if err != nil {
		return err
	}
	if current.Attachment.Revision != expected || record.Attachment.Revision != expected+1 || current.Attachment.ConversationID != record.Attachment.ConversationID || current.RequestSHA256 != record.RequestSHA256 || current.Attachment.Filename != record.Attachment.Filename || current.Attachment.ContentType != record.Attachment.ContentType || current.Attachment.Bytes != record.Attachment.Bytes || current.Attachment.SHA256 != record.Attachment.SHA256 || current.Attachment.CreatedAt != record.Attachment.CreatedAt {
		return conversationError("conflict", "revision_conflict")
	}
	status, valid := attachmentArtifactStatus(record.Attachment.State)
	if !valid {
		return conversationError("conflict", "attachment_transition_invalid")
	}
	metadata, err := attachmentMetadata(record)
	if err != nil {
		return err
	}
	updatedAt := record.Attachment.UpdatedAt.UTC()
	if !updatedAt.After(value.UpdatedAt) {
		updatedAt = value.UpdatedAt.Add(time.Nanosecond)
	}
	changed, err := s.artifactStore.Update(sharedartifact.WithExecutor(ctx, tx), sharedartifact.Mutation{
		WorkspaceID: value.WorkspaceID, ID: value.ID, Owner: value.Owner, Kind: value.Kind,
		ExpectedStatus: value.Status, ExpectedScanStatus: value.ScanStatus, ExpectedUpdatedAt: value.UpdatedAt,
		Status: status, ScanStatus: sharedartifact.ScanNotRequired, ExpiresAt: value.ExpiresAt,
		Metadata: metadata, UpdatedAt: updatedAt,
	})
	if err != nil {
		return err
	}
	if !changed {
		return conversationError("conflict", "revision_conflict")
	}
	return nil
}

func (s *ConversationStore) TransitionAttachment(ctx context.Context, id string, expected int64, in persistence.ConversationAttachmentTransition, a agentsdk.ConversationAuthority) (out persistence.ConversationAttachmentRecord, err error) {
	if err = conversationAuthority(a); err != nil {
		return out, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		previous, txErr := s.attachmentRecord(ctx, tx, id, a)
		if txErr != nil {
			return txErr
		}
		if expected < 1 || previous.Attachment.Revision != expected {
			return conversationError("conflict", "revision_conflict")
		}
		from := previous.Attachment.State
		if previous.Index != nil && in.State != "deleting" {
			return conversationError("conflict", "attachment_index_managed")
		}
		allowed := in.State == "deleting" && !attachmentDeleted(from) || from == "deleting" && in.State == "deleted"
		if !allowed || in.ErrorCode != "" && !attachmentErrorCodePattern.MatchString(in.ErrorCode) || (in.State == "failed") != (in.ErrorCode != "") {
			return conversationError("conflict", "attachment_transition_invalid")
		}
		if !attachmentDeleted(in.State) {
			if _, txErr = s.get(ctx, tx, previous.Attachment.ConversationID, a); txErr != nil {
				return txErr
			}
		}
		previous.Attachment.State, previous.Attachment.ErrorCode = in.State, in.ErrorCode
		previous.Attachment.Revision++
		previous.Attachment.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		out = previous
		if txErr = s.saveAttachmentRecord(ctx, tx, out, expected, a); txErr != nil {
			return txErr
		}
		if in.State == "deleting" {
			return s.queueAttachmentIndexWork(ctx, tx, out, a)
		}
		if in.State == "deleted" {
			statement, args, buildErr := query.NewDeleteBuilder(s.store.Renderer(), attachmentIndexJobTable).Where(attachmentScope(a, id)).Build()
			return conversationExec(ctx, tx, statement, args, buildErr)
		}
		return nil
	})
	return out, err
}

func (s *ConversationStore) AttachmentContent(ctx context.Context, id string, a agentsdk.ConversationAuthority) ([]byte, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	value, record, err := s.attachmentArtifact(ctx, s.store.Database(), id, a)
	if err != nil {
		return nil, err
	}
	if record.Attachment.State == "deleting" || record.Attachment.State == "deleted" || value.Status != sharedartifact.StatusAvailable {
		return nil, conversationError("not_found", "attachment_content_not_found")
	}
	reader, err := s.artifactContent.Open(ctx, value.WorkspaceID, value.StorageReference)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, value.SizeBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != value.SizeBytes || knowledgeartifact.Hash(raw) != value.ContentSHA256 {
		return nil, conversationError("unavailable", "attachment_content_mismatch")
	}
	current, found, err := s.artifactStore.ByID(ctx, value.WorkspaceID, value.ID)
	if err != nil {
		return nil, err
	}
	if !found || current.StorageReference != value.StorageReference || current.Status != sharedartifact.StatusAvailable || !attachmentArtifactOwnedBy(current, a) {
		return nil, conversationError("not_found", "attachment_not_found")
	}
	return raw, nil
}

func (s *ConversationStore) DeleteAttachmentContent(ctx context.Context, id string, a agentsdk.ConversationAuthority) error {
	if err := conversationAuthority(a); err != nil {
		return err
	}
	value, record, err := s.attachmentArtifact(ctx, s.store.Database(), id, a)
	if err != nil {
		return err
	}
	if record.Attachment.State != "deleting" && record.Attachment.State != "deleted" {
		return conversationError("conflict", "attachment_cleanup_invalid")
	}
	err = s.artifactContent.Delete(ctx, value.WorkspaceID, value.StorageReference)
	if errors.Is(err, sharedartifact.ErrContentNotFound) {
		return nil
	}
	return err
}

// Called in the parent deletion transaction. The Artifact metadata transition
// and durable cleanup/index job commit with the conversation deletion receipt.
func (s *ConversationStore) deleteConversationAttachments(ctx context.Context, tx *sql.Tx, conversationID string, a agentsdk.ConversationAuthority) (int64, error) {
	if s == nil || s.artifactStore == nil {
		return 0, nil
	}
	items, err := s.attachmentArtifactsForConversation(ctx, tx, conversationID, a)
	if err != nil {
		return 0, err
	}
	var queued int64
	for _, item := range items {
		record, decodeErr := decodeAttachmentArtifact(item, conversationID)
		if decodeErr != nil {
			return queued, decodeErr
		}
		if attachmentDeleted(record.Attachment.State) {
			continue
		}
		expected := record.Attachment.Revision
		record.Attachment.State, record.Attachment.ErrorCode = "deleting", ""
		record.Attachment.Revision++
		record.Attachment.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		if err = s.saveAttachmentRecord(ctx, tx, record, expected, a); err != nil {
			return queued, err
		}
		if err = s.queueAttachmentIndexWork(ctx, tx, record, a); err != nil {
			return queued, err
		}
		queued++
	}
	return queued, nil
}

var _ persistence.ConversationAttachmentRepository = (*ConversationStore)(nil)
