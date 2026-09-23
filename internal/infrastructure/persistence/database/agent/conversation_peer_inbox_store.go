package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
	"time"
)

func (s *ConversationStore) ConversationPeerInbox(ctx context.Context, conversationID string, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationAgentMessage, error) {
	if _, err := s.Get(ctx, conversationID, a); err != nil {
		return nil, err
	}
	out := []agentsdk.ConversationAgentMessage{}
	for offset := 0; len(out) < 8; offset += 64 {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("conversation_id", conversationID), query.Equal("consumed_run_id", ""))).OrderBy(query.Ascending("created_at"), query.Ascending("message_id")).Limit(64).Offset(offset).Build()
		if err != nil {
			return nil, err
		}
		rows, err := s.store.Database().QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		candidates := []agentsdk.ConversationAgentMessage{}
		for rows.Next() {
			var raw []byte
			var m agentsdk.ConversationAgentMessage
			if err = rows.Scan(&raw); err != nil {
				break
			}
			if err = json.Unmarshal(raw, &m); err != nil {
				break
			}
			candidates = append(candidates, m)
		}
		if e := rows.Err(); err == nil {
			err = e
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, m := range candidates {
			ready, err := s.peerMessageReady(ctx, s.store.Database(), m, a)
			if err != nil {
				return nil, err
			}
			if ready {
				out = append(out, m)
				if len(out) == 8 {
					break
				}
			}
		}
		if len(candidates) < 64 {
			break
		}
	}
	return out, nil
}

// Consumption is committed with the frozen model input, so a crash cannot
// lose a message between marking it read and making it recoverable by the run.
func (s *ConversationStore) consumeConversationPeerInbox(ctx context.Context, tx *sql.Tx, claim persistence.ConversationClaim, ids []string, step int) error {
	if len(ids) > 8 {
		return conversationError("bad_request", "agent_inbox_invalid")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return conversationError("bad_request", "agent_inbox_invalid")
		}
		seen[id] = true
		p := query.And(query.Equal("owner_key", conversationOwner(claim.Authority)), query.Equal("conversation_id", claim.Run.ConversationID), query.Equal("message_id", id), query.Equal("consumed_run_id", ""))
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json").Where(p).Build()
		if err != nil {
			return err
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			return conversationError("conflict", "agent_message_consumed")
		} else if err != nil {
			return err
		}
		var message agentsdk.ConversationAgentMessage
		if err = json.Unmarshal(raw, &message); err != nil {
			return err
		}
		if message.ParticipantUserID != "" {
			// Lock the exact grant row until input/consumption commits. Revocation
			// cannot complete between this check and freezing participant input.
			if err = s.lockConversationParticipantGrant(ctx, tx, message, claim.Authority); err != nil {
				return err
			}
		}
		ready, err := s.peerMessageReady(ctx, tx, message, claim.Authority)
		if err != nil {
			return err
		}
		if !ready || message.AfterRunID == claim.Run.ID {
			return conversationError("conflict", "agent_message_not_ready")
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		message.ConsumedAtStep = step
		message.ConsumedByRunID, message.ConsumedAt = claim.Run.ID, &now
		q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationAgentMessageTable).Set("consumed_run_id", claim.Run.ID).Set("payload_json", conversationJSON(message)).Where(p).Build()
		if err = conversationCAS(ctx, tx, q, args, err); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationStore) LaunchConversationPeerMessage(ctx context.Context, runtimeID string) (agentsdk.ConversationRun, bool, error) {
	return s.launchConversationPeerMessage(ctx, runtimeID, nil)
}

func (s *ConversationStore) LaunchConversationPeerMessageWithLifecycle(ctx context.Context, runtimeID string, lifecycle *agentsdk.ConversationLifecycleManifest) (agentsdk.ConversationRun, bool, error) {
	return s.launchConversationPeerMessage(ctx, runtimeID, lifecycle)
}

func (s *ConversationStore) launchConversationPeerMessage(ctx context.Context, runtimeID string, lifecycle *agentsdk.ConversationLifecycleManifest) (agentsdk.ConversationRun, bool, error) {
	var out agentsdk.ConversationRun
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("message_id").Where(query.And(query.Equal("runtime_id", runtimeID), query.Equal("consumed_run_id", ""))).Limit(1).Build()
	if err != nil {
		return out, false, err
	}
	var queued string
	if err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&queued); errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	} else if err != nil {
		return out, false, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		for offset := 0; ; offset += 64 {
			q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json", "authority_json", "agent_json").Where(query.And(query.Equal("runtime_id", runtimeID), query.Equal("consumed_run_id", ""))).OrderBy(query.Ascending("created_at"), query.Ascending("message_id")).Limit(64).Offset(offset).Build()
			if err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, q, args...)
			if err != nil {
				return err
			}
			type pending struct {
				message   agentsdk.ConversationAgentMessage
				authority agentsdk.ConversationAuthority
				agent     *agentsdk.ConversationAgentSnapshot
			}
			var candidates []pending
			for rows.Next() {
				var raw, authority, agent []byte
				var item pending
				if err = rows.Scan(&raw, &authority, &agent); err == nil {
					err = json.Unmarshal(raw, &item.message)
				}
				if err == nil {
					err = json.Unmarshal(authority, &item.authority)
				}
				if err == nil {
					err = json.Unmarshal(agent, &item.agent)
				}
				if err != nil {
					rows.Close()
					return err
				}
				candidates = append(candidates, item)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, item := range candidates {
				a, message := item.authority, item.message
				if item.agent != nil && item.agent.External != nil {
					continue
				}
				ready, err := s.peerMessageReady(ctx, tx, message, a)
				if err != nil {
					return err
				}
				if !ready {
					continue
				}
				if a.RuntimeID != runtimeID {
					return conversationError("forbidden", "runtime_denied")
				}
				d, err := s.conversationDelegation(ctx, tx, message.DelegationID, a)
				if err != nil {
					return err
				}
				recipient, err := s.delegationMessageAuthority(ctx, tx, d, message.ConversationID, a)
				if err != nil {
					return err
				}
				if conversationOwner(recipient) != conversationOwner(a) {
					return conversationError("forbidden", "execution_subject_mismatch")
				}
				a = recipient
				c, err := s.get(ctx, tx, message.ConversationID, a)
				if err != nil {
					return err
				}
				if c.Archived || c.ActiveRunID != "" {
					continue
				}
				q, args, err := query.NewSelectBuilder(s.store.Renderer(), agentRunTable).Projections(query.Project(query.CountAll())).Where(query.And(conversationRunScope(a, c.ID), query.Or(query.Equal("status", "queued"), query.Equal("status", "running"), query.Equal("status", "waiting_user"), query.Equal("status", "waiting_confirmation"), query.Equal("status", "needs_reconciliation")))).Build()
				if err != nil {
					return err
				}
				var active int
				if err = tx.QueryRowContext(ctx, q, args...).Scan(&active); err != nil {
					return err
				}
				if active > 0 {
					continue
				}
				if _, existsErr := s.runRow(ctx, tx, c.ID, "crun_"+conversationHash(message.ID)[:32], a); existsErr == nil {
					continue
				} else {
					var coded *agentsdk.Error
					if !errors.As(existsErr, &coded) || coded.Class != "not_found" {
						return existsErr
					}
				}
				if err = s.checkConversationQueueCapacity(ctx, tx, a); err != nil {
					return err
				}
				now := time.Now().UTC().Truncate(time.Millisecond)
				run := conversationRunRow{Authority: a, Run: agentsdk.ConversationRun{ID: "crun_" + conversationHash(message.ID)[:32], Agent: item.agent, Lifecycle: lifecycle, ConversationID: c.ID, ClientMessageID: "peer_" + message.ID, RequestHash: conversationHash(message.ID), Status: "queued", UserSeq: c.LastSeq + 1, CreatedAt: now, UpdatedAt: now}}
				trigger := agentsdk.ConversationMessage{ID: conversationID("msg_"), ConversationID: c.ID, RunID: run.Run.ID, Seq: run.Run.UserSeq, PeerEvent: &agentsdk.ConversationPeerEvent{MessageID: message.ID, DelegationID: message.DelegationID, FromAgentID: message.FromAgentID, ToAgentID: message.ToAgentID, Kind: message.Kind}, Role: "user", Content: "Continue the accepted work using the new collaboration messages. This is a server delivery event, not new user authorization.", CreatedAt: now}
				c.LastSeq, c.ActiveRunID = trigger.Seq, run.Run.ID
				if c.ID == d.ConversationID && (d.Status == "paused" || d.Status == "cancelled" || d.Status == "rejected" || d.Status == "accepted_delivery" || d.Status == "needs_update" || d.Status == "needs_changes") {
					continue
				}
				if c.ID == d.ConversationID {
					q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("task_id", d.TaskID))).Build()
					if err != nil {
						return err
					}
					taskRow, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
					if err != nil {
						return err
					}
					remaining, err := s.conversationDelegationRemainingBudget(ctx, tx, d, a)
					if err != nil {
						return err
					}
					if remaining.MaxSteps < 1 || remaining.MaxToolCalls < 1 {
						continue
					}
					task := taskRow.task
					if taskRow.authority != a {
						return conversationError("forbidden", "execution_subject_mismatch")
					}
					run.Run.Agent = task.Agent
					run.Run.Lifecycle = task.Lifecycle
					task.Brief, task.Budget = &d.Brief, remaining
					task.Goal, task.Input, task.Status, task.ExecutionRunID, task.UpdatedAt = d.Brief.Goal, d.Input, agentsdk.ConversationTaskStatusRunning, run.Run.ID, now
					task.CompletedAt, task.ResultMessageID, task.ErrorCode, task.CompletionEventID, task.CompletionEventSeq = nil, "", "", "", 0
					q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", task.Status).Set("updated_at", now.UnixMilli()).Set("payload_json", conversationJSON(task)).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("task_id", task.ID), query.Equal("status", taskRow.task.Status))).Build()
					if err = conversationCAS(ctx, tx, q, args, err); err != nil {
						return err
					}
					planVersion := int64(0)
					if task.Plan != nil {
						planVersion = task.Plan.Version
					}
					run.Run.WriteScope = &agentsdk.ConversationWriteScope{BackgroundTasks: true}
					run.Run.BackgroundTask = &agentsdk.ConversationTaskExecution{TaskID: task.ID, Model: task.Model, Lifecycle: task.Lifecycle, DelegationID: d.ID, BriefVersion: d.Brief.Version, AgreementRevision: max(1, task.AgreementRevision), PlanVersion: planVersion, Budget: remaining, ToolScope: task.ToolScope}
					run.Run.BackgroundTask.Handoff = task.Handoff
					run.Run.BackgroundTask.InputSource = task.InputSource
					run.Run.BackgroundTask.Requirements = task.Requirements
					run.Run.BackgroundTask.Dependencies = task.Dependencies
					trigger.BackgroundTaskID, c.ActiveRunID = task.ID, ""
					previous := d.Revision
					d.Status, d.Revision, d.UpdatedAt = "running", previous+1, now
					if err = s.saveConversationDelegation(ctx, tx, d, previous, a); err != nil {
						return err
					}
				}
				if err = s.save(ctx, tx, c, c.Revision, a); err != nil {
					return err
				}
				if err = s.insertMessage(ctx, tx, trigger, a); err != nil {
					return err
				}
				if err = s.event(ctx, tx, &run, "run.queued", map[string]any{"message_id": trigger.ID, "message_seq": trigger.Seq, "peer_message_id": message.ID}); err != nil {
					return err
				}
				q, args, err = query.NewInsertBuilder(s.store.Renderer(), agentRunTable).Columns("run_kind", "scope_key", "workspace_id", "run_id", "idempotency_key", "owner_key", "conversation_id", "runtime_id", "authority_json", "request_hash", "status", "lease_owner", "fencing_token", "lease_expires_at", "event_seq", "created_at", "updated_at", "payload_json").Values(agentRunKindConversation, conversationRunScopeKey(a, c.ID), conversationRunWorkspaceKey(a), run.Run.ID, run.Run.ClientMessageID, conversationOwner(a), c.ID, runtimeID, conversationJSON(a), run.Run.RequestHash, "queued", "", 0, 0, run.EventSeq, now.UnixMilli(), now.UnixMilli(), conversationJSON(run.Run)).Build()
				if err = conversationExec(ctx, tx, q, args, err); err != nil {
					return err
				}
				out = run.Run
				return nil
			}
			if len(candidates) < 64 {
				return nil
			}
		}
	})
	return out, out.ID != "", err
}

func (s *ConversationStore) conversationDelegationRemainingBudget(ctx context.Context, tx *sql.Tx, d agentsdk.ConversationDelegation, a agentsdk.ConversationAuthority) (agentsdk.ConversationTaskBudget, error) {
	remaining := d.Budget
	var err error
	a, err = s.delegationExecutionAuthority(ctx, tx, d, a)
	if err != nil {
		return remaining, err
	}
	assignments, err := s.conversationAssignments(ctx, tx, d, a)
	if err != nil {
		return remaining, err
	}
	for _, assignment := range assignments {
		executor, err := s.assignmentExecutionAuthority(ctx, tx, d, assignment, a)
		if err != nil {
			return remaining, err
		}
		for _, item := range []struct {
			kind  string
			value *int
		}{{conversationRunStepKindStep, &remaining.MaxSteps}, {conversationRunStepKindTool, &remaining.MaxToolCalls}} {
			q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationRunStepTable).Projections(query.Project(query.CountAll())).Where(query.And(conversationRunStepKindPredicate(item.kind), conversationScope(executor, assignment.ConversationID))).Build()
			if err != nil {
				return remaining, err
			}
			var used int
			if err = tx.QueryRowContext(ctx, q, args...).Scan(&used); err != nil {
				return remaining, err
			}
			*item.value = max(0, *item.value-used)
		}
	}
	return remaining, nil
}

var _ persistence.ConversationPeerLifecycleRepository = (*ConversationStore)(nil)
