// Package artifactstorage provides the standalone host's private content
// storage. It is not a static file server or a path-based model tool.
package artifactstorage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/artifact"
)

type Files struct{ root *os.Root }

func NewFiles(directory string) (*Files, error) {
	if directory == "" {
		return nil, fmt.Errorf("artifact storage directory is required")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create private artifact storage directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("artifact storage must be a private directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open private artifact storage directory: %w", err)
	}
	return &Files{root: root}, nil
}
func (f *Files) Close() error { return f.root.Close() }

func storageError(class, code string) error {
	return &agentsdk.Error{Class: class, Code: "agent.conversation." + code}
}
func ownerKey(a agentsdk.ConversationAuthority) (string, error) {
	if !a.Known || a.RuntimeID == "" || a.WorkspaceID == "" || a.UserID == "" {
		return "", storageError("forbidden", "artifact_storage_denied")
	}
	raw, _ := json.Marshal([]string{a.RuntimeID, a.WorkspaceID, a.UserID})
	return artifact.Hash(raw), nil
}
func (f *Files) owner(a agentsdk.ConversationAuthority, create bool) (*os.Root, error) {
	key, err := ownerKey(a)
	if err != nil {
		return nil, err
	}
	if create {
		if err = f.root.Mkdir(key, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, storageError("unavailable", "artifact_storage_unavailable")
		}
	}
	info, err := f.root.Lstat(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil, storageError("not_found", "artifact_content_not_found")
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, storageError("unavailable", "artifact_storage_unavailable")
	}
	root, err := f.root.OpenRoot(key)
	if err != nil {
		return nil, storageError("unavailable", "artifact_storage_unavailable")
	}
	return root, nil
}
func referenceHash(reference string) (string, error) {
	if !strings.HasPrefix(reference, "content_") {
		return "", storageError("bad_request", "artifact_storage_reference_invalid")
	}
	hash := strings.TrimPrefix(reference, "content_")
	raw, err := hex.DecodeString(hash)
	if err != nil || len(raw) != 32 || strings.ToLower(hash) != hash {
		return "", storageError("bad_request", "artifact_storage_reference_invalid")
	}
	return hash, nil
}
func read(root *os.Root, hash string) ([]byte, error) {
	file, err := root.Open(hash + ".json")
	if errors.Is(err, os.ErrNotExist) {
		return nil, storageError("not_found", "artifact_content_not_found")
	}
	if err != nil {
		return nil, storageError("unavailable", "artifact_storage_unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, storageError("unavailable", "artifact_storage_unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(file, artifact.MaxBytes+1))
	if err != nil {
		return nil, storageError("unavailable", "artifact_storage_unavailable")
	}
	if len(raw) > artifact.MaxBytes || artifact.Hash(raw) != hash {
		return nil, storageError("unavailable", "artifact_content_mismatch")
	}
	return raw, nil
}
func (f *Files) ReadArtifactContent(ctx context.Context, reference string, a agentsdk.ConversationAuthority) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hash, err := referenceHash(reference)
	if err != nil {
		return nil, err
	}
	root, err := f.owner(a, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	raw, err := read(root, hash)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}
func (f *Files) PutArtifactContent(ctx context.Context, hash string, raw []byte, a agentsdk.ConversationAuthority) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	reference := "content_" + hash
	if _, err := referenceHash(reference); err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw) > artifact.MaxBytes || artifact.Hash(raw) != hash {
		return "", storageError("bad_request", "artifact_content_mismatch")
	}
	root, err := f.owner(a, true)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if _, err = read(root, hash); err == nil {
		return reference, nil
	}
	var coded *agentsdk.Error
	if !errors.As(err, &coded) || coded.Class != "not_found" {
		return "", err
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return "", err
	}
	name := ".pending_" + hex.EncodeToString(nonce[:])
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", storageError("unavailable", "artifact_storage_unavailable")
	}
	defer file.Close()
	defer root.Remove(name)
	if n, e := file.Write(raw); e != nil || n != len(raw) {
		return "", storageError("unavailable", "artifact_storage_unavailable")
	}
	if err = file.Sync(); err != nil {
		return "", storageError("unavailable", "artifact_storage_unavailable")
	}
	if err = file.Close(); err != nil {
		return "", storageError("unavailable", "artifact_storage_unavailable")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	// Concurrent writers of this key have the same verified content. Rename
	// publishes the complete object atomically, never a partially written file.
	if err = root.Rename(name, hash+".json"); err != nil {
		return "", storageError("unavailable", "artifact_storage_unavailable")
	}
	dir, err := root.Open(".")
	if err != nil {
		return "", storageError("unavailable", "artifact_storage_unavailable")
	}
	err = dir.Sync()
	_ = dir.Close()
	if err != nil {
		return "", storageError("unavailable", "artifact_storage_unavailable")
	}
	return reference, nil
}

var _ agentsdk.ConversationArtifactStorage = (*Files)(nil)
