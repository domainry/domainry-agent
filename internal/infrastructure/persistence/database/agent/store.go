package agent

import (
	"context"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/base"
	ormdriver "github.com/domainry/domainry-orm/driver"
	"github.com/domainry/domainry-orm/query"
)

func agentError(class, code string) error {
	return &agentsdk.Error{Class: class, Code: code, Message: code, Retryable: class == "rate_limited"}
}

type Store struct {
	*base.SQLDatabase
}

func NewStore(database modulehost.Database, renderer modulehost.Dialect, profile ormdriver.Profile) (*Store, error) {
	if database == nil || renderer == nil || profile == nil {
		return nil, fmt.Errorf("Agent database, dialect and engine profile are required")
	}
	return &Store{SQLDatabase: base.NewSQLDatabase(database, renderer, profile)}, nil
}

func (s *Store) registerWorkerScope(ctx context.Context, executor modulehost.Executor, workspaceID string, updatedAt int64) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return fmt.Errorf("Agent workspace is required")
	}
	insert := query.NewInsertBuilder(s.Renderer(), "_agent_worker_scopes").Columns("workspace_id", "updated_at").Values(workspaceID, updatedAt)
	insert, err := s.Profile().ApplyUpsert(insert, []string{"workspace_id"}, query.AssignExpression("updated_at", query.InsertedValue("updated_at")))
	if err != nil {
		return err
	}
	statement, args, err := insert.Build()
	if err != nil {
		return err
	}
	if executor == nil {
		executor = s.Database()
	}
	_, err = executor.ExecContext(ctx, statement, args...)
	return err
}

func (s *Store) workerScopePage(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 64
	}
	statement, args, err := query.NewSelectBuilder(s.Renderer(), "_agent_worker_scopes").Columns("workspace_id").OrderBy(query.Descending("updated_at")).Limit(limit).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			return nil, err
		}
		values = append(values, workspaceID)
	}
	return values, rows.Err()
}
