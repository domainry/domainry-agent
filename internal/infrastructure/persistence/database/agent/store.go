package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/base"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	shareddefinition "github.com/domainry/domainry-foundation/definition"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	ormdriver "github.com/domainry/domainry-orm/driver"
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
	workerScopes    *sharedworkerscope.Store
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
		SQLDatabase:  base.NewSQLDatabase(database, renderer, profile),
		definitions:  shareddefinition.NewStore(database, definitionDialect, installationID),
		operations:   sharedoperation.NewSQLStore(database, sharedoperation.AdaptDialect(renderer)),
		workerScopes: sharedworkerscope.NewStore(database, renderer),
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

const (
	agentTaskWorkerQueueKind           = sharedworkerscope.OwnerAgentTask
	agentTaskWorkerScopeRecoveryPolicy = "durable_due_scan"
)

func (s *Store) registerWorkerScope(ctx context.Context, executor modulehost.Executor, workspaceID string, updatedAt time.Time) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return fmt.Errorf("Agent workspace is required")
	}
	if executor == nil {
		executor = s.Database()
	}
	if updatedAt.IsZero() {
		return fmt.Errorf("Agent worker scope timestamp is invalid")
	}
	return s.workerScopes.Register(ctx, executor, sharedworkerscope.NewIdentity(agentTaskWorkerQueueKind, workspaceID), updatedAt.UTC())
}

func (s *Store) workerScopePage(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 64
	}
	return s.workerScopes.ScopeKeys(ctx, nil, sharedworkerscope.ScopeQuery{Owner: agentTaskWorkerQueueKind, Order: sharedworkerscope.UpdatedAtDescending, Limit: limit})
}
