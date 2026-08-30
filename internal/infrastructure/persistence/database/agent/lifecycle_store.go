package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	agentstate "github.com/domainry/domainry-agent-sdk/state"
	ormbuilder "github.com/domainry/domainry-orm/builder"
)

type LifecycleStore struct{ store *Store }

func NewLifecycleStore(store *Store) LifecycleStore { return LifecycleStore{store: store} }

func (s LifecycleStore) ListLifecycleCandidates(ctx context.Context, workspaceID string, query agentrepository.LifecycleQuery) ([]agentrepository.LifecycleCandidate, error) {
	workspaceID, err := normalizeWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	type kindRetention struct {
		kind      string
		retention time.Duration
	}
	kinds := []kindRetention{}
	switch strings.TrimSpace(query.Owner) {
	case "agent":
		kinds = []kindRetention{{"session", query.Retention}, {"proposal", query.Retention}}
	case "report":
		switch strings.TrimSpace(query.PolicyKey) {
		case "report.download.v1":
			kinds = []kindRetention{{"report_download_task", query.Retention}}
		case "report.export.v1":
			retention := query.StatusRetention["succeeded"]
			if retention <= 0 {
				retention = query.Retention
			}
			kinds = []kindRetention{{"report_export_audit", retention}, {"report_query_run", retention}}
		}
	default:
		return nil, fmt.Errorf("unsupported Agent lifecycle owner %q", query.Owner)
	}
	result := []agentrepository.LifecycleCandidate{}
	for _, item := range kinds {
		builder := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_runtime_state", workspaceID).
			Columns("state_key", "user_id", "role_key", "payload_json", "updated_at").
			Where(ormbuilder.And(ormbuilder.Equal("kind", item.kind), ormbuilder.LessThanOrEqual("updated_at", query.Now.Add(-item.retention).UTC().UnixNano()))).
			OrderBy(ormbuilder.Ascending("updated_at"), ormbuilder.Ascending("state_key"))
		statement, args, buildErr := builder.Build()
		if buildErr != nil {
			return nil, buildErr
		}
		rows, queryErr := s.store.Database().QueryContext(ctx, statement, args...)
		if queryErr != nil {
			return nil, queryErr
		}
		for rows.Next() {
			value := agentstate.AgentStateRecord{WorkspaceID: workspaceID, Kind: item.kind}
			var payload []byte
			if scanErr := rows.Scan(&value.Key, &value.UserID, &value.RoleKey, &payload, &value.UpdatedAt); scanErr != nil {
				_ = rows.Close()
				return nil, scanErr
			}
			value.Payload = payload
			if query.Owner == "agent" && !agentLifecycleEligible(value.Kind, payload) {
				continue
			}
			result = append(result, agentrepository.LifecycleCandidate{State: value, ResourceID: value.Kind + ":" + value.Key})
			if query.Limit > 0 && len(result) >= query.Limit {
				_ = rows.Close()
				return result, nil
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return nil, rowsErr
		}
		_ = rows.Close()
	}
	return result, nil
}

func agentLifecycleEligible(kind string, payload []byte) bool {
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return false
	}
	switch kind {
	case "session":
		archived, _ := value["archived"].(bool)
		return archived
	case "proposal":
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(value["status"])))
		return status != "" && status != "draft" && status != "pending" && status != "open"
	default:
		return false
	}
}

func (s LifecycleStore) LifecycleCandidateReferenced(ctx context.Context, workspaceID string, candidate agentrepository.LifecycleCandidate) (bool, error) {
	childKind := ""
	switch candidate.State.Kind {
	case "report_export_audit":
		childKind = "report_download_task"
	case "report_query_run":
		childKind = "report_export_audit"
	}
	if childKind == "" {
		return false, nil
	}
	statement, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_runtime_state", workspaceID).
		Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.And(ormbuilder.Equal("kind", childKind), ormbuilder.Equal("state_key", candidate.State.Key))).Build()
	if err != nil {
		return false, err
	}
	var count int
	if err := s.store.Database().QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s LifecycleStore) DeleteLifecycleCandidate(ctx context.Context, workspaceID string, candidate agentrepository.LifecycleCandidate) (bool, error) {
	statement, args, err := ormbuilder.NewWorkspaceDeleteBuilder(s.store.Renderer(), "agent_runtime_state", workspaceID).
		Where(ormbuilder.And(ormbuilder.Equal("kind", candidate.State.Kind), ormbuilder.Equal("state_key", candidate.State.Key), ormbuilder.Equal("updated_at", candidate.State.UpdatedAt))).Build()
	if err != nil {
		return false, err
	}
	result, err := s.store.Database().ExecContext(ctx, statement, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

var _ agentrepository.AgentLifecycleRepository = LifecycleStore{}
