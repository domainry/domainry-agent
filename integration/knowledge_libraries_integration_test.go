package integration_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/domainry/domainry-agent/internal/application"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	agentserver "github.com/domainry/domainry-agent/server"
)

func TestAgentSaaSDoesNotRepublishKnowledgeLibraryContract(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	service, err := conversationassembly.NewService(repo, nil, a.RuntimeID, application.ConversationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server, err := agentserver.New(agentserver.Config{APIKey: "library-saas-fixture", Conversations: service, ConversationRuntimeID: a.RuntimeID})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(server.Handler())
	defer upstream.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, upstream.URL+"/agent/v1/conversations/libraries_create", strings.NewReader(`{"authority":{"runtime_id":"runtime"}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer library-saas-fixture")
	request.Header.Set("Content-Type", "application/json")
	response, err := upstream.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("Agent republished Knowledge-owned library route: status=%d", response.StatusCode)
	}
}
