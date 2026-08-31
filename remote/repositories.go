package remote

import (
	"context"
	"time"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentstate "github.com/domainry/domainry-agent-sdk/state"
)

func (c *client) persistenceCall(ctx context.Context, path string, payload, out any) error {
	return c.call(ctx, "POST", path, payload, "", out)
}

func (c *client) SyncDefinitions(ctx context.Context, value agentpersistence.DefinitionSnapshot) error {
	return c.persistenceCall(ctx, "/api/v1/definitions/sync", value, &struct{}{})
}
func (c *client) DefinitionSnapshot(ctx context.Context) (agentpersistence.DefinitionSnapshot, error) {
	var out agentpersistence.DefinitionSnapshot
	err := c.persistenceCall(ctx, "/api/v1/definitions/snapshot", struct{}{}, &out)
	return out, err
}
func (c *client) List(ctx context.Context, workspaceID, kind, userID, roleKey string) ([]agentstate.AgentStateRecord, error) {
	var out []agentstate.AgentStateRecord
	err := c.persistenceCall(ctx, "/api/v1/internal/dialog-state/records/query", map[string]any{"workspace_id": workspaceID, "kind": kind, "user_id": userID, "role_key": roleKey}, &out)
	return out, err
}
func (c *client) Get(ctx context.Context, workspaceID, kind, key string) (agentstate.AgentStateRecord, bool, error) {
	var out struct {
		Value agentstate.AgentStateRecord `json:"value"`
		Found bool                        `json:"found"`
	}
	err := c.persistenceCall(ctx, "/api/v1/internal/dialog-state/records/get", map[string]string{"workspace_id": workspaceID, "kind": kind, "key": key}, &out)
	return out.Value, out.Found, err
}
func (c *client) Put(ctx context.Context, workspaceID string, value agentstate.AgentStateRecord) error {
	return c.persistenceCall(ctx, "/api/v1/internal/dialog-state/records/put", map[string]any{"workspace_id": workspaceID, "value": value}, &struct{}{})
}
func (c *client) PutBatch(ctx context.Context, workspaceID string, values []agentstate.AgentStateRecord) error {
	return c.persistenceCall(ctx, "/api/v1/internal/dialog-state/records/put-batch", map[string]any{"workspace_id": workspaceID, "values": values}, &struct{}{})
}
func (c *client) CompareAndSwap(ctx context.Context, workspaceID string, value agentstate.AgentStateRecord, expected int64) (bool, error) {
	var out struct {
		Swapped bool `json:"swapped"`
	}
	err := c.persistenceCall(ctx, "/api/v1/internal/dialog-state/records/compare-and-swap", map[string]any{"workspace_id": workspaceID, "value": value, "expected_updated_at": expected}, &out)
	return out.Swapped, err
}
func (c *client) Create(ctx context.Context, value agentstate.AgentTaskRun) (agentstate.AgentTaskRun, bool, error) {
	var out struct {
		Value   agentstate.AgentTaskRun `json:"value"`
		Created bool                    `json:"created"`
	}
	err := c.persistenceCall(ctx, "/api/v1/internal/execution-state/task-runs/create", value, &out)
	return out.Value, out.Created, err
}
func (c *client) GetTask(ctx context.Context, workspaceID, runID string) (agentstate.AgentTaskRun, bool, error) {
	var out struct {
		Value agentstate.AgentTaskRun `json:"value"`
		Found bool                    `json:"found"`
	}
	err := c.persistenceCall(ctx, "/api/v1/internal/execution-state/task-runs/get", map[string]string{"workspace_id": workspaceID, "run_id": runID}, &out)
	return out.Value, out.Found, err
}
func (c *client) ListTasks(ctx context.Context, workspaceID string, filter agentpersistence.AgentTaskRunFilter) ([]agentstate.AgentTaskRun, error) {
	var out []agentstate.AgentTaskRun
	err := c.persistenceCall(ctx, "/api/v1/internal/execution-state/task-runs/query", map[string]any{"workspace_id": workspaceID, "filter": filter}, &out)
	return out, err
}
func (c *client) ClaimNext(ctx context.Context, workspaceID, owner string, now time.Time, lease time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	var out struct {
		Value agentpersistence.AgentTaskClaim `json:"value"`
		Found bool                            `json:"found"`
	}
	err := c.persistenceCall(ctx, "/api/v1/internal/execution-state/task-runs/claim-next", map[string]any{"workspace_id": workspaceID, "owner": owner, "now": now, "lease": lease}, &out)
	return out.Value, out.Found, err
}
func (c *client) Heartbeat(ctx context.Context, workspaceID, runID, owner string, token int64, now time.Time, lease time.Duration) (agentpersistence.AgentTaskHeartbeatResult, error) {
	var out agentpersistence.AgentTaskHeartbeatResult
	err := c.persistenceCall(ctx, "/api/v1/internal/execution-state/task-runs/heartbeat", map[string]any{"workspace_id": workspaceID, "run_id": runID, "owner": owner, "token": token, "now": now, "lease": lease}, &out)
	return out, err
}
func (c *client) SaveRunning(ctx context.Context, value agentstate.AgentTaskRun, owner string, token int64) error {
	return c.persistenceCall(ctx, "/api/v1/internal/execution-state/task-runs/save-running", map[string]any{"value": value, "owner": owner, "token": token}, &struct{}{})
}
func (c *client) SaveWaitingApproval(ctx context.Context, value agentstate.AgentTaskRun, token int64) error {
	return c.persistenceCall(ctx, "/api/v1/internal/execution-state/task-runs/save-waiting", map[string]any{"value": value, "token": token}, &struct{}{})
}
func (c *client) SaveTerminalOverride(ctx context.Context, value agentstate.AgentTaskRun, token int64) error {
	return c.persistenceCall(ctx, "/api/v1/internal/execution-state/task-runs/override", map[string]any{"value": value, "token": token}, &struct{}{})
}
func (c *client) SaveOperationalTransition(ctx context.Context, value agentstate.AgentTaskRun, expected agentstate.AgentTaskRunStatus, token int64) error {
	return c.persistenceCall(ctx, "/api/v1/internal/execution-state/task-runs/operate", map[string]any{"value": value, "expected": expected, "token": token}, &struct{}{})
}
func (c *client) RequestCancel(ctx context.Context, workspaceID, runID, reason string, now time.Time) (agentstate.AgentTaskRun, bool, error) {
	var out struct {
		Value   agentstate.AgentTaskRun `json:"value"`
		Changed bool                    `json:"changed"`
	}
	err := c.persistenceCall(ctx, "/api/v1/internal/execution-state/task-runs/request-cancel", map[string]any{"workspace_id": workspaceID, "run_id": runID, "reason": reason, "now": now}, &out)
	return out.Value, out.Changed, err
}
func (c *client) ListLifecycleCandidates(ctx context.Context, workspaceID string, query agentpersistence.LifecycleQuery) ([]agentpersistence.LifecycleCandidate, error) {
	var out []agentpersistence.LifecycleCandidate
	err := c.persistenceCall(ctx, "/api/v1/lifecycle/executions/query", map[string]any{"workspace_id": workspaceID, "query": query}, &out)
	return out, err
}
func (c *client) DeleteLifecycleCandidate(ctx context.Context, workspaceID string, value agentpersistence.LifecycleCandidate) (bool, error) {
	var out struct {
		Deleted bool `json:"deleted"`
	}
	err := c.persistenceCall(ctx, "/api/v1/lifecycle/executions/delete", map[string]any{"workspace_id": workspaceID, "value": value}, &out)
	return out.Deleted, err
}

type taskRepository struct {
	client *client
}

func (r taskRepository) Create(c context.Context, v agentstate.AgentTaskRun) (agentstate.AgentTaskRun, bool, error) {
	return r.client.Create(c, v)
}
func (r taskRepository) Get(c context.Context, w, id string) (agentstate.AgentTaskRun, bool, error) {
	return r.client.GetTask(c, w, id)
}
func (r taskRepository) List(c context.Context, w string, f agentpersistence.AgentTaskRunFilter) ([]agentstate.AgentTaskRun, error) {
	return r.client.ListTasks(c, w, f)
}
func (r taskRepository) ClaimNext(c context.Context, w, o string, n time.Time, l time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	return r.client.ClaimNext(c, w, o, n, l)
}
func (r taskRepository) Heartbeat(c context.Context, w, id, o string, t int64, n time.Time, l time.Duration) (agentpersistence.AgentTaskHeartbeatResult, error) {
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
func (r taskRepository) ListAgentTaskRunsForWorker(c context.Context, s agentpersistence.SystemScope, f agentpersistence.AgentTaskRunFilter) ([]agentstate.AgentTaskRun, error) {
	var out []agentstate.AgentTaskRun
	err := r.client.persistenceCall(c, "/api/v1/internal/execution-state/task-runs/worker/query", map[string]any{"scope": s, "filter": f}, &out)
	return out, err
}
func (r taskRepository) ClaimNextAgentTaskRunForWorker(c context.Context, s agentpersistence.SystemScope, o string, n time.Time, l time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	var out struct {
		Value agentpersistence.AgentTaskClaim `json:"value"`
		Found bool                            `json:"found"`
	}
	err := r.client.persistenceCall(c, "/api/v1/internal/execution-state/task-runs/worker/claim", map[string]any{"scope": s, "owner": o, "now": n, "lease": l}, &out)
	return out.Value, out.Found, err
}
func (r taskRepository) ClaimAgentTaskRun(c context.Context, w, id, o string, n time.Time, l time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	var out struct {
		Value agentpersistence.AgentTaskClaim `json:"value"`
		Found bool                            `json:"found"`
	}
	err := r.client.persistenceCall(c, "/api/v1/internal/execution-state/task-runs/claim", map[string]any{"workspace_id": w, "run_id": id, "owner": o, "now": n, "lease": l}, &out)
	return out.Value, out.Found, err
}
func (r taskRepository) CreateInteractiveRun(c context.Context, v agentstate.AgentInteractiveRun) (agentstate.AgentInteractiveRun, bool, error) {
	var out struct {
		Value  agentstate.AgentInteractiveRun `json:"value"`
		Replay bool                           `json:"replay"`
	}
	err := r.client.persistenceCall(c, "/api/v1/internal/execution-state/interactive-runs/create", v, &out)
	return out.Value, out.Replay, err
}
func (r taskRepository) GetInteractiveRun(c context.Context, w, id string) (agentstate.AgentInteractiveRun, bool, error) {
	var out struct {
		Value agentstate.AgentInteractiveRun `json:"value"`
		Found bool                           `json:"found"`
	}
	err := r.client.persistenceCall(c, "/api/v1/internal/execution-state/interactive-runs/get", map[string]string{"workspace_id": w, "run_id": id}, &out)
	return out.Value, out.Found, err
}
func (r taskRepository) ListInteractiveRuns(c context.Context, w, session, user string, f agentpersistence.AgentInteractiveRunFilter) ([]agentstate.AgentInteractiveRun, error) {
	var out []agentstate.AgentInteractiveRun
	err := r.client.persistenceCall(c, "/api/v1/internal/execution-state/interactive-runs/query", map[string]any{"workspace_id": w, "session_id": session, "user_id": user, "filter": f}, &out)
	return out, err
}
func (r taskRepository) SaveInteractiveRun(c context.Context, v agentstate.AgentInteractiveRun, expected int64) (bool, error) {
	var out struct {
		Saved bool `json:"saved"`
	}
	err := r.client.persistenceCall(c, "/api/v1/internal/execution-state/interactive-runs/save", map[string]any{"value": v, "expected_updated_at": expected}, &out)
	return out.Saved, err
}
func (r taskRepository) CommitInteractiveTaskHandoff(c context.Context, v agentstate.AgentInteractiveRun, expected int64, task agentstate.AgentTaskRun) (agentstate.AgentInteractiveRun, bool, error) {
	var out struct {
		Value  agentstate.AgentInteractiveRun `json:"value"`
		Replay bool                           `json:"replay"`
	}
	err := r.client.persistenceCall(c, "/api/v1/internal/execution-state/interactive-runs/handoff", map[string]any{"value": v, "expected_updated_at": expected, "task": task}, &out)
	return out.Value, out.Replay, err
}
func (r taskRepository) BeginAgentToolCall(c context.Context, v agentpersistence.AgentToolCallStart) (string, int, error) {
	var out struct {
		Ref     string `json:"ref"`
		Attempt int    `json:"attempt"`
	}
	err := r.client.persistenceCall(c, "/api/v1/internal/execution-state/tool-invocations/begin", v, &out)
	return out.Ref, out.Attempt, err
}
func (r taskRepository) FinishAgentToolCall(c context.Context, v agentpersistence.AgentToolCallFinish) error {
	return r.client.persistenceCall(c, "/api/v1/internal/execution-state/tool-invocations/finish", v, &struct{}{})
}

var _ agentpersistence.DefinitionRepository = (*client)(nil)
var _ agentpersistence.AgentStateRepository = (*client)(nil)
var _ agentpersistence.AgentTaskRunRepository = taskRepository{}
var _ agentpersistence.AgentTaskRunSystemWorkerRepository = taskRepository{}
var _ agentpersistence.AgentTaskRunDirectClaimRepository = taskRepository{}
var _ agentpersistence.AgentInteractiveRunRepository = taskRepository{}
var _ agentpersistence.AgentToolCallLedger = taskRepository{}
var _ agentpersistence.AgentLifecycleRepository = (*client)(nil)
