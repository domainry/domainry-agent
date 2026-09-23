package artifactkernel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
)

const maxArtifactContentBytes = int64(100 << 20)

var artifactReferencePattern = regexp.MustCompile(`^artifact_[a-f0-9]{32}_[a-f0-9]{64}$`)

// ContentFiles is the standalone deployment's private immutable blob adapter.
// SQL stores only the returned opaque reference and integrity evidence.
type ContentFiles struct{ root *os.Root }

func NewContentFiles(directory string) (*ContentFiles, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, fmt.Errorf("shared artifact content directory is required")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("shared artifact content directory must be private")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &ContentFiles{root: root}, nil
}

func (f *ContentFiles) Close() error {
	if f == nil || f.root == nil {
		return nil
	}
	return f.root.Close()
}

func (f *ContentFiles) PutImmutable(ctx context.Context, workspaceID, identity string, content []byte) (sharedartifact.ContentInfo, error) {
	if err := ctx.Err(); err != nil {
		return sharedartifact.ContentInfo{}, err
	}
	if strings.TrimSpace(identity) == "" || int64(len(content)) > maxArtifactContentBytes {
		return sharedartifact.ContentInfo{}, fmt.Errorf("shared artifact content identity or size is invalid")
	}
	root, err := f.workspace(workspaceID, true)
	if err != nil {
		return sharedartifact.ContentInfo{}, err
	}
	defer root.Close()
	contentDigest := sha256.Sum256(content)
	identityDigest := sha256.Sum256([]byte(strings.TrimSpace(identity)))
	reference := "artifact_" + hex.EncodeToString(identityDigest[:16]) + "_" + hex.EncodeToString(contentDigest[:])
	if info, found, statErr := statContent(root, reference); statErr != nil {
		return sharedartifact.ContentInfo{}, statErr
	} else if found {
		if info.SHA256 != hex.EncodeToString(contentDigest[:]) || info.Size != int64(len(content)) {
			return sharedartifact.ContentInfo{}, sharedartifact.ErrIdentityConflict
		}
		return info, nil
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return sharedartifact.ContentInfo{}, err
	}
	pending := ".pending_" + hex.EncodeToString(nonce[:])
	file, err := root.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return sharedartifact.ContentInfo{}, err
	}
	defer root.Remove(pending)
	if n, writeErr := file.Write(content); writeErr != nil || n != len(content) {
		_ = file.Close()
		if writeErr != nil {
			return sharedartifact.ContentInfo{}, writeErr
		}
		return sharedartifact.ContentInfo{}, io.ErrShortWrite
	}
	if err = file.Sync(); err == nil {
		err = file.Close()
	} else {
		_ = file.Close()
	}
	if err != nil {
		return sharedartifact.ContentInfo{}, err
	}
	if err = root.Rename(pending, reference+".blob"); err != nil {
		return sharedartifact.ContentInfo{}, err
	}
	if err = syncRoot(root); err != nil {
		return sharedartifact.ContentInfo{}, err
	}
	return sharedartifact.ContentInfo{Reference: reference, SHA256: hex.EncodeToString(contentDigest[:]), Size: int64(len(content))}, nil
}

func (f *ContentFiles) Open(ctx context.Context, workspaceID, reference string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !artifactReferencePattern.MatchString(reference) {
		return nil, fmt.Errorf("shared artifact content reference is invalid")
	}
	root, err := f.workspace(workspaceID, false)
	if err != nil {
		return nil, err
	}
	file, err := root.Open(reference + ".blob")
	_ = root.Close()
	if errors.Is(err, os.ErrNotExist) {
		return nil, sharedartifact.ErrContentNotFound
	}
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxArtifactContentBytes {
		_ = file.Close()
		return nil, fmt.Errorf("shared artifact content file is invalid")
	}
	return file, nil
}

func (f *ContentFiles) Stat(ctx context.Context, workspaceID, reference string) (sharedartifact.ContentInfo, error) {
	if err := ctx.Err(); err != nil {
		return sharedartifact.ContentInfo{}, err
	}
	if !artifactReferencePattern.MatchString(reference) {
		return sharedartifact.ContentInfo{}, fmt.Errorf("shared artifact content reference is invalid")
	}
	root, err := f.workspace(workspaceID, false)
	if err != nil {
		return sharedartifact.ContentInfo{}, err
	}
	defer root.Close()
	info, found, err := statContent(root, reference)
	if err != nil {
		return sharedartifact.ContentInfo{}, err
	}
	if !found {
		return sharedartifact.ContentInfo{}, sharedartifact.ErrContentNotFound
	}
	return info, nil
}

func (f *ContentFiles) Delete(ctx context.Context, workspaceID, reference string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !artifactReferencePattern.MatchString(reference) {
		return fmt.Errorf("shared artifact content reference is invalid")
	}
	root, err := f.workspace(workspaceID, false)
	if errors.Is(err, sharedartifact.ErrContentNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.Remove(reference + ".blob"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRoot(root)
}

func (f *ContentFiles) workspace(workspaceID string, create bool) (*os.Root, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || len(workspaceID) > 255 || f == nil || f.root == nil {
		return nil, fmt.Errorf("shared artifact workspace is invalid")
	}
	digest := sha256.Sum256([]byte(workspaceID))
	name := hex.EncodeToString(digest[:])
	if create {
		if err := f.root.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := f.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, sharedartifact.ErrContentNotFound
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("shared artifact workspace directory is invalid")
	}
	return f.root.OpenRoot(name)
}

func statContent(root *os.Root, reference string) (sharedartifact.ContentInfo, bool, error) {
	file, err := root.Open(reference + ".blob")
	if errors.Is(err, os.ErrNotExist) {
		return sharedartifact.ContentInfo{}, false, nil
	}
	if err != nil {
		return sharedartifact.ContentInfo{}, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxArtifactContentBytes {
		return sharedartifact.ContentInfo{}, false, fmt.Errorf("shared artifact content file is invalid")
	}
	digest := sha256.New()
	if _, err = io.Copy(digest, io.LimitReader(file, maxArtifactContentBytes+1)); err != nil {
		return sharedartifact.ContentInfo{}, false, err
	}
	return sharedartifact.ContentInfo{Reference: reference, SHA256: hex.EncodeToString(digest.Sum(nil)), Size: info.Size()}, true, nil
}

func syncRoot(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	return closeErr
}

var _ sharedartifact.ContentStore = (*ContentFiles)(nil)
var _ sharedartifact.ContentWriter = (*ContentFiles)(nil)
