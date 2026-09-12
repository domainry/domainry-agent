package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentinfra "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
)

func clearAgentEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"AGENT_KNOWLEDGE_BASE_URL", "AGENT_KNOWLEDGE_API_KEY", "AGENT_KNOWLEDGE_TEAM_ID", "AGENT_KNOWLEDGE_KB_ID", "AGENT_KNOWLEDGE_WORKSPACE_ID", "AGENT_KNOWLEDGE_TOP_K"} {
		t.Setenv(name, "")
	}
	for _, name := range []string{"AGENT_HTTP_BASE_URL", "AGENT_HTTP_API_KEY", "AGENT_HTTP_AGENT_ID", "AGENT_SAAS_API_KEY", "AGENT_SAAS_RUNTIME_ID", "AGENT_CONVERSATION_PROVIDER", "AGENT_CONVERSATION_PROTOCOL", "AGENT_CONVERSATION_BASE_URL", "AGENT_CONVERSATION_MODEL_URL", "AGENT_CONVERSATION_MODEL_API_KEY", "AGENT_CONVERSATION_MODEL", "AGENT_PROVIDER_API_KEY"} {
		t.Setenv(name, "")
	}
	for _, name := range []string{"IDENTITY_ENDPOINT", "IDENTITY_TENANT_ID", "IDENTITY_WORKSPACE_ID", "IDENTITY_ISSUER", "IDENTITY_AUDIENCE", "IDENTITY_SERVICE_ACCESS_TOKEN", "IDENTITY_CAPABILITY_CONTRACT_SHA256"} {
		t.Setenv(name, "")
	}
}
func TestConversationExecutableStartsWithoutLegacyProvider(t *testing.T) {
	clearAgentEnvironment(t)
	configureIdentityFixture(t)
	t.Setenv("AGENT_SAAS_API_KEY", "service-key")
	t.Setenv("AGENT_SAAS_RUNTIME_ID", "runtime")
	t.Setenv("AGENT_CONVERSATION_PROVIDER", "gateway")
	t.Setenv("AGENT_CONVERSATION_MODEL", "glm-5.3-flash-free")
	t.Setenv("AGENT_PROVIDER_API_KEY", "configured-key")
	t.Setenv("AGENT_CONVERSATION_BASE_URL", "https://models.example.com")
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = agentinfra.EnsureSchema(t.Context(), db, "sqlite", ""); err != nil {
		t.Fatal(err)
	}
	renderer, err := agentinfra.Renderer("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := agentinfra.NewAgentStore(db, renderer, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	service, closeService, err := openService(store)
	if err != nil {
		t.Fatal(err)
	}
	defer closeService()
	call := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer service-key")
		recorder := httptest.NewRecorder()
		service.Handler().ServeHTTP(recorder, request)
		return recorder
	}
	if got := call("/readyz"); got.Code != 200 {
		t.Fatalf("conversation-only readiness: %d %s", got.Code, got.Body)
	}
	got := call("/agent/v1/descriptor")
	var descriptor agentsdk.Descriptor
	if json.Unmarshal(got.Body.Bytes(), &descriptor) != nil || descriptor.Validate() != nil {
		t.Fatal("invalid descriptor")
	}
	if len(descriptor.Capabilities) != 2 || !descriptor.HasCapability(agentsdk.CapabilityConversationStreamV1) || descriptor.HasCapability(agentsdk.CapabilityTaskStart) {
		t.Fatalf("incorrect capabilities %+v", descriptor.Capabilities)
	}
	closeService()
	if got := call("/readyz"); got.Code != 503 {
		t.Fatalf("stopped worker reported ready: %d", got.Code)
	}
	// No model configuration allows managing stored conversations, but cannot
	// report ready to generate replies or require a legacy provider.
	for _, name := range []string{"AGENT_CONVERSATION_PROVIDER", "AGENT_CONVERSATION_MODEL", "AGENT_PROVIDER_API_KEY", "AGENT_CONVERSATION_BASE_URL"} {
		t.Setenv(name, "")
	}
	service, closeService, err = openService(store)
	if err != nil {
		t.Fatal(err)
	}
	defer closeService()
	if got := call("/readyz"); got.Code != 503 {
		t.Fatalf("missing model reported ready: %d", got.Code)
	}
	closeService()
	t.Setenv("AGENT_CONVERSATION_PROVIDER", "gateway")
	t.Setenv("AGENT_CONVERSATION_MODEL", "glm-5.3-flash-free")
	t.Setenv("AGENT_PROVIDER_API_KEY", "configured-key")
	t.Setenv("AGENT_CONVERSATION_BASE_URL", "https://models.example.com")
	service, closeService, err = openService(store)
	if err != nil {
		t.Fatal(err)
	}
	defer closeService()
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if got := call("/readyz"); got.Code != 503 {
		t.Fatalf("closed persistence reported ready: %d", got.Code)
	}

}
func TestExecutableRejectsPartialOrMissingProviderConfiguration(t *testing.T) {
	clearAgentEnvironment(t)
	t.Setenv("AGENT_SAAS_API_KEY", "service-key")
	if _, _, err := openService(nil); err == nil {
		t.Fatal("empty configuration accepted")
	}
	t.Setenv("AGENT_HTTP_API_KEY", "key")
	if _, _, err := openService(nil); err == nil {
		t.Fatal("partial legacy configuration accepted")
	}
	t.Setenv("AGENT_HTTP_API_KEY", "")
	t.Setenv("AGENT_CONVERSATION_PROVIDER", "gateway")
	if _, _, err := openService(nil); err == nil {
		t.Fatal("conversation without runtime accepted")
	}
	t.Setenv("AGENT_SAAS_RUNTIME_ID", "runtime")
	if _, _, err := openService(nil); err == nil {
		t.Fatal("Gateway without model or API key accepted")
	}
}
