package module

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

// A host-approved API-push source. Users select Key; all credentials, remote
// identities and parser mappings stay in host configuration.
type KnowledgeDatasourceConfig struct {
	Key, Name, Description string
	Knowledge              KnowledgeConfig
	PermissionIDs          []string
}
type knowledgeDatasourceEntry struct {
	config     KnowledgeDatasourceConfig
	definition agentsdk.KnowledgeDatasourceDefinition
}
type knowledgeDatasourceCatalog struct {
	runtimeID string
	entries   map[string]knowledgeDatasourceEntry
}

func knowledgeDatasourceEnvironment(raw string) ([]KnowledgeDatasourceConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if len(raw) > 256*1024 {
		return nil, fmt.Errorf("knowledge datasource configuration is too large")
	}
	var input []struct {
		Key             string                    `json:"key"`
		Name            string                    `json:"name"`
		Description     string                    `json:"description"`
		WorkspaceID     string                    `json:"workspace_id"`
		BaseURL         string                    `json:"base_url"`
		TeamID          string                    `json:"team_id"`
		KBID            string                    `json:"kb_id"`
		APIKeyEnv       string                    `json:"api_key_env"`
		TopK            int                       `json:"top_k"`
		ResponseMapping *KnowledgeResponseMapping `json:"response_mapping"`
		PermissionIDs   []string                  `json:"permission_ids"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || len(input) == 0 || len(input) > 1000 {
		return nil, fmt.Errorf("invalid knowledge datasource JSON")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("invalid trailing knowledge datasource JSON")
	}
	keys := regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	out := make([]KnowledgeDatasourceConfig, 0, len(input))
	for _, in := range input {
		if !keys.MatchString(in.APIKeyEnv) {
			return nil, fmt.Errorf("datasources require an API key environment variable name")
		}
		out = append(out, KnowledgeDatasourceConfig{Key: in.Key, Name: in.Name, Description: in.Description, PermissionIDs: in.PermissionIDs, Knowledge: KnowledgeConfig{WorkspaceID: in.WorkspaceID, BaseURL: in.BaseURL, TeamID: in.TeamID, KBID: in.KBID, APIKey: os.Getenv(in.APIKeyEnv), TopK: in.TopK, ResponseMapping: in.ResponseMapping}})
	}
	return out, nil
}
func assembleKnowledgeDatasources(options *ConversationOptions, configured []KnowledgeDatasourceConfig, raw, runtimeID string) error {
	if raw != "" {
		if len(configured) > 0 {
			return fmt.Errorf("configure knowledge datasources only once")
		}
		var err error
		configured, err = knowledgeDatasourceEnvironment(raw)
		if err != nil {
			return err
		}
	}
	if len(configured) == 0 {
		return nil
	}
	if options.KnowledgeDatasources != nil || len(configured) > 1000 {
		return fmt.Errorf("configure at most 1000 datasources using one catalog")
	}
	out := &knowledgeDatasourceCatalog{runtimeID: runtimeID, entries: map[string]knowledgeDatasourceEntry{}}
	seen := map[string]bool{}
	if source, ok := options.Knowledge.(agentsdk.ManagedKnowledgeDocumentSource); ok {
		seen[source.KnowledgeDocumentSourceIdentity()] = true
	}
	for _, binding := range options.LibraryKnowledge {
		if source, ok := binding.Source.(agentsdk.ManagedKnowledgeDocumentSource); ok {
			seen[source.KnowledgeDocumentSourceIdentity()] = true
		}
	}
	keyPattern := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,95}$`)
	for _, entry := range configured {
		if !keyPattern.MatchString(entry.Key) || strings.TrimSpace(entry.Name) == "" || len(entry.Name) > 128 || len(entry.Description) > 1024 || entry.Knowledge.PermissionIDs != nil || entry.Knowledge.AuthorizeWorkspace != nil || entry.Knowledge.DocumentPermissionIDs != nil {
			return fmt.Errorf("invalid fixed knowledge datasource configuration")
		}
		if _, exists := out.entries[entry.Key]; exists {
			return fmt.Errorf("duplicate knowledge datasource key")
		}
		entry.Knowledge.WorkspaceID = strings.TrimSpace(entry.Knowledge.WorkspaceID)
		entry.PermissionIDs = slices.Clone(entry.PermissionIDs)
		if entry.Knowledge.ResponseMapping != nil {
			raw, _ := json.Marshal(entry.Knowledge.ResponseMapping)
			var copy KnowledgeResponseMapping
			if json.Unmarshal(raw, &copy) != nil {
				return fmt.Errorf("invalid knowledge mapping")
			}
			entry.Knowledge.ResponseMapping = &copy
		}
		binding := KnowledgeLibraryConfig{LibraryID: "lib_00000000000000000000000000000000", Knowledge: entry.Knowledge, PermissionIDs: entry.PermissionIDs, ManageDocuments: true}
		if err := bindLibraryPermissions(&binding, runtimeID); err != nil {
			return err
		}
		binding.Knowledge.DocumentManagement = true
		source, err := provider.NewKnowledge(binding.Knowledge)
		if err != nil || source == nil || source.KnowledgeDocumentManagementReady() != nil {
			return fmt.Errorf("datasource requires a document provider and explicit response mappings")
		}
		id := source.KnowledgeDocumentSourceIdentity()
		if seen[id] {
			return fmt.Errorf("a remote KB cannot be shared by library, default or datasource catalog entries")
		}
		seen[id] = true
		out.entries[entry.Key] = knowledgeDatasourceEntry{config: entry, definition: agentsdk.KnowledgeDatasourceDefinition{Key: entry.Key, Name: entry.Name, Description: entry.Description, SourceID: id}}
	}
	options.KnowledgeDatasources = out
	return nil
}
func (c *knowledgeDatasourceCatalog) KnowledgeDatasources(ctx context.Context, scope agentsdk.KnowledgeDocumentStorageScope) ([]agentsdk.KnowledgeDatasourceDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scope.RuntimeID != c.runtimeID || scope.WorkspaceID == "" {
		return nil, fmt.Errorf("knowledge datasource scope mismatch")
	}
	out := []agentsdk.KnowledgeDatasourceDefinition{}
	for _, entry := range c.entries {
		if entry.config.Knowledge.WorkspaceID == scope.WorkspaceID {
			out = append(out, entry.definition)
		}
	}
	slices.SortFunc(out, func(a, b agentsdk.KnowledgeDatasourceDefinition) int { return strings.Compare(a.Key, b.Key) })
	return out, nil
}
func (c *knowledgeDatasourceCatalog) OpenKnowledgeDatasource(ctx context.Context, key string, scope agentsdk.KnowledgeDocumentStorageScope) (agentsdk.KnowledgeDatasourceSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry, ok := c.entries[key]
	if !ok || scope.RuntimeID != c.runtimeID || scope.WorkspaceID != entry.config.Knowledge.WorkspaceID || !regexp.MustCompile(`^lib_[a-f0-9]{32}$`).MatchString(scope.LibraryID) {
		return nil, fmt.Errorf("knowledge datasource unavailable")
	}
	binding := KnowledgeLibraryConfig{LibraryID: scope.LibraryID, Knowledge: entry.config.Knowledge, PermissionIDs: entry.config.PermissionIDs, ManageDocuments: true}
	if err := bindLibraryPermissions(&binding, c.runtimeID); err != nil {
		return nil, err
	}
	binding.Knowledge.DocumentManagement = true
	return provider.NewKnowledge(binding.Knowledge)
}
