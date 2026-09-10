package provider

import (
	"context"
	"errors"
	"net"
	"net/http"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Stable, content-free error codes cross the provider/application boundary.
// Raw response bodies, endpoint URLs and credentials are never persisted.
func conversationHTTPError(status int) error {
	code := "provider_failed"
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		code = "provider_access_denied"
	case http.StatusPaymentRequired:
		code = "provider_quota_exhausted"
	case http.StatusTooManyRequests:
		code = "provider_rate_limited"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		code = "provider_timeout"
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		code = "provider_request_invalid"
	default:
		if status >= 500 {
			code = "provider_unavailable"
		}
	}
	return &agentsdk.Error{Class: "unavailable", Code: "agent.conversation." + code}
}
func conversationNetworkError(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		return &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.provider_timeout"}
	}
	if errors.As(err, &network) {
		return &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.provider_network"}
	}
	return err
}
