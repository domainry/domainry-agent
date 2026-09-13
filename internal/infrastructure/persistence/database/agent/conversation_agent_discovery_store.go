package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) ConversationAgentObservations(ctx context.Context, a agentsdk.ConversationAuthority) (persistence.ConversationAgentObservations, error) {
	out := persistence.ConversationAgentObservations{Load: map[string]persistence.ConversationAgentLoad{}, HistoryComplete: true}
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		out = persistence.ConversationAgentObservations{Load: map[string]persistence.ConversationAgentLoad{}, HistoryComplete: true}
		owner := query.Equal("owner_key", conversationOwner(a))
		visible, err := s.visibleConversationAgentIDs(ctx, tx, a)
		if err != nil {
			return err
		}
		workspace := query.Or(owner, query.And(query.Equal("runtime_id", a.RuntimeID), query.Equal("workspace_key", conversationHash([]string{a.RuntimeID, a.WorkspaceID}))))
		// Aggregate visible Agent load across workspace users, without exposing
		// their task identities. Queued tasks and their runs are mutually
		// exclusive in the launch transaction; default stays caller-scoped.
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_runs").Columns("payload_json", "lease_expires_at", "owner_key").Where(query.And(workspace, query.Or(query.Equal("status", "queued"), query.Equal("status", "running"), query.Equal("status", "waiting_user"), query.Equal("status", "waiting_confirmation"), query.Equal("status", "needs_reconciliation")))).Build()
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		for rows.Next() {
			var raw []byte
			var expires int64
			var rowOwner string
			var run agentsdk.ConversationRun
			if err = rows.Scan(&raw, &expires, &rowOwner); err != nil {
				break
			}
			if err = json.Unmarshal(raw, &run); err != nil {
				break
			}
			id := "default"
			if run.Agent != nil {
				id = run.Agent.ID
			}
			if !visible[id] || id == "default" && rowOwner != conversationOwner(a) {
				continue
			}
			load := out.Load[id]
			if run.Waiting() {
				load.Waiting++
			} else if run.Status == "running" && expires > now {
				load.Running++
			} else {
				load.Queued++
			}
			out.Load[id] = load
		}
		if e := rows.Err(); err == nil {
			err = e
		}
		rows.Close()
		if err != nil {
			return err
		}
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns("payload_json", "owner_key").Where(query.And(workspace, query.Equal("status", "queued"))).Build()
		if err != nil {
			return err
		}
		rows, err = tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var raw []byte
			var task agentsdk.ConversationTask
			var rowOwner string
			if err = rows.Scan(&raw, &rowOwner); err != nil {
				break
			}
			if err = json.Unmarshal(raw, &task); err != nil {
				break
			}
			id := "default"
			if task.Agent != nil {
				id = task.Agent.ID
			}
			if !visible[id] || id == "default" && rowOwner != conversationOwner(a) {
				continue
			}
			load := out.Load[id]
			load.Queued++
			out.Load[id] = load
		}
		if e := rows.Err(); err == nil {
			err = e
		}
		rows.Close()
		if err != nil {
			return err
		}
		// Recent history is explicitly sampled; it is not a lifetime success rate.
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_runs").Columns("payload_json").Where(query.And(owner, query.Or(query.Equal("status", "completed"), query.Equal("status", "failed"), query.Equal("status", "cancelled")))).OrderBy(query.Descending("created_at"), query.Descending("run_id")).Limit(201).Build()
		if err != nil {
			return err
		}
		rows, err = tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		var history []agentsdk.ConversationRun
		for rows.Next() {
			if len(history) == 200 {
				out.HistoryComplete = false
				break
			}
			var raw []byte
			var run agentsdk.ConversationRun
			if err = rows.Scan(&raw); err != nil {
				break
			}
			if err = json.Unmarshal(raw, &run); err != nil {
				break
			}
			history = append(history, run)
		}
		if e := rows.Err(); err == nil {
			err = e
		}
		rows.Close()
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, run := range history {
			if run.Agent == nil {
				continue
			} // legacy runs have no verifiable configuration identity
			item := persistence.ConversationAgentObservation{AgentID: run.Agent.ID, Revision: run.Agent.Revision, Model: run.Agent.ModelIdentity, Status: run.Status, Usage: run.Usage, DurationMillis: run.DurationMilliseconds, ToolCalls: run.Metrics.ToolCalls}
			item.ConfigurationDigest = run.Agent.Digest
			if run.BackgroundTask != nil && run.BackgroundTask.DelegationID != "" {
				d, err := s.conversationDelegation(ctx, tx, run.BackgroundTask.DelegationID, a)
				if err != nil {
					return err
				}
				item.TaskType = d.Requirements.TaskType
				if !seen[d.ID] && (d.Status == "accepted_delivery" || d.Status == "needs_changes") {
					out.History = append(out.History, persistence.ConversationAgentObservation{ConfigurationDigest: item.ConfigurationDigest, AgentID: item.AgentID, Revision: item.Revision, Model: item.Model, TaskType: item.TaskType, Status: d.Status})
					seen[d.ID] = true
				}
			}
			out.History = append(out.History, item)
		}
		return nil
	})
	return out, err
}

var _ persistence.ConversationAgentDiscoveryRepository = (*ConversationStore)(nil)
