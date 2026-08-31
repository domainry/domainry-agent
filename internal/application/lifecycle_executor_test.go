package application

import (
	"context"
	"testing"
	"time"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentstate "github.com/domainry/domainry-agent-sdk/state"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
)

type lifecycleRepositoryStub struct {
	candidates []agentpersistence.LifecycleCandidate
	query      agentpersistence.LifecycleQuery
	deleted    []agentpersistence.LifecycleCandidate
}

func (s *lifecycleRepositoryStub) ListLifecycleCandidates(_ context.Context, _ string, query agentpersistence.LifecycleQuery) ([]agentpersistence.LifecycleCandidate, error) {
	s.query = query
	return append([]agentpersistence.LifecycleCandidate(nil), s.candidates...), nil
}
func (s *lifecycleRepositoryStub) DeleteLifecycleCandidate(_ context.Context, _ string, value agentpersistence.LifecycleCandidate) (bool, error) {
	s.deleted = append(s.deleted, value)
	return true, nil
}

type lifecycleArchiveStub struct {
	owner, resourceType, resourceID string
}

func (s *lifecycleArchiveStub) ArchivePayload(_ context.Context, owner string, _ lifecyclemodel.CleanupJob, _ lifecyclemodel.PolicyVersion, resourceType, resourceID string, _ []byte) (bool, error) {
	s.owner, s.resourceType, s.resourceID = owner, resourceType, resourceID
	return true, nil
}

func TestLifecycleExecutorOwnsPreviewArchiveAndPurge(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	repository := &lifecycleRepositoryStub{candidates: []agentpersistence.LifecycleCandidate{{
		State: agentstate.AgentStateRecord{Kind: "session", Key: "session-1", UpdatedAt: now.Add(-2 * time.Hour).UnixNano()}, ResourceID: "session:session-1",
	}}}
	archives := &lifecycleArchiveStub{}
	executor := NewLifecycleExecutor(repository, archives)
	var _ lifecyclecontract.OwnerLifecycleExecutor = executor
	if owner := executor.Owner(t.Context()); owner != "agent" {
		t.Fatalf("owner=%q", owner)
	}
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "agent.dialog.v1", DefaultRetention: time.Hour}}
	preview, err := executor.Preview(t.Context(), "workspace-1", policy, now)
	if err != nil || preview.Rows != 1 || !preview.OldestEligible.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	job := lifecyclemodel.CleanupJob{WorkspaceID: "workspace-1", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}
	result, err := executor.ProcessBatch(t.Context(), job, policy, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 1 || result.Archived != 1 || result.Purged != 1 || len(repository.deleted) != 1 {
		t.Fatalf("result=%#v deleted=%d", result, len(repository.deleted))
	}
	if archives.owner != "agent" || archives.resourceType != "agent.state" || archives.resourceID != "session:session-1" {
		t.Fatalf("archive=%#v", archives)
	}
	if repository.query.PolicyKey != "agent.dialog.v1" || repository.query.Retention != time.Hour {
		t.Fatalf("query=%#v", repository.query)
	}
}
