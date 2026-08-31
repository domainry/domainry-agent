package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type dialogStateMemoryRepository struct {
	mu     sync.Mutex
	values map[string]agentmodel.AgentStateRecord
}

func newDialogStateMemoryRepository() *dialogStateMemoryRepository {
	return &dialogStateMemoryRepository{values: map[string]agentmodel.AgentStateRecord{}}
}

func dialogStateMemoryKey(workspaceID, kind, key string) string {
	return workspaceID + "\x00" + kind + "\x00" + key
}

func (r *dialogStateMemoryRepository) List(_ context.Context, workspaceID, kind, userID, roleKey string) ([]agentmodel.AgentStateRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make([]agentmodel.AgentStateRecord, 0)
	for _, value := range r.values {
		if value.WorkspaceID == workspaceID && value.Kind == kind && value.UserID == userID && value.RoleKey == roleKey {
			values = append(values, value)
		}
	}
	return values, nil
}

func (r *dialogStateMemoryRepository) Get(_ context.Context, workspaceID, kind, key string) (agentmodel.AgentStateRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[dialogStateMemoryKey(workspaceID, kind, key)]
	return value, ok, nil
}

func (r *dialogStateMemoryRepository) Put(_ context.Context, workspaceID string, value agentmodel.AgentStateRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[dialogStateMemoryKey(workspaceID, value.Kind, value.Key)] = value
	return nil
}

func (r *dialogStateMemoryRepository) PutBatch(ctx context.Context, workspaceID string, values []agentmodel.AgentStateRecord) error {
	for _, value := range values {
		if err := r.Put(ctx, workspaceID, value); err != nil {
			return err
		}
	}
	return nil
}

func (r *dialogStateMemoryRepository) CompareAndSwap(_ context.Context, workspaceID string, value agentmodel.AgentStateRecord, expectedUpdatedAt int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := dialogStateMemoryKey(workspaceID, value.Kind, value.Key)
	current, ok := r.values[key]
	if !ok || current.UpdatedAt != expectedUpdatedAt {
		return false, nil
	}
	r.values[key] = value
	return true, nil
}

func TestDialogStateServiceOwnsScopedSessionLifecycle(t *testing.T) {
	repository := newDialogStateMemoryRepository()
	service := NewDialogStateService(repository)
	now := time.Unix(1_700_000_000, 0)
	service.now = func() time.Time {
		now = now.Add(time.Nanosecond)
		return now
	}
	authority := agentsdk.AgentAuthority{WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator"}

	created, err := service.UpsertSession(t.Context(), agentsdk.AgentSessionUpsertRequest{ExternalSessionID: "session-1", LastSummary: "Review customer", Context: map[string]any{"object_key": "customer", "record_id": "customer-1"}}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != "Review customer" || created.ObjectKey != "customer" || created.RecordID != "customer-1" || created.WorkspaceID != authority.WorkspaceID {
		t.Fatalf("created=%+v", created)
	}
	updated, err := service.UpsertSession(t.Context(), agentsdk.AgentSessionUpsertRequest{ExternalSessionID: created.ExternalSessionID, AgentSessionID: "provider-session-1", Title: "Customer review", LastSummary: "Ready"}, authority)
	if err != nil || updated.CreatedAt != created.CreatedAt || updated.UpdatedAt <= created.UpdatedAt {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	listed, err := service.ListSessions(t.Context(), agentsdk.AgentSessionQuery{Search: "customer", Limit: 1}, authority)
	if err != nil || len(listed) != 1 || listed[0].ExternalSessionID != created.ExternalSessionID {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	archived, err := service.SetSessionArchived(t.Context(), created.ExternalSessionID, true, authority)
	if err != nil || !archived.Archived {
		t.Fatalf("archived=%+v err=%v", archived, err)
	}
	listed, err = service.ListSessions(t.Context(), agentsdk.AgentSessionQuery{}, authority)
	if err != nil || len(listed) != 0 {
		t.Fatalf("active sessions=%+v err=%v", listed, err)
	}
	listed, err = service.ListSessions(t.Context(), agentsdk.AgentSessionQuery{IncludeArchived: true}, authority)
	if err != nil || len(listed) != 1 {
		t.Fatalf("archived sessions=%+v err=%v", listed, err)
	}
	other := agentsdk.AgentAuthority{WorkspaceID: authority.WorkspaceID, UserID: "user-2", RoleKey: authority.RoleKey}
	listed, err = service.ListSessions(t.Context(), agentsdk.AgentSessionQuery{IncludeArchived: true}, other)
	if err != nil || len(listed) != 0 {
		t.Fatalf("cross-principal sessions=%+v err=%v", listed, err)
	}
}

func TestDialogStateServiceOwnsProposalCASAndVisibility(t *testing.T) {
	repository := newDialogStateMemoryRepository()
	service := NewDialogStateService(repository)
	now := time.Unix(1_700_000_000, 0)
	service.now = func() time.Time {
		now = now.Add(time.Nanosecond)
		return now
	}
	authority := agentsdk.AgentAuthority{WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator"}
	stored, err := service.StoreProposal(t.Context(), agentsdk.AgentProposal{ProposalID: "proposal-1", WorkspaceID: authority.WorkspaceID, UserID: authority.UserID, Role: authority.RoleKey, Title: "Update customer", Proposed: map[string]any{"action": "update"}})
	if err != nil || stored.Status != "draft" || !stored.Audited {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	approved, err := service.DecideProposal(t.Context(), agentsdk.AgentProposalDecision{ProposalID: stored.ProposalID, Decision: "approved", Reason: "verified", ExpectedUpdatedAt: stored.UpdatedAt}, authority)
	if err != nil || approved.Status != "approved" || approved.DecisionActor != authority.UserID || approved.DecisionReason != "verified" {
		t.Fatalf("approved=%+v err=%v", approved, err)
	}
	_, err = service.DecideProposal(t.Context(), agentsdk.AgentProposalDecision{ProposalID: stored.ProposalID, Decision: "approved", ExpectedUpdatedAt: stored.UpdatedAt}, authority)
	assertDialogStateError(t, err, "conflict", "agent_dialog.proposal_decision_conflict")
	visible, err := service.ListProposals(t.Context(), "approved", authority)
	if err != nil || len(visible) != 1 || visible[0].ProposalID != stored.ProposalID {
		t.Fatalf("visible=%+v err=%v", visible, err)
	}
	_, err = service.GetProposal(t.Context(), stored.ProposalID, agentsdk.AgentAuthority{WorkspaceID: authority.WorkspaceID, UserID: "other", RoleKey: authority.RoleKey})
	assertDialogStateError(t, err, "not_found", "agent_dialog.proposal_not_found")
}

func TestDialogStateServiceFailsClosedWithoutAuthorityOrRepository(t *testing.T) {
	service := NewDialogStateService(nil)
	_, err := service.ListSessions(t.Context(), agentsdk.AgentSessionQuery{}, agentsdk.AgentAuthority{})
	assertDialogStateError(t, err, "forbidden", "backend.workspace_scope_required")
	_, err = service.ListSessions(t.Context(), agentsdk.AgentSessionQuery{}, agentsdk.AgentAuthority{WorkspaceID: "workspace-1"})
	assertDialogStateError(t, err, "unavailable", "agent.dialog_state.unavailable")
	_, err = NewDialogStateService(newDialogStateMemoryRepository()).SetSessionArchived(t.Context(), "", false, agentsdk.AgentAuthority{WorkspaceID: "workspace-1"})
	assertDialogStateError(t, err, "bad_request", "agent_dialog.session_id_required")
}

func assertDialogStateError(t *testing.T, err error, class, code string) {
	t.Helper()
	var value *agentsdk.Error
	if !errors.As(err, &value) || value.Class != class || value.ErrorCode() != code {
		t.Fatalf("error=%v class=%q code=%q", err, class, code)
	}
}
