package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/connectortransport"
	connector "github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-connectors/providers/knowledge_base/http_api"
)

// KnowledgeConfig binds one Agent workspace to one remote team/knowledge base.
// PermissionIDs, when provided, must derive access from the authenticated
// authority. Browser input and model output must never supply permissions.
type KnowledgeConfig struct {
	// DocumentManagement grants this host port document writes for its fixed KB.
	// It is not derived from model input or enabled by retrieval configuration.
	DocumentManagement                         bool
	ResponseMapping                            *KnowledgeResponseMapping
	mappingInvalid                             bool
	BaseURL, APIKey, TeamID, KBID, WorkspaceID string
	TopK                                       int
	Client                                     *http.Client
	Transport                                  connector.Transport
	PermissionIDs                              func(context.Context, agentsdk.ConversationAuthority) ([]string, error)
	// AuthorizeWorkspace is supplied only by a trusted multi-Workspace host.
	// It must validate the current user/Workspace ownership on every request.
	// Without it, this connection remains restricted to WorkspaceID.
	AuthorizeWorkspace func(context.Context, agentsdk.ConversationAuthority) error
}

func KnowledgeConfigFromEnvironment() KnowledgeConfig {
	c := KnowledgeConfig{
		BaseURL: os.Getenv("AGENT_KNOWLEDGE_BASE_URL"), APIKey: os.Getenv("AGENT_KNOWLEDGE_API_KEY"),
		TeamID: os.Getenv("AGENT_KNOWLEDGE_TEAM_ID"), KBID: os.Getenv("AGENT_KNOWLEDGE_KB_ID"),
		WorkspaceID: os.Getenv("AGENT_KNOWLEDGE_WORKSPACE_ID"),
	}
	if strings.TrimSpace(c.APIKey) == "" {
		c.APIKey = os.Getenv("AGENT_PROVIDER_API_KEY")
	}
	if raw := strings.TrimSpace(os.Getenv("AGENT_KNOWLEDGE_TOP_K")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			value = -1 // Preserve invalid configuration for startup validation.
		}
		c.TopK = value
	}
	if raw := strings.TrimSpace(os.Getenv("AGENT_KNOWLEDGE_RESPONSE_MAPPING")); raw != "" {
		var err error
		c.ResponseMapping, err = knowledgeMappingJSON(raw)
		c.mappingInvalid = err != nil
	}
	return c
}

func (c KnowledgeConfig) Configured() bool {
	// A key alone does not enable retrieval.
	return strings.TrimSpace(c.BaseURL+c.TeamID+c.KBID+c.WorkspaceID) != "" || c.TopK != 0 || c.ResponseMapping != nil || c.mappingInvalid || c.DocumentManagement
}

type Knowledge struct {
	config  KnowledgeConfig
	adapter connector.Adapter
}

// NewKnowledge binds the shared official Provider to this host's transport.
// A nil result means retrieval is disabled, without a typed-nil interface.
func NewKnowledge(c KnowledgeConfig) (*Knowledge, error) {
	if !c.Configured() {
		return nil, nil
	}
	if c.mappingInvalid {
		return nil, fmt.Errorf("invalid knowledge response mapping JSON")
	}
	if err := validateKnowledgeMapping(c.ResponseMapping); err != nil {
		return nil, err
	}
	if c.ResponseMapping != nil {
		// Do not retain a caller-owned mutable policy pointer.
		raw, _ := json.Marshal(c.ResponseMapping)
		var copy KnowledgeResponseMapping
		_ = json.Unmarshal(raw, &copy)
		c.ResponseMapping = &copy
	}
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.APIKey, c.TeamID, c.KBID, c.WorkspaceID = strings.TrimSpace(c.APIKey), strings.TrimSpace(c.TeamID), strings.TrimSpace(c.KBID), strings.TrimSpace(c.WorkspaceID)
	if c.BaseURL == "" || c.APIKey == "" || c.TeamID == "" || c.KBID == "" || c.WorkspaceID == "" {
		return nil, fmt.Errorf("knowledge API origin, API key, team ID, knowledge base ID and Agent workspace ID are required")
	}
	if c.TopK == 0 {
		c.TopK = 5
	}
	if c.TopK < 1 || c.TopK > 20 {
		return nil, fmt.Errorf("knowledge top_k must be between 1 and 20")
	}
	if c.Transport == nil {
		var transport *connectortransport.HTTP
		var err error
		if c.DocumentManagement {
			transport, err = connectortransport.NewKnowledgeDocumentHTTP(c.BaseURL, c.KBID, c.Client)
		} else {
			transport, err = connectortransport.NewHTTP(c.BaseURL, c.Client)
		}
		if err != nil {
			return nil, err
		}
		c.Transport = transport
	}
	adapter, err := httpapi.New(c.Transport)
	if err != nil {
		return nil, err
	}
	k := &Knowledge{config: c, adapter: adapter}
	if err = adapter.(connector.ConfigValidator).ValidateConfig(k.connection()); err != nil {
		return nil, fmt.Errorf("invalid knowledge provider configuration")
	}
	return k, nil
}

func (k *Knowledge) connection() connector.Connection {
	return connector.Connection{Key: "agent_knowledge", WorkspaceID: k.config.WorkspaceID, ConnectorKey: httpapi.ConnectorKey, ProviderKey: httpapi.ProviderKey, Config: map[string]any{"base_url": k.config.BaseURL, "team_id": k.config.TeamID, "kb_id": k.config.KBID}}
}

func knowledgeFailure(code string) error {
	class := "unavailable"
	if code == "access_denied" {
		class = "forbidden"
	}
	if code == "not_found" {
		class = "not_found"
	}
	return &agentsdk.Error{Class: class, Code: "agent.conversation.knowledge_" + code}
}
func knowledgeConnectorError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if code, ok := connector.ProviderErrorCodeOf(err); ok && strings.HasPrefix(code, "knowledge_api.") {
		return knowledgeFailure(strings.TrimPrefix(code, "knowledge_api."))
	}
	return knowledgeFailure("failed")
}

func (k *Knowledge) Search(ctx context.Context, query string, authority agentsdk.ConversationAuthority) (json.RawMessage, error) {
	out, err := connector.Call(ctx, knowledgeGateway{k, authority}, httpapi.Search, httpapi.SearchInput{Query: query, TopK: k.config.TopK})
	if err != nil {
		return nil, knowledgeConnectorError(err)
	}
	return json.Marshal(out)
}

// Fetch exposes the same shared Connector operation with the same host policy.
func (k *Knowledge) Fetch(ctx context.Context, docID string, authority agentsdk.ConversationAuthority) (json.RawMessage, error) {
	out, err := connector.Call(ctx, knowledgeGateway{k, authority}, httpapi.Fetch, httpapi.FetchInput{DocID: docID})
	if err != nil {
		return nil, knowledgeConnectorError(err)
	}
	return json.Marshal(out)
}

type knowledgeGateway struct {
	knowledge *Knowledge
	authority agentsdk.ConversationAuthority
}

func (g knowledgeGateway) Call(ctx context.Context, request connector.CallRequest) (connector.CallResult, error) {
	k, a := g.knowledge, g.authority
	if !a.Known || strings.TrimSpace(a.RuntimeID) == "" || strings.TrimSpace(a.UserID) == "" || strings.TrimSpace(a.WorkspaceID) == "" {
		return connector.CallResult{}, connector.PermanentError("knowledge_api.access_denied", nil)
	}
	if k.config.AuthorizeWorkspace != nil {
		if err := k.config.AuthorizeWorkspace(ctx, a); err != nil {
			return connector.CallResult{}, connector.PermanentError("knowledge_api.access_denied", nil)
		}
	} else if a.WorkspaceID != k.config.WorkspaceID {
		return connector.CallResult{}, connector.PermanentError("knowledge_api.access_denied", nil)
	}
	request.Connection = k.connection()
	request.Connection.WorkspaceID = a.WorkspaceID
	request.Principal = connector.Principal{IsAuthenticated: true, UserID: a.UserID, WorkspaceID: a.WorkspaceID}
	request.Secrets = map[string]string{"api_key": k.config.APIKey}
	if k.config.PermissionIDs != nil {
		ids, err := k.config.PermissionIDs(ctx, a)
		if err != nil {
			return connector.CallResult{}, connector.PermanentError("knowledge_api.access_denied", nil)
		}
		request.Connection.Config["permission_ids_by_user"] = map[string][]string{a.UserID: ids}
	}
	return k.adapter.Call(ctx, request)
}
