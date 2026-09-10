package provider

import (
	"encoding/json"
	"fmt"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"strings"
)

type chatText struct {
	Role         string            `json:"role"`
	Content      string            `json:"content"`
	Refusal      string            `json:"refusal"`
	ToolCalls    []json.RawMessage `json:"tool_calls"`
	FunctionCall json.RawMessage   `json:"function_call"`
}

func (v chatText) text() (string, error) {
	if (v.Role != "" && v.Role != "assistant") || len(v.ToolCalls) > 0 || presentJSON(v.FunctionCall) {
		return "", fmt.Errorf("conversation requires an assistant text reply")
	}
	return v.Content + v.Refusal, nil
}

type chatEnvelope struct {
	Model   string          `json:"model"`
	Usage   map[string]any  `json:"usage"`
	Error   json.RawMessage `json:"error"`
	Choices []struct {
		Index        int      `json:"index"`
		FinishReason string   `json:"finish_reason"`
		Message      chatText `json:"message"`
		Delta        chatText `json:"delta"`
	} `json:"choices"`
}

func decodeChat(raw []byte) (agentsdk.ConversationModelResult, error) {
	var v chatEnvelope
	if json.Unmarshal(raw, &v) != nil || presentJSON(v.Error) || len(v.Choices) != 1 || v.Choices[0].FinishReason != "stop" || v.Choices[0].Message.Role != "assistant" {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("incomplete conversation chat response")
	}
	text, err := v.Choices[0].Message.text()
	return agentsdk.ConversationModelResult{Content: text, Model: v.Model, Usage: v.Usage}, err
}

type contentBlock struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}
type messagesEnvelope struct {
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Content    []contentBlock `json:"content"`
	Usage      map[string]any `json:"usage"`
}

func decodeMessages(raw []byte) (agentsdk.ConversationModelResult, error) {
	var v messagesEnvelope
	if json.Unmarshal(raw, &v) != nil || v.Type != "message" || v.Role != "assistant" || v.StopReason != "end_turn" {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("incomplete conversation Messages response")
	}
	var text strings.Builder
	for _, block := range v.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking", "redacted_thinking":
		default:
			return agentsdk.ConversationModelResult{}, fmt.Errorf("unsupported conversation content block")
		}
	}
	return agentsdk.ConversationModelResult{Content: text.String(), Model: v.Model, Usage: v.Usage}, nil
}

type responseItem struct {
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Status  string         `json:"status"`
	Content []contentBlock `json:"content"`
}
type responseEnvelope struct {
	Status     string          `json:"status"`
	Model      string          `json:"model"`
	Error      json.RawMessage `json:"error"`
	Incomplete json.RawMessage `json:"incomplete_details"`
	Output     []responseItem  `json:"output"`
	Usage      map[string]any  `json:"usage"`
}

func decodeResponse(raw []byte) (agentsdk.ConversationModelResult, error) {
	var v responseEnvelope
	if json.Unmarshal(raw, &v) != nil || v.Status != "completed" || presentJSON(v.Error) || presentJSON(v.Incomplete) {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("incomplete conversation Responses response")
	}
	var text strings.Builder
	for _, item := range v.Output {
		if item.Type == "reasoning" {
			continue
		}
		if item.Type != "message" || item.Role != "assistant" || (item.Status != "" && item.Status != "completed") {
			return agentsdk.ConversationModelResult{}, fmt.Errorf("unsupported conversation output item")
		}
		for _, block := range item.Content {
			switch block.Type {
			case "output_text":
				text.WriteString(block.Text)
			case "refusal":
				text.WriteString(block.Refusal)
			default:
				return agentsdk.ConversationModelResult{}, fmt.Errorf("unsupported conversation output content")
			}
		}
	}
	return agentsdk.ConversationModelResult{Content: text.String(), Model: v.Model, Usage: v.Usage}, nil
}
