package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	shareddefinition "github.com/domainry/domainry-foundation/definition"
)

type DefinitionStore struct{ store *Store }

func NewDefinitionStore(store *Store) DefinitionStore { return DefinitionStore{store: store} }

type definitionSeed struct {
	kind, key, name string
	payload         any
}

func definitionSeeds(snapshot agentpersistence.DefinitionSnapshot) []definitionSeed {
	values := make([]definitionSeed, 0, len(snapshot.Skills)+len(snapshot.Agents)+len(snapshot.Tasks)+len(snapshot.Entrypoints)+len(snapshot.Principals))
	for _, value := range snapshot.Skills {
		values = append(values, definitionSeed{"skill", strings.TrimSpace(value.Key), value.Name, value})
	}
	for _, value := range snapshot.Agents {
		values = append(values, definitionSeed{"agent", strings.TrimSpace(value.Key), value.Name, value})
	}
	for _, value := range snapshot.Tasks {
		values = append(values, definitionSeed{"agent_task", strings.TrimSpace(value.Key) + "@" + strings.TrimSpace(value.Version), value.Name, value})
	}
	for _, value := range snapshot.Entrypoints {
		values = append(values, definitionSeed{"agent_entrypoint", strings.TrimSpace(value.Key), value.Key, value})
	}
	for _, value := range snapshot.Principals {
		values = append(values, definitionSeed{"agent_service_principal", strings.TrimSpace(value.Key), value.Key, value})
	}
	return values
}

func (s DefinitionStore) SyncDefinitions(ctx context.Context, snapshot agentpersistence.DefinitionSnapshot) error {
	if s.store == nil || s.store.Database() == nil {
		return fmt.Errorf("Agent definition store is unavailable")
	}
	if strings.TrimSpace(snapshot.SchemaVersion) == "" || strings.TrimSpace(snapshot.SchemaHash) == "" || strings.TrimSpace(snapshot.SourceKind) == "" || strings.TrimSpace(snapshot.SourceID) == "" {
		return fmt.Errorf("Agent definition snapshot identity is required")
	}
	values := definitionSeeds(snapshot)
	definitions := make([]shareddefinition.Definition, 0, len(values))
	for _, seed := range values {
		if seed.key == "" {
			return fmt.Errorf("Agent %s definition key is required", seed.kind)
		}
		raw, err := json.Marshal(seed.payload)
		if err != nil {
			return err
		}
		definitions = append(definitions, shareddefinition.Definition{
			Owner: shareddefinition.OwnerAgent, ResourceType: seed.kind, ResourceKey: seed.key,
			Name: strings.TrimSpace(seed.name), Payload: raw,
		})
	}
	return s.store.definitions.ReplaceSourceSnapshot(ctx, shareddefinition.SourceSnapshot{
		Owner: shareddefinition.OwnerAgent, SchemaVersion: strings.TrimSpace(snapshot.SchemaVersion),
		SourceKind: strings.TrimSpace(snapshot.SourceKind), SourceID: strings.TrimSpace(snapshot.SourceID),
		Definitions: definitions,
	})
}

func (s DefinitionStore) DefinitionSnapshot(ctx context.Context) (agentpersistence.DefinitionSnapshot, error) {
	var result agentpersistence.DefinitionSnapshot
	if s.store == nil || s.store.Database() == nil {
		return result, fmt.Errorf("Agent definition store is unavailable")
	}
	definitions, err := s.store.definitions.List(ctx, shareddefinition.Query{Owner: shareddefinition.OwnerAgent})
	if err != nil {
		return result, err
	}
	for _, value := range definitions {
		if result.SchemaVersion == "" {
			result.SchemaVersion, result.SourceKind, result.SourceID = value.SchemaVersion, value.SourceKind, value.SourceID
		} else if result.SchemaVersion != value.SchemaVersion || result.SourceKind != value.SourceKind || result.SourceID != value.SourceID {
			return result, fmt.Errorf("Agent definition rows contain multiple source snapshots")
		}
		switch value.ResourceType {
		case "skill":
			var item agentsdk.SkillSchema
			if err := json.Unmarshal(value.Payload, &item); err != nil {
				return result, err
			}
			result.Skills = append(result.Skills, item)
		case "agent":
			var item agentsdk.AgentSchema
			if err := json.Unmarshal(value.Payload, &item); err != nil {
				return result, err
			}
			result.Agents = append(result.Agents, item)
		case "agent_task":
			var item agentsdk.AgentTaskDefinition
			if err := json.Unmarshal(value.Payload, &item); err != nil {
				return result, err
			}
			result.Tasks = append(result.Tasks, item)
		case "agent_entrypoint":
			var item agentsdk.AgentEntrypointAssignment
			if err := json.Unmarshal(value.Payload, &item); err != nil {
				return result, err
			}
			result.Entrypoints = append(result.Entrypoints, item)
		case "agent_service_principal":
			var item agentsdk.AgentServicePrincipalBinding
			if err := json.Unmarshal(value.Payload, &item); err != nil {
				return result, err
			}
			result.Principals = append(result.Principals, item)
		default:
			return result, fmt.Errorf("unsupported Agent definition kind %q", value.ResourceType)
		}
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

var _ agentpersistence.DefinitionRepository = DefinitionStore{}
