package provider

import (
	"bufio"
	"context"
	"fmt"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"io"
	"mime"
	"strings"
	"unicode/utf8"
)

// Only a protocol-specific terminal event completes a stream. EOF, including
// an unterminated final SSE frame, is a failure. No second request is issued.
func readConversationSSE(ctx context.Context, r io.Reader, consume func(string, []byte) (bool, error)) error {
	reader := &io.LimitedReader{R: r, N: maxResponseBytes + 1}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxResponseBytes+1)
	var event string
	var data strings.Builder
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if reader.N <= 0 {
			return fmt.Errorf("conversation stream too large")
		}
		line := scanner.Text()
		if !utf8.ValidString(line) {
			return fmt.Errorf("invalid conversation stream encoding")
		}
		if line == "" {
			if data.Len() > 0 {
				done, err := consume(event, []byte(strings.TrimSuffix(data.String(), "\n")))
				if err != nil {
					return err
				}
				if done {
					return nil
				}
			}
			event = ""
			data.Reset()
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return conversationNetworkError(err)
	}
	return fmt.Errorf("conversation stream ended before completion")
}
func (m *ConversationModel) StreamConversation(ctx context.Context, in agentsdk.ConversationModelRequest, emit func(string) error) (agentsdk.ConversationModelResult, error) {
	if emit == nil {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("conversation delta callback required")
	}
	resp, err := m.request(ctx, in, true)
	if err != nil {
		return agentsdk.ConversationModelResult{}, err
	}
	defer resp.Body.Close()
	kind, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || kind != "text/event-stream" {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("conversation model did not return SSE")
	}
	state := conversationStreamState{usage: map[string]any{}, blocks: map[int]string{}}
	accept := func(text string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if text == "" {
			return nil
		}
		if !validModelText(text) || len(text) > in.MaxOutputBytes-state.text.Len() {
			return fmt.Errorf("invalid or oversized conversation delta")
		}
		if err := emit(text); err != nil {
			return err
		}
		state.text.WriteString(text)
		return nil
	}
	err = readConversationSSE(ctx, resp.Body, func(event string, raw []byte) (bool, error) {
		switch m.config.Protocol {
		case ConversationProtocolMessages:
			return state.messages(event, raw, accept)
		case ConversationProtocolResponses:
			return state.responses(event, raw, accept)
		default:
			return state.chat(event, raw, accept)
		}
	})
	if err != nil {
		return agentsdk.ConversationModelResult{}, err
	}
	if strings.TrimSpace(state.text.String()) == "" {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("empty conversation stream")
	}
	return agentsdk.ConversationModelResult{Content: state.text.String(), Model: state.model, Usage: state.usage}, nil
}
