package server

import (
	"net/http"
	"time"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentstate "github.com/domainry/domainry-agent-sdk/state"
)

func (s *Server) repositoryOperation(w http.ResponseWriter, r *http.Request, operation string) {
	if s.config.Repositories == nil {
		writeError(w, http.StatusServiceUnavailable, "agent.saas.repository_unavailable", "repository unavailable")
		return
	}
	state, tasks := s.config.Repositories.AgentStateRepository(), s.config.Repositories.AgentTaskRunRepository()
	definitions, _ := s.config.Repositories.(agentpersistence.DefinitionBinding)
	lifecycles, _ := s.config.Repositories.(agentpersistence.LifecycleBinding)
	bad := func(err error) { writeError(w, http.StatusBadRequest, "agent.saas.request_invalid", err.Error()) }
	fail := func(err error) {
		writeError(w, http.StatusInternalServerError, "agent.saas.repository_failed", err.Error())
	}
	switch operation {
	case "definitions.sync":
		var input agentpersistence.DefinitionSnapshot
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		if definitions == nil {
			writeError(w, 503, "agent.saas.repository_unavailable", "definition repository unavailable")
			return
		}
		if err := definitions.DefinitionRepository().SyncDefinitions(r.Context(), input); err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, struct{}{})
	case "definitions.snapshot":
		var input struct{}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		if definitions == nil {
			writeError(w, 503, "agent.saas.repository_unavailable", "definition repository unavailable")
			return
		}
		value, err := definitions.DefinitionRepository().DefinitionSnapshot(r.Context())
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, value)
	case "state.list":
		var input struct {
			WorkspaceID string `json:"workspace_id"`
			Kind        string `json:"kind"`
			UserID      string `json:"user_id"`
			RoleKey     string `json:"role_key"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		value, err := state.List(r.Context(), input.WorkspaceID, input.Kind, input.UserID, input.RoleKey)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, value)
	case "state.get":
		var input struct {
			WorkspaceID string `json:"workspace_id"`
			Kind        string `json:"kind"`
			Key         string `json:"key"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		value, found, err := state.Get(r.Context(), input.WorkspaceID, input.Kind, input.Key)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"value": value, "found": found})
	case "state.put":
		var input struct {
			WorkspaceID string                      `json:"workspace_id"`
			Value       agentstate.AgentStateRecord `json:"value"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		if err := state.Put(r.Context(), input.WorkspaceID, input.Value); err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, struct{}{})
	case "state.put_batch":
		var input struct {
			WorkspaceID string                        `json:"workspace_id"`
			Values      []agentstate.AgentStateRecord `json:"values"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		if err := state.PutBatch(r.Context(), input.WorkspaceID, input.Values); err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, struct{}{})
	case "state.compare_and_swap":
		var input struct {
			WorkspaceID string                      `json:"workspace_id"`
			Value       agentstate.AgentStateRecord `json:"value"`
			Expected    int64                       `json:"expected_updated_at"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		swapped, err := state.CompareAndSwap(r.Context(), input.WorkspaceID, input.Value, input.Expected)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]bool{"swapped": swapped})
	case "task.create":
		var input agentstate.AgentTaskRun
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		value, created, err := tasks.Create(r.Context(), input)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"value": value, "created": created})
	case "task.get":
		var input struct {
			WorkspaceID string `json:"workspace_id"`
			RunID       string `json:"run_id"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		value, found, err := tasks.Get(r.Context(), input.WorkspaceID, input.RunID)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"value": value, "found": found})
	case "task.list":
		var input struct {
			WorkspaceID string                              `json:"workspace_id"`
			Filter      agentpersistence.AgentTaskRunFilter `json:"filter"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		value, err := tasks.List(r.Context(), input.WorkspaceID, input.Filter)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, value)
	case "task.claim_next":
		var input struct {
			WorkspaceID string        `json:"workspace_id"`
			Owner       string        `json:"owner"`
			Now         time.Time     `json:"now"`
			Lease       time.Duration `json:"lease"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		value, found, err := tasks.ClaimNext(r.Context(), input.WorkspaceID, input.Owner, input.Now, input.Lease)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"value": value, "found": found})
	case "task.heartbeat":
		var input struct {
			WorkspaceID string        `json:"workspace_id"`
			RunID       string        `json:"run_id"`
			Owner       string        `json:"owner"`
			Token       int64         `json:"token"`
			Now         time.Time     `json:"now"`
			Lease       time.Duration `json:"lease"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		value, err := tasks.Heartbeat(r.Context(), input.WorkspaceID, input.RunID, input.Owner, input.Token, input.Now, input.Lease)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, value)
	case "task.save_running":
		s.saveTask(w, r, tasks, "running", bad, fail)
	case "task.save_waiting_approval":
		s.saveTask(w, r, tasks, "waiting", bad, fail)
	case "task.save_terminal_override":
		s.saveTask(w, r, tasks, "terminal", bad, fail)
	case "task.save_operational_transition":
		s.saveTask(w, r, tasks, "transition", bad, fail)
	case "task.request_cancel":
		var input struct {
			WorkspaceID string    `json:"workspace_id"`
			RunID       string    `json:"run_id"`
			Reason      string    `json:"reason"`
			Now         time.Time `json:"now"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		value, changed, err := tasks.RequestCancel(r.Context(), input.WorkspaceID, input.RunID, input.Reason, input.Now)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"value": value, "changed": changed})
	case "task.worker_list":
		var input struct {
			Scope  agentpersistence.SystemScope        `json:"scope"`
			Filter agentpersistence.AgentTaskRunFilter `json:"filter"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		repository, ok := tasks.(agentpersistence.AgentTaskRunSystemWorkerRepository)
		if !ok {
			writeError(w, 503, "agent.saas.repository_unavailable", "worker repository unavailable")
			return
		}
		value, err := repository.ListAgentTaskRunsForWorker(r.Context(), input.Scope, input.Filter)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, value)
	case "task.worker_claim":
		var input struct {
			Scope agentpersistence.SystemScope `json:"scope"`
			Owner string                       `json:"owner"`
			Now   time.Time                    `json:"now"`
			Lease time.Duration                `json:"lease"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		repository, ok := tasks.(agentpersistence.AgentTaskRunSystemWorkerRepository)
		if !ok {
			writeError(w, 503, "agent.saas.repository_unavailable", "worker repository unavailable")
			return
		}
		value, found, err := repository.ClaimNextAgentTaskRunForWorker(r.Context(), input.Scope, input.Owner, input.Now, input.Lease)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"value": value, "found": found})
	case "task.direct_claim":
		var input struct {
			WorkspaceID string        `json:"workspace_id"`
			RunID       string        `json:"run_id"`
			Owner       string        `json:"owner"`
			Now         time.Time     `json:"now"`
			Lease       time.Duration `json:"lease"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		repository, ok := tasks.(agentpersistence.AgentTaskRunDirectClaimRepository)
		if !ok {
			writeError(w, 503, "agent.saas.repository_unavailable", "direct claim repository unavailable")
			return
		}
		value, found, err := repository.ClaimAgentTaskRun(r.Context(), input.WorkspaceID, input.RunID, input.Owner, input.Now, input.Lease)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"value": value, "found": found})
	case "interactive.create":
		var input agentstate.AgentInteractiveRun
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		repository, ok := tasks.(agentpersistence.AgentInteractiveRunRepository)
		if !ok {
			writeError(w, 503, "agent.saas.repository_unavailable", "interactive repository unavailable")
			return
		}
		value, replay, err := repository.CreateInteractiveRun(r.Context(), input)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"value": value, "replay": replay})
	case "interactive.get":
		var input struct {
			WorkspaceID string `json:"workspace_id"`
			RunID       string `json:"run_id"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		repository, ok := tasks.(agentpersistence.AgentInteractiveRunRepository)
		if !ok {
			writeError(w, 503, "agent.saas.repository_unavailable", "interactive repository unavailable")
			return
		}
		value, found, err := repository.GetInteractiveRun(r.Context(), input.WorkspaceID, input.RunID)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"value": value, "found": found})
	case "interactive.list":
		var input struct {
			WorkspaceID string                                     `json:"workspace_id"`
			SessionID   string                                     `json:"session_id"`
			UserID      string                                     `json:"user_id"`
			Filter      agentpersistence.AgentInteractiveRunFilter `json:"filter"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		repository, ok := tasks.(agentpersistence.AgentInteractiveRunRepository)
		if !ok {
			writeError(w, 503, "agent.saas.repository_unavailable", "interactive repository unavailable")
			return
		}
		value, err := repository.ListInteractiveRuns(r.Context(), input.WorkspaceID, input.SessionID, input.UserID, input.Filter)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, value)
	case "interactive.save":
		var input struct {
			Value    agentstate.AgentInteractiveRun `json:"value"`
			Expected int64                          `json:"expected_updated_at"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		repository, ok := tasks.(agentpersistence.AgentInteractiveRunRepository)
		if !ok {
			writeError(w, 503, "agent.saas.repository_unavailable", "interactive repository unavailable")
			return
		}
		saved, err := repository.SaveInteractiveRun(r.Context(), input.Value, input.Expected)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]bool{"saved": saved})
	case "interactive.handoff":
		var input struct {
			Value    agentstate.AgentInteractiveRun `json:"value"`
			Expected int64                          `json:"expected_updated_at"`
			Task     agentstate.AgentTaskRun        `json:"task"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		repository, ok := tasks.(agentpersistence.AgentInteractiveRunRepository)
		if !ok {
			writeError(w, 503, "agent.saas.repository_unavailable", "interactive repository unavailable")
			return
		}
		value, replay, err := repository.CommitInteractiveTaskHandoff(r.Context(), input.Value, input.Expected, input.Task)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"value": value, "replay": replay})
	case "tool.begin":
		var input agentpersistence.AgentToolCallStart
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		repository, ok := tasks.(agentpersistence.AgentToolCallLedger)
		if !ok {
			writeError(w, 503, "agent.saas.repository_unavailable", "tool ledger unavailable")
			return
		}
		ref, attempt, err := repository.BeginAgentToolCall(r.Context(), input)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]any{"ref": ref, "attempt": attempt})
	case "tool.finish":
		var input agentpersistence.AgentToolCallFinish
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		repository, ok := tasks.(agentpersistence.AgentToolCallLedger)
		if !ok {
			writeError(w, 503, "agent.saas.repository_unavailable", "tool ledger unavailable")
			return
		}
		if err := repository.FinishAgentToolCall(r.Context(), input); err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, struct{}{})
	case "lifecycle.list":
		var input struct {
			WorkspaceID string                          `json:"workspace_id"`
			Query       agentpersistence.LifecycleQuery `json:"query"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		if lifecycles == nil {
			writeError(w, 503, "agent.saas.repository_unavailable", "lifecycle repository unavailable")
			return
		}
		value, err := lifecycles.AgentLifecycleRepository().ListLifecycleCandidates(r.Context(), input.WorkspaceID, input.Query)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, value)
	case "lifecycle.referenced", "lifecycle.delete":
		var input struct {
			WorkspaceID string                              `json:"workspace_id"`
			Value       agentpersistence.LifecycleCandidate `json:"value"`
		}
		if err := decode(r, &input); err != nil {
			bad(err)
			return
		}
		if lifecycles == nil {
			writeError(w, 503, "agent.saas.repository_unavailable", "lifecycle repository unavailable")
			return
		}
		if operation == "lifecycle.referenced" {
			value, err := lifecycles.AgentLifecycleRepository().LifecycleCandidateReferenced(r.Context(), input.WorkspaceID, input.Value)
			if err != nil {
				fail(err)
				return
			}
			writeJSON(w, 200, map[string]bool{"referenced": value})
			return
		}
		value, err := lifecycles.AgentLifecycleRepository().DeleteLifecycleCandidate(r.Context(), input.WorkspaceID, input.Value)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, 200, map[string]bool{"deleted": value})
	default:
		writeError(w, http.StatusNotFound, "agent.saas.operation_unknown", "unknown repository operation")
	}
}

func (s *Server) repositoryHandler(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.repositoryOperation(w, r, operation)
	}
}

func (s *Server) registerPersistenceRoutes(mux *http.ServeMux) {
	routes := map[string]string{
		"/api/v1/definitions/sync":                                   "definitions.sync",
		"/api/v1/definitions/snapshot":                               "definitions.snapshot",
		"/api/v1/internal/dialog-state/records/query":                "state.list",
		"/api/v1/internal/dialog-state/records/get":                  "state.get",
		"/api/v1/internal/dialog-state/records/put":                  "state.put",
		"/api/v1/internal/dialog-state/records/put-batch":            "state.put_batch",
		"/api/v1/internal/dialog-state/records/compare-and-swap":     "state.compare_and_swap",
		"/api/v1/internal/execution-state/task-runs/create":          "task.create",
		"/api/v1/internal/execution-state/task-runs/get":             "task.get",
		"/api/v1/internal/execution-state/task-runs/query":           "task.list",
		"/api/v1/internal/execution-state/task-runs/claim-next":      "task.claim_next",
		"/api/v1/internal/execution-state/task-runs/heartbeat":       "task.heartbeat",
		"/api/v1/internal/execution-state/task-runs/save-running":    "task.save_running",
		"/api/v1/internal/execution-state/task-runs/save-waiting":    "task.save_waiting_approval",
		"/api/v1/internal/execution-state/task-runs/override":        "task.save_terminal_override",
		"/api/v1/internal/execution-state/task-runs/operate":         "task.save_operational_transition",
		"/api/v1/internal/execution-state/task-runs/request-cancel":  "task.request_cancel",
		"/api/v1/internal/execution-state/task-runs/worker/query":    "task.worker_list",
		"/api/v1/internal/execution-state/task-runs/worker/claim":    "task.worker_claim",
		"/api/v1/internal/execution-state/task-runs/claim":           "task.direct_claim",
		"/api/v1/internal/execution-state/interactive-runs/create":   "interactive.create",
		"/api/v1/internal/execution-state/interactive-runs/get":      "interactive.get",
		"/api/v1/internal/execution-state/interactive-runs/query":    "interactive.list",
		"/api/v1/internal/execution-state/interactive-runs/save":     "interactive.save",
		"/api/v1/internal/execution-state/interactive-runs/handoff":  "interactive.handoff",
		"/api/v1/internal/execution-state/tool-invocations/begin":    "tool.begin",
		"/api/v1/internal/execution-state/tool-invocations/finish":   "tool.finish",
		"/api/v1/lifecycle/executions/query":                         "lifecycle.list",
		"/api/v1/lifecycle/executions/referenced":                    "lifecycle.referenced",
		"/api/v1/lifecycle/executions/delete":                        "lifecycle.delete",
	}
	for path, operation := range routes {
		mux.HandleFunc("POST "+path, s.repositoryHandler(operation))
	}
}

func (*Server) saveTask(w http.ResponseWriter, r *http.Request, tasks agentpersistence.AgentTaskRunRepository, kind string, bad, fail func(error)) {
	var input struct {
		Value    agentstate.AgentTaskRun       `json:"value"`
		Owner    string                        `json:"owner"`
		Token    int64                         `json:"token"`
		Expected agentstate.AgentTaskRunStatus `json:"expected"`
	}
	if err := decode(r, &input); err != nil {
		bad(err)
		return
	}
	var err error
	switch kind {
	case "running":
		err = tasks.SaveRunning(r.Context(), input.Value, input.Owner, input.Token)
	case "waiting":
		err = tasks.SaveWaitingApproval(r.Context(), input.Value, input.Token)
	case "terminal":
		err = tasks.SaveTerminalOverride(r.Context(), input.Value, input.Token)
	default:
		err = tasks.SaveOperationalTransition(r.Context(), input.Value, input.Expected, input.Token)
	}
	if err != nil {
		fail(err)
		return
	}
	writeJSON(w, 200, struct{}{})
}
