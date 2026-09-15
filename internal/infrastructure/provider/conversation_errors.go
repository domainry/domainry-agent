package provider

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type conversationModelFailure struct {
	err     error
	details agentsdk.ConversationModelFailureDetails
}

func (f *conversationModelFailure) Error() string { return f.err.Error() }
func (f *conversationModelFailure) Unwrap() error { return f.err }
func (f *conversationModelFailure) ConversationModelFailureDetails() agentsdk.ConversationModelFailureDetails {
	details := f.details
	details.Usage = cloneConversationUsage(details.Usage)
	return details
}

func cloneConversationUsage(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func conversationFailureWithDetails(err error, details agentsdk.ConversationModelFailureDetails) error {
	return &conversationModelFailure{err: err, details: details}
}

// Stable, content-free error codes cross the provider/application boundary.
// Raw response bodies, endpoint URLs and credentials are never persisted.
func conversationHTTPError(status int, retryAfter time.Duration) error {
	code := "provider_failed"
	retryable := false
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		code = "provider_access_denied"
	case http.StatusPaymentRequired:
		code = "provider_quota_exhausted"
	case http.StatusTooManyRequests:
		code = "provider_rate_limited"
		retryable = true
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		code = "provider_timeout"
		retryable = true
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		code = "provider_request_invalid"
	default:
		if status >= 500 {
			code = "provider_unavailable"
			retryable = true
		}
	}
	err := &agentsdk.Error{Class: "unavailable", Code: "agent.conversation." + code, Retryable: retryable}
	return conversationFailureWithDetails(err, agentsdk.ConversationModelFailureDetails{Retryable: retryable, RetryAfter: retryAfter, ErrorCode: code})
}
func conversationNetworkError(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		coded := &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.provider_timeout", Retryable: true}
		return conversationFailureWithDetails(coded, agentsdk.ConversationModelFailureDetails{Retryable: true, ErrorCode: "provider_timeout"})
	}
	if errors.As(err, &network) || errors.Is(err, io.ErrUnexpectedEOF) {
		coded := &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.provider_network", Retryable: true}
		return conversationFailureWithDetails(coded, agentsdk.ConversationModelFailureDetails{Retryable: true, ErrorCode: "provider_network"})
	}
	return err
}

func parseConversationRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func withConversationFailureUsage(err error, usage map[string]any) error {
	if err == nil || len(usage) == 0 {
		return err
	}
	var provider agentsdk.ConversationModelFailureProvider
	if !errors.As(err, &provider) {
		return err
	}
	details := provider.ConversationModelFailureDetails()
	details.Usage = cloneConversationUsage(usage)
	return conversationFailureWithDetails(err, details)
}
