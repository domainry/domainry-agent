package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/base"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	shareddefinition "github.com/domainry/domainry-foundation/definition"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	ormdriver "github.com/domainry/domainry-orm/driver"
	"github.com/domainry/domainry-orm/query"
)

func agentError(class, code string) error {
	return &agentsdk.Error{Class: class, Code: code, Message: code, Retryable: class == "rate_limited"}
}

type Store struct {
	*base.SQLDatabase
	artifactStore   sharedartifact.ManagedStore
	artifactContent sharedartifact.ContentStore
	artifactWriter  sharedartifact.ContentWriter
	definitions     shareddefinition.Store
	operations      *sharedoperation.SQLStore
}

func NewStore(database modulehost.Database, renderer modulehost.Dialect, profile ormdriver.Profile, installationID string) (*Store, error) {
	if database == nil || renderer == nil || profile == nil || strings.TrimSpace(installationID) == "" {
		return nil, fmt.Errorf("Agent database, dialect, engine profile and installation identity are required")
	}
	definitionDialect, ok := renderer.(shareddefinition.Dialect)
	if !ok {
		return nil, fmt.Errorf("Agent database dialect does not support shared Definitions")
	}
	return &Store{
		SQLDatabase: base.NewSQLDatabase(database, renderer, profile),
		definitions: shareddefinition.NewStore(database, definitionDialect, installationID),
		operations:  sharedoperation.NewSQLStore(database, sharedoperation.AdaptDialect(renderer)),
	}, nil
}

func (s *Store) BindArtifactPersistence(store sharedartifact.ManagedStore, content sharedartifact.ContentStore, writer sharedartifact.ContentWriter) error {
	if store == nil || content == nil || writer == nil {
		return fmt.Errorf("shared Agent artifact persistence is incomplete")
	}
	s.artifactStore, s.artifactContent, s.artifactWriter = store, content, writer
	return nil
}

func (s *Store) ArtifactStore() sharedartifact.ManagedStore        { return s.artifactStore }
func (s *Store) ArtifactContentStore() sharedartifact.ContentStore { return s.artifactContent }
func (s *Store) ArtifactContentWriter() sharedartifact.ContentWriter {
	return s.artifactWriter
}

const agentTaskWorkerScopeRecoveryPolicy = "durable_due_scan"

func (s *Store) registerWorkerScope(ctx context.Context, executor modulehost.Executor, workspaceID, updatedAt string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return fmt.Errorf("Agent workspace is required")
	}
	digest := sha256.Sum256([]byte(agentTaskWorkerQueueKind + "\x00" + workspaceID))
	id := "worker_scope:" + hex.EncodeToString(digest[:12])
	insert := query.NewInsertBuilder(s.Renderer(), "_worker_scopes").Columns("id", "owner", "scope_key", "updated_at").Values(id, agentTaskWorkerQueueKind, workspaceID, updatedAt)
	insert, err := s.Profile().ApplyUpsert(insert, []string{"owner", "scope_key"}, query.AssignExpression("updated_at", query.InsertedValue("updated_at")))
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
	statement, args, err := query.NewSelectBuilder(s.Renderer(), "_worker_scopes").Columns("scope_key").Where(query.Equal("owner", agentTaskWorkerQueueKind)).OrderBy(query.Descending("updated_at")).Limit(limit).Build()
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
