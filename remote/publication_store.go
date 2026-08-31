package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	ormschema "github.com/domainry/domainry-orm/schema"
	"sync"
	"time"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	agentpersistence "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-orm/query"
)

const publicationTable = "_agent_task_publications"

type publicationStore struct {
	store    *agentstore.Store
	client   *client
	workerID string
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
}

type taskPublication struct {
	ID, Operation string
	Mutation      agentrepository.AgentTaskMutation
	Attempts      int
}

func publicationMigrations(renderer modulehost.Dialect) ([]modulehost.SchemaMigration, error) {
	statement, _, err := ormschema.NewTable(renderer, publicationTable).IfNotExists().Columns(
		ormschema.Column("id", ormschema.TextKey(255)).NotNull(),
		ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(),
		ormschema.Column("run_id", ormschema.TextKey(255)).NotNull(),
		ormschema.Column("operation", ormschema.TextKey(32)).NotNull(),
		ormschema.Column("payload_json", ormschema.LongText()).NotNull(),
		ormschema.Column("status", ormschema.TextKey(32)).NotNull(),
		ormschema.Column("attempts", ormschema.Integer()).NotNull(),
		ormschema.Column("next_attempt_at", ormschema.BigInt()).NotNull(),
		ormschema.Column("lease_owner", ormschema.TextKey(255)).NotNull(),
		ormschema.Column("lease_expires_at", ormschema.BigInt()).NotNull(),
		ormschema.Column("last_error", ormschema.Text()).NotNull(),
		ormschema.Column("created_at", ormschema.BigInt()).NotNull(),
		ormschema.Column("updated_at", ormschema.BigInt()).NotNull(),
	).PrimaryKey("id").Build()
	if err != nil {
		return nil, err
	}
	return []modulehost.SchemaMigration{{Version: 1, Name: "agent_task_publications", Statements: []string{statement}}}, nil
}

func newPublicationStore(host interface {
	Database() modulehost.Database
	Dialect() modulehost.Dialect
	Migrations() modulehost.MigrationRegistrar
}, client *client, workerID string) (*publicationStore, error) {
	store, err := agentpersistence.NewAgentStore(host.Database(), host.Dialect(), host.Migrations().Driver())
	if err != nil {
		return nil, err
	}
	return &publicationStore{store: store, client: client, workerID: workerID, done: make(chan struct{})}, nil
}

func publicationID(operation string, mutation agentrepository.AgentTaskMutation) string {
	raw, _ := json.Marshal(struct {
		Operation string
		Mutation  agentrepository.AgentTaskMutation
	}{operation, mutation})
	hash := sha256.Sum256(raw)
	return "agent-task-publication:" + hex.EncodeToString(hash[:])
}

func (s *publicationStore) enqueue(ctx context.Context, executor modulehost.Executor, operation string, mutation agentrepository.AgentTaskMutation) error {
	if s == nil || s.store == nil || executor == nil {
		return fmt.Errorf("Agent SaaS publication store is unavailable")
	}
	raw, err := json.Marshal(mutation)
	if err != nil {
		return err
	}
	now := time.Now().UTC().UnixMilli()
	insert := query.NewInsertBuilder(s.store.Renderer(), publicationTable).Columns("id", "workspace_id", "run_id", "operation", "payload_json", "status", "attempts", "next_attempt_at", "lease_owner", "lease_expires_at", "last_error", "created_at", "updated_at").Values(publicationID(operation, mutation), mutation.WorkspaceID, mutation.RunID, operation, raw, "pending", 0, now, "", 0, "", now, now)
	insert, err = s.store.Profile().ApplyUpsert(insert, []string{"id"},
		query.AssignExpression("payload_json", query.InsertedValue("payload_json")),
		query.AssignExpression("updated_at", query.InsertedValue("updated_at")))
	if err != nil {
		return err
	}
	statement, args, err := insert.Build()
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(ctx, statement, args...)
	return err
}

func (s *publicationStore) InsertAgentTask(ctx context.Context, executor modulehost.Executor, mutation agentrepository.AgentTaskMutation) error {
	return s.enqueue(ctx, executor, "insert", mutation)
}
func (s *publicationStore) UpdateAgentTask(ctx context.Context, executor modulehost.Executor, mutation agentrepository.AgentTaskMutation) error {
	return s.enqueue(ctx, executor, "update", mutation)
}

func (s *publicationStore) start(parent context.Context) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	s.cancel = cancel
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			_ = s.relay(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *publicationStore) close(ctx context.Context) error {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *publicationStore) relay(ctx context.Context) error {
	now := time.Now().UTC().UnixMilli()
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), publicationTable).Columns("id", "operation", "payload_json", "attempts").Where(query.Or(
		query.And(query.Equal("status", "pending"), query.LessThanOrEqual("next_attempt_at", now)),
		query.And(query.Equal("status", "processing"), query.LessThanOrEqual("lease_expires_at", now)),
	)).OrderBy(query.Ascending("created_at")).Limit(32).Build()
	if err != nil {
		return err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	values := []taskPublication{}
	for rows.Next() {
		var value taskPublication
		var raw []byte
		if err := rows.Scan(&value.ID, &value.Operation, &raw, &value.Attempts); err != nil {
			_ = rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &value.Mutation); err != nil {
			_ = rows.Close()
			return err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, value := range values {
		if err := s.deliver(ctx, value, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *publicationStore) deliver(ctx context.Context, value taskPublication, now int64) error {
	claim, args, err := query.NewUpdateBuilder(s.store.Renderer(), publicationTable).Set("status", "processing").Set("lease_owner", s.workerID).Set("lease_expires_at", now+30000).Set("updated_at", now).Where(query.And(query.Equal("id", value.ID), query.Or(query.Equal("status", "pending"), query.And(query.Equal("status", "processing"), query.LessThanOrEqual("lease_expires_at", now))))).Build()
	if err != nil {
		return err
	}
	result, err := s.store.Database().ExecContext(ctx, claim, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return err
	}
	operation := "task.mutation_insert"
	if value.Operation == "update" {
		operation = "task.mutation_update"
	}
	deliveryErr := taskRepository{client: s.client}.applyMutation(ctx, operation, value.Mutation)
	status, next, lastError := "delivered", int64(0), ""
	attempts := value.Attempts + 1
	if deliveryErr != nil {
		status = "pending"
		delay := time.Second * time.Duration(1<<min(attempts, 6))
		next = time.Now().Add(delay).UnixMilli()
		lastError = deliveryErr.Error()
	}
	finish, finishArgs, buildErr := query.NewUpdateBuilder(s.store.Renderer(), publicationTable).Set("status", status).Set("attempts", attempts).Set("next_attempt_at", next).Set("lease_owner", "").Set("lease_expires_at", 0).Set("last_error", lastError).Set("updated_at", time.Now().UnixMilli()).Where(query.And(query.Equal("id", value.ID), query.Equal("status", "processing"), query.Equal("lease_owner", s.workerID))).Build()
	if buildErr != nil {
		return buildErr
	}
	_, finishErr := s.store.Database().ExecContext(ctx, finish, finishArgs...)
	if finishErr != nil {
		return finishErr
	}
	return nil
}

var _ agentrepository.AgentTaskTransactionRepository = (*publicationStore)(nil)
