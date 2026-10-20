package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewayRequestCarriesConversationIDHeader(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		received = request.Header.Get(conversationIDHeader)
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{}`)
	}))
	defer server.Close()

	model, err := NewConversationModel(ConversationModelConfig{
		Provider: ConversationProviderGateway,
		Protocol: ConversationProtocolChat,
		URL:      server.URL,
		APIKey:   "test-key",
		Model:    "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := model.sendConversationPayload(context.Background(), map[string]any{"model": "test-model"}, "request-1", " conversation-1 ", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if received != "conversation-1" {
		t.Fatalf("gateway conversation header = %q, want conversation-1", received)
	}
}

func TestCompatibleProviderDoesNotReceiveGatewayConversationHeader(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		received = request.Header.Get(conversationIDHeader)
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{}`)
	}))
	defer server.Close()

	model, err := NewConversationModel(ConversationModelConfig{
		Provider: ConversationProviderCompatible,
		Protocol: ConversationProtocolChat,
		URL:      server.URL,
		Model:    "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := model.sendConversationPayload(context.Background(), map[string]any{"model": "test-model"}, "request-1", "conversation-1", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if received != "" {
		t.Fatalf("compatible provider received gateway-only conversation header %q", received)
	}
}
