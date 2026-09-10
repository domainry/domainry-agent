package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationHTTPFailuresAreClassifiedWithoutProviderContent(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
	}{{401, "provider_access_denied"}, {403, "provider_access_denied"}, {402, "provider_quota_exhausted"}, {429, "provider_rate_limited"}, {400, "provider_request_invalid"}, {404, "provider_request_invalid"}, {500, "provider_unavailable"}, {504, "provider_timeout"}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"private-provider-response"}`))
			}))
			defer server.Close()
			model, err := NewConversationModel(ConversationModelConfig{URL: server.URL, Model: "test", APIKey: "private-provider-key"})
			if err != nil {
				t.Fatal(err)
			}
			for _, stream := range []bool{false, true} {
				input := agentsdk.ConversationModelRequest{Purpose: "reply", Messages: []agentsdk.ConversationModelMessage{{Role: "user", Content: "test"}}, MaxOutputBytes: 1024}
				if stream {
					_, err = model.StreamConversation(t.Context(), input, func(string) error { return nil })
				} else {
					_, err = model.GenerateConversation(t.Context(), input)
				}
				var failure *agentsdk.Error
				if !errors.As(err, &failure) || failure.Code != "agent.conversation."+tc.code {
					t.Fatalf("wrong category: %v", err)
				}
				if strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), server.URL) {
					t.Fatal("provider content leaked into error")
				}
			}
		})
	}
}
func TestConversationNetworkFailureCategories(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{{context.DeadlineExceeded, "provider_timeout"}, {&net.DNSError{Err: "private-host", IsTimeout: true}, "provider_timeout"}, {&net.DNSError{Err: "private-host"}, "provider_network"}} {
		var failure *agentsdk.Error
		if !errors.As(conversationNetworkError(tc.err), &failure) || failure.Code != "agent.conversation."+tc.code || strings.Contains(failure.Error(), "private-host") {
			t.Fatal("network error not safely classified")
		}
	}
	if !errors.Is(conversationNetworkError(context.Canceled), context.Canceled) {
		t.Fatal("cancellation identity lost")
	}
}
