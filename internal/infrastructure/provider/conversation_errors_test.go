package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestConversationRetryDetails(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	if got := parseConversationRetryAfter("7", now); got != 7*time.Second {
		t.Fatalf("seconds Retry-After = %v", got)
	}
	if got := parseConversationRetryAfter(now.Add(9*time.Second).Format(http.TimeFormat), now); got != 9*time.Second {
		t.Fatalf("date Retry-After = %v", got)
	}
	for _, status := range []int{408, 429, 500, 504} {
		var details agentsdk.ConversationModelFailureProvider
		if err := conversationHTTPError(status, 3*time.Second); !errors.As(err, &details) || !details.ConversationModelFailureDetails().Retryable || details.ConversationModelFailureDetails().RetryAfter != 3*time.Second {
			t.Fatalf("status %d was not retryable with Retry-After: %v", status, err)
		}
	}
	for _, status := range []int{400, 401, 402, 403, 404, 422} {
		var details agentsdk.ConversationModelFailureProvider
		if err := conversationHTTPError(status, 3*time.Second); !errors.As(err, &details) || details.ConversationModelFailureDetails().Retryable {
			t.Fatalf("status %d was retryable: %v", status, err)
		}
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

func TestConversationUnexpectedEOFIsRetryableNetworkFailure(t *testing.T) {
	err := conversationNetworkError(io.ErrUnexpectedEOF)
	var details agentsdk.ConversationModelFailureProvider
	if !errors.As(err, &details) || !details.ConversationModelFailureDetails().Retryable || details.ConversationModelFailureDetails().ErrorCode != "provider_network" {
		t.Fatalf("unexpected EOF was not retryable: %v", err)
	}
}
