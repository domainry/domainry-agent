package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

const (
	ConversationProviderCompatible = "compatible"
	ConversationProviderGateway    = "gateway"
	ConversationProtocolChat       = "chat_completions"
	ConversationProtocolMessages   = "messages"
	ConversationProtocolResponses  = "responses"
)

// URL is a complete endpoint override. BaseURL supplies a configured service
// origin for protocol routes; no external service domain is built into code.
type ConversationModelConfig struct {
	Provider, Protocol string
	BaseURL            string
	URL, APIKey, Model string
	MaxOutputTokens    int
	Client             *http.Client
}

func ConversationModelConfigFromEnvironment() ConversationModelConfig {
	c := ConversationModelConfig{Provider: os.Getenv("AGENT_CONVERSATION_PROVIDER"), Protocol: os.Getenv("AGENT_CONVERSATION_PROTOCOL"), BaseURL: os.Getenv("AGENT_CONVERSATION_BASE_URL"), URL: os.Getenv("AGENT_CONVERSATION_MODEL_URL"), APIKey: os.Getenv("AGENT_CONVERSATION_MODEL_API_KEY"), Model: os.Getenv("AGENT_CONVERSATION_MODEL"), MaxOutputTokens: 4096}
	if strings.EqualFold(strings.TrimSpace(c.Provider), ConversationProviderGateway) && strings.TrimSpace(c.APIKey) == "" {
		c.APIKey = os.Getenv("AGENT_PROVIDER_API_KEY")
	}
	return c
}
func (c ConversationModelConfig) Configured() bool {
	return strings.TrimSpace(c.Provider+c.Protocol+c.BaseURL+c.URL+c.APIKey+c.Model) != ""
}

type ConversationModel struct{ config ConversationModelConfig }

func NewConversationModel(c ConversationModelConfig) (*ConversationModel, error) {
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	c.Protocol = strings.ToLower(strings.TrimSpace(c.Protocol))
	c.URL = strings.TrimSpace(c.URL)
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.Model = strings.TrimSpace(c.Model)
	c.APIKey = strings.TrimSpace(c.APIKey)
	if c.Provider == "" {
		c.Provider = ConversationProviderCompatible
	}
	if c.Protocol == "" {
		c.Protocol = ConversationProtocolChat
	}
	if c.Provider != ConversationProviderCompatible && c.Provider != ConversationProviderGateway {
		return nil, fmt.Errorf("unknown conversation provider")
	}
	routes := map[string]string{ConversationProtocolChat: "/v1/chat/completions", ConversationProtocolMessages: "/v1/messages", ConversationProtocolResponses: "/v1/responses"}
	route, ok := routes[c.Protocol]
	if !ok {
		return nil, fmt.Errorf("unknown conversation protocol")
	}
	if c.Provider == ConversationProviderGateway {
		if c.APIKey == "" {
			return nil, fmt.Errorf("Gateway API key is required")
		}
	}
	if c.URL == "" && c.BaseURL != "" {
		base, err := url.Parse(c.BaseURL)
		if err != nil || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || (base.Path != "" && base.Path != "/") || (base.Scheme != "https" && base.Scheme != "http") {
			return nil, fmt.Errorf("conversation model base URL must be a service origin")
		}
		c.URL = c.BaseURL + route
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || c.Model == "" {
		return nil, fmt.Errorf("conversation model URL and model name are required")
	}
	if c.MaxOutputTokens == 0 {
		c.MaxOutputTokens = 4096
	}
	if c.MaxOutputTokens < 1 {
		return nil, fmt.Errorf("invalid conversation output token limit")
	}
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 120 * time.Second}
	}
	// API keys must not follow redirects to another origin (x-api-key is not a
	// sensitive header in net/http's built-in redirect policy).
	client := *c.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.Client = &client
	return &ConversationModel{config: c}, nil
}
func (m *ConversationModel) request(ctx context.Context, in agentsdk.ConversationModelRequest, stream bool) (*http.Response, error) {
	if in.MaxOutputBytes < 1 || len(in.Messages) == 0 || (in.Purpose != "reply" && in.Purpose != "summary") {
		return nil, fmt.Errorf("invalid conversation model request")
	}
	for _, message := range in.Messages {
		if (message.Role != "system" && message.Role != "user" && message.Role != "assistant") || !validModelText(message.Content) {
			return nil, fmt.Errorf("invalid conversation message")
		}
	}
	payload := map[string]any{"model": m.config.Model, "stream": stream}
	switch m.config.Protocol {
	case ConversationProtocolChat:
		payload["messages"] = in.Messages
		tokenKey := "max_tokens"
		if m.config.Provider == ConversationProviderGateway {
			tokenKey = "max_completion_tokens"
		}
		payload[tokenKey] = m.config.MaxOutputTokens
		if stream {
			payload["stream_options"] = map[string]any{"include_usage": true}
		}
	case ConversationProtocolMessages:
		messages := []agentsdk.ConversationModelMessage{}
		system := []string{}
		for _, message := range in.Messages {
			if message.Role == "system" {
				if len(messages) > 0 {
					return nil, fmt.Errorf("Messages protocol requires leading system instructions")
				}
				system = append(system, message.Content)
			} else {
				messages = append(messages, message)
			}
		}
		if len(messages) == 0 {
			return nil, fmt.Errorf("Messages protocol requires conversation messages")
		}
		payload["messages"] = messages
		payload["max_tokens"] = m.config.MaxOutputTokens
		if len(system) > 0 {
			payload["system"] = strings.Join(system, "\n\n")
		}
	case ConversationProtocolResponses:
		payload["input"] = in.Messages
		payload["max_output_tokens"] = m.config.MaxOutputTokens
		payload["store"] = false
	}
	return m.sendConversationPayload(ctx, payload, in.IdempotencyKey, stream)
}

// Text and tool-capable requests share authentication, cancellation, redirect
// policy and transport error handling; their wire contracts remain separate.
func (m *ConversationModel) sendConversationPayload(ctx context.Context, payload map[string]any, idempotencyKey string, stream bool) (*http.Response, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.config.URL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if m.config.APIKey != "" {
		if m.config.Provider == ConversationProviderGateway || m.config.Protocol == ConversationProtocolMessages {
			req.Header.Set("x-api-key", m.config.APIKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+m.config.APIKey)
		}
	}
	if m.config.Protocol == ConversationProtocolMessages {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, err := m.config.Client.Do(req)
	if err != nil {
		return nil, conversationNetworkError(err)
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, conversationHTTPError(resp.StatusCode)
	}
	return resp, nil
}
func validModelText(s string) bool         { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }
func presentJSON(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }
func (m *ConversationModel) GenerateConversation(ctx context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	resp, err := m.request(ctx, in, false)
	if err != nil {
		return agentsdk.ConversationModelResult{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return agentsdk.ConversationModelResult{}, conversationNetworkError(err)
	}
	if len(raw) > maxResponseBytes || !utf8.Valid(raw) {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("invalid conversation response size or encoding")
	}
	var out agentsdk.ConversationModelResult
	switch m.config.Protocol {
	case ConversationProtocolMessages:
		out, err = decodeMessages(raw)
	case ConversationProtocolResponses:
		out, err = decodeResponse(raw)
	default:
		out, err = decodeChat(raw)
	}
	if err != nil {
		return agentsdk.ConversationModelResult{}, err
	}
	if !validModelText(out.Content) || strings.TrimSpace(out.Content) == "" || len(out.Content) > in.MaxOutputBytes {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("invalid conversation reply size or encoding (%d bytes, limit %d)", len(out.Content), in.MaxOutputBytes)
	}
	return out, nil
}

var _ agentsdk.ConversationModel = (*ConversationModel)(nil)
var _ agentsdk.ConversationStreamingModel = (*ConversationModel)(nil)
