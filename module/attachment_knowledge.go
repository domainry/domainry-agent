package module

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

func attachmentKnowledgeEnvironment(raw string) ([]KnowledgeConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if len(raw) > 256*1024 {
		return nil, fmt.Errorf("attachment knowledge configuration is too large")
	}
	var input []struct {
		WorkspaceID     string                    `json:"workspace_id"`
		BaseURL         string                    `json:"base_url"`
		TeamID          string                    `json:"team_id"`
		KBID            string                    `json:"kb_id"`
		APIKeyEnv       string                    `json:"api_key_env"`
		TopK            int                       `json:"top_k"`
		ResponseMapping *KnowledgeResponseMapping `json:"response_mapping"`
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || len(input) == 0 || len(input) > 1000 {
		return nil, fmt.Errorf("invalid attachment knowledge binding JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("invalid trailing attachment knowledge binding JSON")
	}
	keys := regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	out := make([]KnowledgeConfig, 0, len(input))
	for _, in := range input {
		if !keys.MatchString(in.APIKeyEnv) {
			return nil, fmt.Errorf("attachment sources require an API key environment variable name")
		}
		out = append(out, KnowledgeConfig{BaseURL: in.BaseURL, TeamID: in.TeamID, KBID: in.KBID, WorkspaceID: in.WorkspaceID, APIKey: os.Getenv(in.APIKeyEnv), TopK: in.TopK, ResponseMapping: in.ResponseMapping})
	}
	return out, nil
}

func assembleAttachmentKnowledge(options *ConversationOptions, configured []KnowledgeConfig, raw, runtime string) error {
	if raw != "" {
		if len(configured) > 0 {
			return fmt.Errorf("configure attachment knowledge only once")
		}
		var err error
		configured, err = attachmentKnowledgeEnvironment(raw)
		if err != nil {
			return err
		}
	}
	if len(configured) > 1000 || len(configured) > 0 && len(options.AttachmentKnowledge) > 0 {
		return fmt.Errorf("configure attachment sources using one binding mechanism")
	}
	for _, c := range configured {
		source, err := provider.NewAttachmentKnowledge(c, runtime)
		if err != nil {
			return err
		}
		options.AttachmentKnowledge = append(options.AttachmentKnowledge, agentsdk.ConversationAttachmentKnowledgeBinding{WorkspaceID: strings.TrimSpace(c.WorkspaceID), Knowledge: source})
	}
	used := map[string]bool{}
	if source, ok := options.Knowledge.(agentsdk.ManagedKnowledgeDocumentSource); ok {
		used[source.KnowledgeDocumentSourceIdentity()] = true
	}
	for _, b := range options.LibraryKnowledge {
		if source, ok := b.Source.(agentsdk.ManagedKnowledgeDocumentSource); ok {
			used[source.KnowledgeDocumentSourceIdentity()] = true
		}
	}
	if catalog, ok := options.KnowledgeDatasources.(*knowledgeDatasourceCatalog); ok {
		for _, entry := range catalog.entries {
			used[entry.definition.SourceID] = true
		}
	}
	workspaces := map[string]bool{}
	for _, b := range options.AttachmentKnowledge {
		if b.Knowledge == nil || strings.TrimSpace(b.WorkspaceID) == "" || workspaces[b.WorkspaceID] {
			return fmt.Errorf("each workspace requires a unique attachment knowledge binding")
		}
		id := b.Knowledge.AttachmentKnowledgeSourceIdentity()
		if used[id] {
			return fmt.Errorf("attachment KB cannot be reused by a default, library, catalog or another attachment binding")
		}
		workspaces[b.WorkspaceID], used[id] = true, true
	}
	return nil
}
