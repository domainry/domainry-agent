package remote

import (
	"context"
	"fmt"
	"time"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	agentstate "github.com/domainry/domainry-agent-sdk/state"
)

func (c *client) repositoryCall(ctx context.Context, operation string, payload, out any) error {
	return c.call(ctx, "POST", "/api/v1/repository/"+operation, payload, "", out)
}

func (c *client) SyncDefinitions(ctx context.Context, value agentrepository.DefinitionSnapshot) error {
	return c.repositoryCall(ctx, "definitions.sync", value, &struct{}{})
}
func (c *client) DefinitionSnapshot(ctx context.Context) (agentrepository.DefinitionSnapshot, error) {
	var out agentrepository.DefinitionSnapshot
	err := c.repositoryCall(ctx, "definitions.snapshot", struct{}{}, &out)
	return out, err
}
func (c *client) List(ctx context.Context, workspaceID, kind, userID, roleKey string) ([]agentstate.AgentStateRecord, error) {
	var out []agentstate.AgentStateRecord
	err := c.repositoryCall(ctx, "state.list", map[string]any{"workspace_id": workspaceID, "kind": kind, "user_id": userID, "role_key": roleKey}, &out)
	return out, err
}
func (c *client) Get(ctx context.Context, workspaceID, kind, key string) (agentstate.AgentStateRecord, bool, error) {
	var out struct {
		Value agentstate.AgentStateRecord `json:"value"`
		Found bool                        `json:"found"`
	}
	err := c.repositoryCall(ctx, "state.get", map[string]string{"workspace_id": workspaceID, "kind": kind, "key": key}, &out)
	return out.Value, out.Found, err
}
func (c *client) Put(ctx context.Context, workspaceID string, value agentstate.AgentStateRecord) error {
	return c.repositoryCall(ctx, "state.put", map[string]any{"workspace_id": workspaceID, "value": value}, &struct{}{})
}
func (c *client) PutBatch(ctx context.Context, workspaceID string, values []agentstate.AgentStateRecord) error {
	return c.repositoryCall(ctx, "state.put_batch", map[string]any{"workspace_id": workspaceID, "values": values}, &struct{}{})
}
func (c *client) CompareAndSwap(ctx context.Context, workspaceID string, value agentstate.AgentStateRecord, expected int64) (bool, error) {
	var out struct {
		Swapped bool `json:"swapped"`
	}
	err := c.repositoryCall(ctx, "state.compare_and_swap", map[string]any{"workspace_id": workspaceID, "value": value, "expected_updated_at": expected}, &out)
	return out.Swapped, err
}
func (c *client) Create(ctx context.Context, value agentstate.AgentTaskRun) (agentstate.AgentTaskRun, bool, error) {
	var out struct {
		Value   agentstate.AgentTaskRun `json:"value"`
		Created bool                    `json:"created"`
	}
	err := c.repositoryCall(ctx, "task.create", value, &out)
	return out.Value, out.Created, err
}
func (c *client) GetTask(ctx context.Context, workspaceID, runID string) (agentstate.AgentTaskRun, bool, error) {
	var out struct {
		Value agentstate.AgentTaskRun `json:"value"`
		Found bool                    `json:"found"`
	}
	err := c.repositoryCall(ctx, "task.get", map[string]string{"workspace_id": workspaceID, "run_id": runID}, &out)
	return out.Value, out.Found, err
}
func (c *client) ListTasks(ctx context.Context, workspaceID string, filter agentrepository.AgentTaskRunFilter) ([]agentstate.AgentTaskRun, error) {
	var out []agentstate.AgentTaskRun
	err := c.repositoryCall(ctx, "task.list", map[string]any{"workspace_id": workspaceID, "filter": filter}, &out)
	return out, err
}
func (c *client) ClaimNext(ctx context.Context, workspaceID, owner string, now time.Time, lease time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	var out struct {
		Value agentrepository.AgentTaskClaim `json:"value"`
		Found bool                           `json:"found"`
	}
	err := c.repositoryCall(ctx, "task.claim_next", map[string]any{"workspace_id": workspaceID, "owner": owner, "now": now, "lease": lease}, &out)
	return out.Value, out.Found, err
}
func (c *client) Heartbeat(ctx context.Context, workspaceID, runID, owner string, token int64, now time.Time, lease time.Duration) (agentrepository.AgentTaskHeartbeatResult, error) {
	var out agentrepository.AgentTaskHeartbeatResult
	err := c.repositoryCall(ctx, "task.heartbeat", map[string]any{"workspace_id": workspaceID, "run_id": runID, "owner": owner, "token": token, "now": now, "lease": lease}, &out)
	return out, err
}
func (c *client) SaveRunning(ctx context.Context, value agentstate.AgentTaskRun, owner string, token int64) error {
	return c.repositoryCall(ctx, "task.save_running", map[string]any{"value": value, "owner": owner, "token": token}, &struct{}{})
}
func (c *client) SaveWaitingApproval(ctx context.Context, value agentstate.AgentTaskRun, token int64) error {
	return c.repositoryCall(ctx, "task.save_waiting_approval", map[string]any{"value": value, "token": token}, &struct{}{})
}
func (c *client) SaveTerminalOverride(ctx context.Context, value agentstate.AgentTaskRun, token int64) error {
	return c.repositoryCall(ctx, "task.save_terminal_override", map[string]any{"value": value, "token": token}, &struct{}{})
}
func (c *client) SaveOperationalTransition(ctx context.Context, value agentstate.AgentTaskRun, expected agentstate.AgentTaskRunStatus, token int64) error {
	return c.repositoryCall(ctx, "task.save_operational_transition", map[string]any{"value": value, "expected": expected, "token": token}, &struct{}{})
}
func (c *client) RequestCancel(ctx context.Context, workspaceID, runID, reason string, now time.Time) (agentstate.AgentTaskRun, bool, error) {
	var out struct {
		Value   agentstate.AgentTaskRun `json:"value"`
		Changed bool                    `json:"changed"`
	}
	err := c.repositoryCall(ctx, "task.request_cancel", map[string]any{"workspace_id": workspaceID, "run_id": runID, "reason": reason, "now": now}, &out)
	return out.Value, out.Changed, err
}
func (c *client) ListLifecycleCandidates(ctx context.Context, workspaceID string, query agentrepository.LifecycleQuery) ([]agentrepository.LifecycleCandidate, error) {
	var out []agentrepository.LifecycleCandidate
	err := c.repositoryCall(ctx, "lifecycle.list", map[string]any{"workspace_id": workspaceID, "query": query}, &out)
	return out, err
}
func (c *client) LifecycleCandidateReferenced(ctx context.Context, workspaceID string, value agentrepository.LifecycleCandidate) (bool, error) {
	var out struct {
		Referenced bool `json:"referenced"`
	}
	err := c.repositoryCall(ctx, "lifecycle.referenced", map[string]any{"workspace_id": workspaceID, "value": value}, &out)
	return out.Referenced, err
}
func (c *client) DeleteLifecycleCandidate(ctx context.Context, workspaceID string, value agentrepository.LifecycleCandidate) (bool, error) {
	var out struct {
		Deleted bool `json:"deleted"`
	}
	err := c.repositoryCall(ctx, "lifecycle.delete", map[string]any{"workspace_id": workspaceID, "value": value}, &out)
	return out.Deleted, err
}

type taskRepository struct {
	client       *client
	publications *publicationStore
}

func (r taskRepository) Create(c context.Context, v agentstate.AgentTaskRun) (agentstate.AgentTaskRun, bool, error) {
	return r.client.Create(c, v)
}
func (r taskRepository) Get(c context.Context, w, id string) (agentstate.AgentTaskRun, bool, error) {
	return r.client.GetTask(c, w, id)
}
func (r taskRepository) List(c context.Context, w string, f agentrepository.AgentTaskRunFilter) ([]agentstate.AgentTaskRun, error) {
	return r.client.ListTasks(c, w, f)
}
func (r taskRepository) ClaimNext(c context.Context, w, o string, n time.Time, l time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	return r.client.ClaimNext(c, w, o, n, l)
}
func (r taskRepository) Heartbeat(c context.Context, w, id, o string, t int64, n time.Time, l time.Duration) (agentrepository.AgentTaskHeartbeatResult, error) {
	return r.client.Heartbeat(c, w, id, o, t, n, l)
}
func (r taskRepository) SaveRunning(c context.Context, v agentstate.AgentTaskRun, o string, t int64) error {
	return r.client.SaveRunning(c, v, o, t)
}
func (r taskRepository) SaveWaitingApproval(c context.Context, v agentstate.AgentTaskRun, t int64) error {
	return r.client.SaveWaitingApproval(c, v, t)
}
func (r taskRepository) SaveTerminalOverride(c context.Context, v agentstate.AgentTaskRun, t int64) error {
	return r.client.SaveTerminalOverride(c, v, t)
}
func (r taskRepository) SaveOperationalTransition(c context.Context, v agentstate.AgentTaskRun, e agentstate.AgentTaskRunStatus, t int64) error {
	return r.client.SaveOperationalTransition(c, v, e, t)
}
func (r taskRepository) RequestCancel(c context.Context, w, id, reason string, n time.Time) (agentstate.AgentTaskRun, bool, error) {
	return r.client.RequestCancel(c, w, id, reason, n)
}
func (r taskRepository) ListAgentTaskRunsForWorker(c context.Context, s agentrepository.SystemScope, f agentrepository.AgentTaskRunFilter) ([]agentstate.AgentTaskRun, error) {
	var out []agentstate.AgentTaskRun
	err := r.client.repositoryCall(c, "task.worker_list", map[string]any{"scope": s, "filter": f}, &out)
	return out, err
}
func (r taskRepository) ClaimNextAgentTaskRunForWorker(c context.Context, s agentrepository.SystemScope, o string, n time.Time, l time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	var out struct {
		Value agentrepository.AgentTaskClaim `json:"value"`
		Found bool                           `json:"found"`
	}
	err := r.client.repositoryCall(c, "task.worker_claim", map[string]any{"scope": s, "owner": o, "now": n, "lease": l}, &out)
	return out.Value, out.Found, err
}
func (r taskRepository) ClaimAgentTaskRun(c context.Context, w, id, o string, n time.Time, l time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	var out struct {
		Value agentrepository.AgentTaskClaim `json:"value"`
		Found bool                           `json:"found"`
	}
	err := r.client.repositoryCall(c, "task.direct_claim", map[string]any{"workspace_id": w, "run_id": id, "owner": o, "now": n, "lease": l}, &out)
	return out.Value, out.Found, err
}
func (r taskRepository) CreateInteractiveRun(c context.Context, v agentstate.AgentInteractiveRun) (agentstate.AgentInteractiveRun, bool, error) {
	var out struct {
		Value  agentstate.AgentInteractiveRun `json:"value"`
		Replay bool                           `json:"replay"`
	}
	err := r.client.repositoryCall(c, "interactive.create", v, &out)
	return out.Value, out.Replay, err
}
func (r taskRepository) GetInteractiveRun(c context.Context, w, id string) (agentstate.AgentInteractiveRun, bool, error) {
	var out struct {
		Value agentstate.AgentInteractiveRun `json:"value"`
		Found bool                           `json:"found"`
	}
	err := r.client.repositoryCall(c, "interactive.get", map[string]string{"workspace_id": w, "run_id": id}, &out)
	return out.Value, out.Found, err
}
func (r taskRepository) ListInteractiveRuns(c context.Context, w, session, user string, f agentrepository.AgentInteractiveRunFilter) ([]agentstate.AgentInteractiveRun, error) {
	var out []agentstate.AgentInteractiveRun
	err := r.client.repositoryCall(c, "interactive.list", map[string]any{"workspace_id": w, "session_id": session, "user_id": user, "filter": f}, &out)
	return out, err
}
func (r taskRepository) SaveInteractiveRun(c context.Context, v agentstate.AgentInteractiveRun, expected int64) (bool, error) {
	var out struct {
		Saved bool `json:"saved"`
	}
	err := r.client.repositoryCall(c, "interactive.save", map[string]any{"value": v, "expected_updated_at": expected}, &out)
	return out.Saved, err
}
func (r taskRepository) CommitInteractiveTaskHandoff(c context.Context, v agentstate.AgentInteractiveRun, expected int64, task agentstate.AgentTaskRun) (agentstate.AgentInteractiveRun, bool, error) {
	var out struct {
		Value  agentstate.AgentInteractiveRun `json:"value"`
		Replay bool                           `json:"replay"`
	}
	err := r.client.repositoryCall(c, "interactive.handoff", map[string]any{"value": v, "expected_updated_at": expected, "task": task}, &out)
	return out.Value, out.Replay, err
}
func (r taskRepository) BeginAgentToolCall(c context.Context, v agentrepository.AgentToolCallStart) (string, int, error) {
	var out struct {
		Ref     string `json:"ref"`
		Attempt int    `json:"attempt"`
	}
	err := r.client.repositoryCall(c, "tool.begin", v, &out)
	return out.Ref, out.Attempt, err
}
func (r taskRepository) FinishAgentToolCall(c context.Context, v agentrepository.AgentToolCallFinish) error {
	return r.client.repositoryCall(c, "tool.finish", v, &struct{}{})
}
func (r taskRepository) applyMutation(c context.Context, operation string, value agentrepository.AgentTaskMutation) error {
	return r.client.repositoryCall(c, operation, value, &struct{}{})
}
func (r taskRepository) InsertAgentTask(c context.Context, executor modulehost.Executor, value agentrepository.AgentTaskMutation) error {
	if r.publications == nil {
		return fmt.Errorf("Agent SaaS publication store is unavailable")
	}
	return r.publications.InsertAgentTask(c, executor, value)
}
func (r taskRepository) UpdateAgentTask(c context.Context, executor modulehost.Executor, value agentrepository.AgentTaskMutation) error {
	if r.publications == nil {
		return fmt.Errorf("Agent SaaS publication store is unavailable")
	}
	return r.publications.UpdateAgentTask(c, executor, value)
}

var _ agentrepository.DefinitionRepository = (*client)(nil)
var _ agentrepository.AgentStateRepository = (*client)(nil)
var _ agentrepository.AgentTaskRunRepository = taskRepository{}
var _ agentrepository.AgentTaskRunSystemWorkerRepository = taskRepository{}
var _ agentrepository.AgentTaskRunDirectClaimRepository = taskRepository{}
var _ agentrepository.AgentInteractiveRunRepository = taskRepository{}
var _ agentrepository.AgentToolCallLedger = taskRepository{}
var _ agentrepository.AgentTaskTransactionRepository = taskRepository{}
var _ agentrepository.AgentLifecycleRepository = (*client)(nil)
