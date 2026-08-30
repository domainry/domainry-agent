package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	ormbuilder "github.com/domainry/domainry-orm/query"
)

type DefinitionStore struct{ store *Store }

var definitionTables = []string{
	"_agent_skill_definitions",
	"_agent_definitions",
	"_agent_task_definitions",
	"_agent_entrypoint_definitions",
	"_agent_service_principal_definitions",
}

func NewDefinitionStore(store *Store) DefinitionStore { return DefinitionStore{store: store} }

type definitionSeed struct {
	table, kind, key, name string
	payload                any
}

func definitionSeeds(snapshot agentrepository.DefinitionSnapshot) []definitionSeed {
	values := make([]definitionSeed, 0, len(snapshot.Skills)+len(snapshot.Agents)+len(snapshot.Tasks)+len(snapshot.Entrypoints)+len(snapshot.Principals))
	for _, value := range snapshot.Skills {
		values = append(values, definitionSeed{"_agent_skill_definitions", "skill", strings.TrimSpace(value.Key), value.Name, value})
	}
	for _, value := range snapshot.Agents {
		values = append(values, definitionSeed{"_agent_definitions", "agent", strings.TrimSpace(value.Key), value.Name, value})
	}
	for _, value := range snapshot.Tasks {
		values = append(values, definitionSeed{"_agent_task_definitions", "agent_task", strings.TrimSpace(value.Key) + "@" + strings.TrimSpace(value.Version), value.Name, value})
	}
	for _, value := range snapshot.Entrypoints {
		values = append(values, definitionSeed{"_agent_entrypoint_definitions", "agent_entrypoint", strings.TrimSpace(value.Key), value.Key, value})
	}
	for _, value := range snapshot.Principals {
		values = append(values, definitionSeed{"_agent_service_principal_definitions", "agent_service_principal", strings.TrimSpace(value.Key), value.Key, value})
	}
	return values
}

func (s DefinitionStore) SyncDefinitions(ctx context.Context, snapshot agentrepository.DefinitionSnapshot) error {
	if s.store == nil || s.store.database == nil {
		return fmt.Errorf("Agent definition store is unavailable")
	}
	if strings.TrimSpace(snapshot.SchemaVersion) == "" || strings.TrimSpace(snapshot.SchemaHash) == "" || strings.TrimSpace(snapshot.SourceKind) == "" || strings.TrimSpace(snapshot.SourceID) == "" {
		return fmt.Errorf("Agent definition snapshot identity is required")
	}
	tx, err := s.store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, table := range definitionTables {
		statement, args, err := ormbuilder.NewUpdateBuilder(s.store.renderer, table).Set("disabled_at", now).Where(ormbuilder.IsNotNull("resource_key")).Build()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return err
		}
	}
	for _, seed := range definitionSeeds(snapshot) {
		if seed.key == "" {
			return fmt.Errorf("Agent %s definition key is required", seed.kind)
		}
		raw, err := json.Marshal(seed.payload)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(raw)
		columns := []string{"id", "resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at"}
		insert := ormbuilder.NewInsertBuilder(s.store.renderer, seed.table).Columns(columns...).Values(seed.kind+":"+seed.key, seed.key, "", seed.name, raw, snapshot.SchemaVersion, hex.EncodeToString(hash[:]), snapshot.SourceKind, snapshot.SourceID, nil, now, now)
		insert, err = s.store.profile.ApplyUpsert(insert, []string{"resource_key"}, ormbuilder.AssignExpression("name", ormbuilder.InsertedValue("name")), ormbuilder.AssignExpression("payload_json", ormbuilder.InsertedValue("payload_json")), ormbuilder.AssignExpression("schema_version", ormbuilder.InsertedValue("schema_version")), ormbuilder.AssignExpression("schema_hash", ormbuilder.InsertedValue("schema_hash")), ormbuilder.AssignExpression("source_kind", ormbuilder.InsertedValue("source_kind")), ormbuilder.AssignExpression("source_id", ormbuilder.InsertedValue("source_id")), ormbuilder.AssignExpression("disabled_at", ormbuilder.InsertedValue("disabled_at")), ormbuilder.AssignExpression("updated_at", ormbuilder.InsertedValue("updated_at")))
		if err != nil {
			return err
		}
		statement, args, err := insert.Build()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func loadDefinitions[T any](ctx context.Context, store *Store, table string) ([]T, string, string, string, error) {
	statement, args, err := ormbuilder.NewSelectBuilder(store.renderer, table).Columns("payload_json", "schema_version", "source_kind", "source_id").Where(ormbuilder.IsNull("disabled_at")).OrderBy(ormbuilder.Ascending("resource_key")).Build()
	if err != nil {
		return nil, "", "", "", err
	}
	rows, err := store.database.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, "", "", "", err
	}
	defer rows.Close()
	values := []T{}
	var version, sourceKind, sourceID string
	for rows.Next() {
		var raw []byte
		var rowVersion, rowSourceKind, rowSourceID string
		if err := rows.Scan(&raw, &rowVersion, &rowSourceKind, &rowSourceID); err != nil {
			return nil, "", "", "", err
		}
		var value T
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, "", "", "", err
		}
		values = append(values, value)
		if version == "" {
			version, sourceKind, sourceID = rowVersion, rowSourceKind, rowSourceID
		}
	}
	return values, version, sourceKind, sourceID, rows.Err()
}

func (s DefinitionStore) DefinitionSnapshot(ctx context.Context) (agentrepository.DefinitionSnapshot, error) {
	var result agentrepository.DefinitionSnapshot
	var err error
	result.Skills, result.SchemaVersion, result.SourceKind, result.SourceID, err = loadDefinitions[agentsdk.SkillSchema](ctx, s.store, "_agent_skill_definitions")
	if err != nil {
		return result, err
	}
	result.Agents, _, _, _, err = loadDefinitions[agentsdk.AgentSchema](ctx, s.store, "_agent_definitions")
	if err != nil {
		return result, err
	}
	result.Tasks, _, _, _, err = loadDefinitions[agentsdk.AgentTaskDefinition](ctx, s.store, "_agent_task_definitions")
	if err != nil {
		return result, err
	}
	result.Entrypoints, _, _, _, err = loadDefinitions[agentsdk.AgentEntrypointAssignment](ctx, s.store, "_agent_entrypoint_definitions")
	if err != nil {
		return result, err
	}
	result.Principals, _, _, _, err = loadDefinitions[agentsdk.AgentServicePrincipalBinding](ctx, s.store, "_agent_service_principal_definitions")
	if err != nil {
		return result, err
	}
	raw, _ := json.Marshal(struct {
		Skills      []agentsdk.SkillSchema
		Agents      []agentsdk.AgentSchema
		Tasks       []agentsdk.AgentTaskDefinition
		Entrypoints []agentsdk.AgentEntrypointAssignment
		Principals  []agentsdk.AgentServicePrincipalBinding
	}{result.Skills, result.Agents, result.Tasks, result.Entrypoints, result.Principals})
	hash := sha256.Sum256(raw)
	result.SchemaHash = hex.EncodeToString(hash[:])
	return result, nil
}

var _ agentrepository.DefinitionRepository = DefinitionStore{}
