package provider

import (
	"context"
	"encoding/json"
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

func conversationOutputTruncated(usage map[string]any) error {
	return conversationFailureWithDetails(
		&agentsdk.Error{Class: "unavailable", Code: "agent.conversation.model_output_truncated"},
		agentsdk.ConversationModelFailureDetails{ErrorCode: "model_output_truncated", Usage: cloneConversationUsage(usage)},
	)
}

// Stable, content-free error codes cross the provider/application boundary.
// Raw response bodies, endpoint URLs and credentials are never persisted.
func conversationHTTPError(status int, retryAfter time.Duration, responseBody ...[]byte) error {
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
		if len(responseBody) > 0 && conversationImageInputRejected(responseBody[0]) {
			code = "model_image_input_unsupported"
		}
	default:
		if status >= 500 {
			code = "provider_unavailable"
			retryable = true
		}
	}
	err := &agentsdk.Error{Class: "unavailable", Code: "agent.conversation." + code, Retryable: retryable}
	return conversationFailureWithDetails(err, agentsdk.ConversationModelFailureDetails{Retryable: retryable, RetryAfter: retryAfter, ErrorCode: code})
}

// conversationImageInputRejected intentionally recognizes only a small set of
// provider messages. The response body is never returned or persisted.
func conversationImageInputRejected(raw []byte) bool {
	if len(raw) == 0 || len(raw) > 8192 {
		return false
	}
	var envelope struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return false
	}
	encoded, err := json.Marshal(envelope.Error)
	if err != nil {
		return false
	}
	message := strings.ToLower(string(encoded))
	mentionsImage := strings.Contains(message, "image_url") || strings.Contains(message, "input_image") || strings.Contains(message, "image input")
	unsupported := strings.Contains(message, "unknown parameter") || strings.Contains(message, "not supported") || strings.Contains(message, "unsupported")
	return mentionsImage && unsupported
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

// A provider may return syntactically valid SSE transport but end one model
// step with malformed or inconsistent protocol events. Retrying is safe here:
// the model step has not completed and no business tool has been executed yet.
// Preserve callback/storage errors and already-classified provider failures;
// those have their own retry semantics.
func conversationProtocolError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	var classified agentsdk.ConversationModelFailureProvider
	if errors.As(err, &classified) {
		return err
	}
	coded := &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.provider_failed", Retryable: true}
	return conversationFailureWithDetails(coded, agentsdk.ConversationModelFailureDetails{Retryable: true, ErrorCode: "provider_failed"})
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
