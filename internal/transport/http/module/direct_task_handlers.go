package module

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

const directTaskMetadataMaxBytes = 64 << 10

type directTaskAttachmentMetadata struct {
	Filename, ContentType, SHA256, Detail string
	Bytes                                 int64
}

func (m *directTaskAttachmentMetadata) UnmarshalJSON(raw []byte) error {
	type wire struct {
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		SHA256      string `json:"sha256"`
		Detail      string `json:"detail,omitempty"`
		Bytes       int64  `json:"bytes"`
	}
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*m = directTaskAttachmentMetadata(value)
	return nil
}

type directTaskStartMetadata struct {
	TaskKey           string                         `json:"task_key"`
	TaskVersion       string                         `json:"task_version"`
	Input             map[string]any                 `json:"input"`
	Attachments       []directTaskAttachmentMetadata `json:"attachments,omitempty"`
	AttachmentSources []directTaskAttachmentSource   `json:"attachment_sources,omitempty"`
}

type directTaskAttachmentSource struct {
	Kind      string `json:"kind"`
	ObjectKey string `json:"object_key"`
	RecordID  string `json:"record_id"`
	FieldKey  string `json:"field_key"`
	Filename  string `json:"filename"`
	Detail    string `json:"detail,omitempty"`
}

func (s *adapter) startPrincipalTaskRun(w http.ResponseWriter, r *http.Request) {
	identity, ok := identitysdk.RequestIdentityFromContext(r.Context())
	if !ok || !identity.Principal.Known {
		writeCode(w, http.StatusForbidden, "backend.workspace_scope_required")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 255 {
		writeCode(w, http.StatusBadRequest, "backend.idempotency.key_required")
		return
	}
	maxRequestBytes := int64(agentsdk.TaskAttachmentMaxCount)*agentsdk.TaskAttachmentMaxBytes + directTaskMetadataMaxBytes + (1 << 20)
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeCode(w, http.StatusBadRequest, "agent.task.multipart_invalid")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	metadataValues := r.MultipartForm.Value["request"]
	files := r.MultipartForm.File["files"]
	if len(metadataValues) != 1 || len(metadataValues[0]) > directTaskMetadataMaxBytes || len(files) > agentsdk.TaskAttachmentMaxCount {
		writeCode(w, http.StatusBadRequest, "agent.task.multipart_invalid")
		return
	}
	var metadata directTaskStartMetadata
	decoder := json.NewDecoder(strings.NewReader(metadataValues[0]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		writeCode(w, http.StatusBadRequest, "agent.task.request_invalid")
		return
	}
	var trailing any
	attachmentCount := len(files) + len(metadata.AttachmentSources)
	if err := decoder.Decode(&trailing); err != io.EOF || len(metadata.Attachments) != len(files) || attachmentCount > agentsdk.TaskAttachmentMaxCount {
		writeCode(w, http.StatusBadRequest, "agent.task.request_invalid")
		return
	}
	attachments := make([]agentsdk.TaskAttachment, 0, len(files))
	for index, header := range files {
		declared := metadata.Attachments[index]
		file, err := header.Open()
		if err != nil {
			writeCode(w, http.StatusBadRequest, "agent.task.attachment_read_failed")
			return
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, agentsdk.TaskAttachmentMaxBytes+1))
		_ = file.Close()
		if readErr != nil || len(raw) == 0 || int64(len(raw)) > agentsdk.TaskAttachmentMaxBytes {
			writeCode(w, http.StatusBadRequest, "agent.task.attachment_read_failed")
			return
		}
		digest := sha256.Sum256(raw)
		detected := strings.ToLower(strings.TrimSpace(http.DetectContentType(raw[:min(len(raw), 512)])))
		if header.Filename != declared.Filename || int64(len(raw)) != declared.Bytes || hex.EncodeToString(digest[:]) != strings.ToLower(strings.TrimSpace(declared.SHA256)) || detected != strings.ToLower(strings.TrimSpace(declared.ContentType)) {
			writeCode(w, http.StatusBadRequest, "agent.task.attachment_identity_mismatch")
			return
		}
		attachments = append(attachments, agentsdk.TaskAttachment{
			Filename: declared.Filename, ContentType: detected, Bytes: int64(len(raw)), SHA256: hex.EncodeToString(digest[:]), Detail: declared.Detail, Data: raw,
		})
	}
	sources := make([]modulehost.TaskAttachmentSource, 0, len(metadata.AttachmentSources))
	for _, source := range metadata.AttachmentSources {
		sources = append(sources, modulehost.TaskAttachmentSource{
			Kind: source.Kind, ObjectKey: source.ObjectKey, RecordID: source.RecordID, FieldKey: source.FieldKey,
			Filename: source.Filename, Detail: source.Detail,
		})
	}
	principal := identity.Principal
	run, replayed, err := s.directTasks.Start(r.Context(), agentapplication.DirectTaskExecutionRequest{
		TaskKey: metadata.TaskKey, TaskVersion: metadata.TaskVersion, Input: metadata.Input, Attachments: attachments, AttachmentSources: sources, IdempotencyKey: idempotencyKey,
		Principal: modulehost.Principal{
			Known: true, WorkspaceID: strings.TrimSpace(principal.WorkspaceID), UserID: strings.TrimSpace(principal.UserID), RoleKey: strings.TrimSpace(principal.RoleKey),
			AuthorizationRevision: strings.TrimSpace(principal.AuthorizationRevision), RequestID: strings.TrimSpace(r.Header.Get("X-Request-ID")),
			CorrelationID: strings.TrimSpace(r.Header.Get("X-Correlation-ID")), CausationID: strings.TrimSpace(r.Header.Get("X-Causation-ID")),
		},
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task": agentpersistence.ProjectAgentTaskRun(run), "replayed": replayed})
}
