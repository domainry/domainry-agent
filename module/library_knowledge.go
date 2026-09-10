package module

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

// LibraryKnowledgeBinding lets a trusted host supply its own isolated source
// through ConversationOptions without importing Agent's internal application.
type LibraryKnowledgeBinding = application.LibraryKnowledgeBinding

// KnowledgeLibraryConfig is startup-only host configuration. A remote KB must
// be dedicated to one local library; remote team-visible documents would defeat
// membership isolation if a default source or another library shared that KB.
type KnowledgeLibraryConfig struct {
	LibraryID       string
	Knowledge       KnowledgeConfig
	ManageDocuments bool
}

func knowledgeLibraryEnvironment(raw string) ([]KnowledgeLibraryConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if len(raw) > 256*1024 {
		return nil, fmt.Errorf("knowledge library binding configuration is too large")
	}
	var input []struct {
		LibraryID       string                    `json:"library_id"`
		WorkspaceID     string                    `json:"workspace_id"`
		BaseURL         string                    `json:"base_url"`
		TeamID          string                    `json:"team_id"`
		KBID            string                    `json:"kb_id"`
		APIKeyEnv       string                    `json:"api_key_env"`
		TopK            int                       `json:"top_k"`
		ResponseMapping *KnowledgeResponseMapping `json:"response_mapping"`
		ManageDocuments bool                      `json:"manage_documents"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || len(input) == 0 || len(input) > 1000 {
		return nil, fmt.Errorf("invalid knowledge library binding JSON")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("invalid trailing knowledge library binding JSON")
	}
	out := make([]KnowledgeLibraryConfig, 0, len(input))
	envName := regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	for _, in := range input {
		if !envName.MatchString(in.APIKeyEnv) {
			return nil, fmt.Errorf("each knowledge library requires an API key environment variable name")
		}
		out = append(out, KnowledgeLibraryConfig{LibraryID: in.LibraryID, ManageDocuments: in.ManageDocuments, Knowledge: KnowledgeConfig{BaseURL: in.BaseURL, TeamID: in.TeamID, KBID: in.KBID, WorkspaceID: in.WorkspaceID, APIKey: os.Getenv(in.APIKeyEnv), TopK: in.TopK, ResponseMapping: in.ResponseMapping}})
	}
	return out, nil
}

func libraryRemoteIdentity(c KnowledgeConfig) string {
	raw := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if parsed, err := url.Parse(raw); err == nil {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		if parsed.Scheme == "https" && parsed.Port() == "443" {
			parsed.Host = parsed.Hostname()
			if strings.Contains(parsed.Host, ":") {
				parsed.Host = "[" + parsed.Host + "]"
			}
		}
		if parsed.Scheme == "http" && parsed.Port() == "80" {
			parsed.Host = parsed.Hostname()
			if strings.Contains(parsed.Host, ":") {
				parsed.Host = "[" + parsed.Host + "]"
			}
		}
		raw = parsed.String()
	}
	key, _ := json.Marshal([]string{raw, strings.TrimSpace(c.TeamID), strings.TrimSpace(c.KBID)})
	return string(key)
}

func assembleLibraryKnowledge(options *ConversationOptions, configured []KnowledgeLibraryConfig, raw string, legacy KnowledgeConfig) error {
	if raw != "" {
		if len(configured) > 0 {
			return fmt.Errorf("configure knowledge library bindings only once")
		}
		var err error
		configured, err = knowledgeLibraryEnvironment(raw)
		if err != nil {
			return err
		}
	}
	if len(configured) == 0 {
		return nil
	}
	if len(configured) > 1000 || len(options.LibraryKnowledge) > 0 {
		return fmt.Errorf("configure at most 1000 library sources using only one binding mechanism")
	}
	seen := map[string]bool{}
	if legacy.Configured() {
		seen[libraryRemoteIdentity(legacy)] = true
	}
	for _, binding := range configured {
		if binding.Knowledge.AuthorizeWorkspace != nil {
			return fmt.Errorf("a library knowledge source must bind exactly one workspace")
		}
		identity := libraryRemoteIdentity(binding.Knowledge)
		if seen[identity] {
			return fmt.Errorf("a remote knowledge base cannot be shared by different library or default bindings")
		}
		seen[identity] = true
		binding.Knowledge.DocumentManagement = binding.ManageDocuments
		source, err := provider.NewKnowledge(binding.Knowledge)
		if err != nil || source == nil {
			return fmt.Errorf("invalid library knowledge provider configuration")
		}
		options.LibraryKnowledge = append(options.LibraryKnowledge, application.LibraryKnowledgeBinding{LibraryID: binding.LibraryID, WorkspaceID: strings.TrimSpace(binding.Knowledge.WorkspaceID), Source: source, ManageDocuments: binding.ManageDocuments})
	}
	return nil
}
