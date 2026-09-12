package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
)

type LifecycleExecutor struct {
	repository agentpersistence.AgentLifecycleRepository
	archives   lifecyclecontract.ArchiveWriter
}

func NewLifecycleExecutor(repository agentpersistence.AgentLifecycleRepository, archives lifecyclecontract.ArchiveWriter) lifecyclecontract.OwnerLifecycleExecutor {
	return LifecycleExecutor{repository: repository, archives: archives}
}

func (LifecycleExecutor) Owner(context.Context) string { return "agent" }

func (e LifecycleExecutor) Preview(ctx context.Context, workspaceID string, policy lifecyclemodel.PolicyVersion, now time.Time) (lifecyclecontract.CleanupPreview, error) {
	candidates, err := e.candidates(ctx, workspaceID, policy.Policy, now, 0)
	if err != nil {
		return lifecyclecontract.CleanupPreview{}, err
	}
	result := lifecyclecontract.CleanupPreview{Rows: int64(len(candidates))}
	for _, candidate := range candidates {
		updated := lifecycleCandidateUpdatedAt(candidate)
		result.Bytes += int64(len(candidate.Payload))
		if result.OldestEligible.IsZero() || updated.Before(result.OldestEligible) {
			result.OldestEligible = updated
		}
	}
	return result, nil
}

func (e LifecycleExecutor) ProcessBatch(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, holds []lifecyclemodel.LegalHold, batchSize int) (lifecyclemodel.CleanupBatchResult, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	candidates, err := e.candidates(ctx, job.WorkspaceID, policy.Policy, job.UpdatedAt, batchSize)
	if err != nil {
		return lifecyclemodel.CleanupBatchResult{}, err
	}
	result := lifecyclemodel.CleanupBatchResult{Done: len(candidates) < batchSize}
	for _, candidate := range candidates {
		resourceID := candidate.ResourceID
		resourceType := candidate.ResourceType
		if resourceType == "" {
			resourceType = "agent.state"
		}
		result.Scanned++
		result.Checkpoint = resourceType + ":" + resourceID
		updated := lifecycleCandidateUpdatedAt(candidate)
		if result.OldestEligible.IsZero() || updated.Before(result.OldestEligible) {
			result.OldestEligible = updated
		}
		if agentLifecycleHeld(holds, resourceType, resourceID, job.UpdatedAt) {
			result.Skipped++
			continue
		}
		if job.DryRun {
			continue
		}
		raw := candidate.Payload
		if len(raw) == 0 {
			raw, _ = json.Marshal(map[string]any{"workspace_id": job.WorkspaceID, "kind": candidate.State.Kind, "state_key": candidate.State.Key, "user_id": candidate.State.UserID, "role_key": candidate.State.RoleKey, "payload_json": json.RawMessage(candidate.State.Payload), "updated_at": candidate.State.UpdatedAt})
		}
		archived, archiveErr := e.archives.ArchivePayload(ctx, "agent", job, policy, resourceType, resourceID, raw)
		if archiveErr != nil {
			result.Failed++
			return result, archiveErr
		}
		if archived {
			result.Archived++
		}
		if job.Operation == lifecyclemodel.OperationArchive {
			continue
		}
		deleted, deleteErr := e.repository.DeleteLifecycleCandidate(ctx, job.WorkspaceID, candidate)
		if deleteErr != nil {
			result.Failed++
			return result, deleteErr
		}
		if deleted {
			result.Purged++
		}
	}
	return result, nil
}

func lifecycleCandidateUpdatedAt(candidate agentpersistence.LifecycleCandidate) time.Time {
	if !candidate.UpdatedAt.IsZero() {
		return candidate.UpdatedAt.UTC()
	}
	return time.Unix(0, candidate.State.UpdatedAt).UTC()
}

func (e LifecycleExecutor) candidates(ctx context.Context, workspaceID string, policy lifecyclemodel.RetentionPolicy, now time.Time, limit int) ([]agentpersistence.LifecycleCandidate, error) {
	if e.repository == nil {
		return nil, fmt.Errorf("Agent lifecycle repository is unavailable")
	}
	return e.repository.ListLifecycleCandidates(ctx, workspaceID, agentpersistence.LifecycleQuery{
		PolicyKey: policy.Key, Now: now, Retention: policy.DefaultRetention, StatusRetention: policy.StatusRetention, Limit: limit,
	})
}

func agentLifecycleHeld(holds []lifecyclemodel.LegalHold, resourceType, resourceID string, now time.Time) bool {
	for _, hold := range holds {
		if now.Before(hold.StartsAt) || (hold.EndsAt != nil && !now.Before(*hold.EndsAt)) {
			continue
		}
		if (hold.Owner == "" || hold.Owner == "agent") && (hold.ResourceType == "" || hold.ResourceType == resourceType) && (hold.ResourceID == "" || hold.ResourceID == resourceID) {
			return true
		}
	}
	return false
}
