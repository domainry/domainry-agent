package application

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// Explain correctable schema failures without echoing argument values,
// arbitrary validator messages, or unbounded schema error trees.
func conversationArgumentFailure(err error) agentsdk.ConversationToolResult {
	type issue struct {
		Path       string   `json:"path"`
		Keyword    string   `json:"keyword"`
		Properties []string `json:"properties,omitempty"`
		Expected   []string `json:"expected,omitempty"`
	}
	clip := func(s string, max int) string {
		if len(s) <= max {
			return s
		}
		s = s[:max]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		return s
	}
	names := func(values []string) []string {
		out := []string{}
		for _, value := range values {
			if len(out) == 8 {
				break
			}
			out = append(out, clip(value, 64))
		}
		return out
	}
	issues := []issue{}
	var validation *jsonschema.ValidationError
	if errors.As(err, &validation) {
		var walk func(*jsonschema.ValidationError, int)
		walk = func(node *jsonschema.ValidationError, depth int) {
			if node == nil || depth > 64 || len(issues) >= 4 {
				return
			}
			if len(node.Causes) > 0 {
				for _, child := range node.Causes {
					walk(child, depth+1)
					if len(issues) >= 4 {
						break
					}
				}
				return
			}
			parts := make([]string, len(node.InstanceLocation))
			for i, value := range node.InstanceLocation {
				parts[i] = strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
			}
			path := ""
			if len(parts) > 0 {
				path = "/" + strings.Join(parts, "/")
			}
			value := issue{Path: clip(path, 256), Keyword: "schema"}
			if node.ErrorKind != nil && len(node.ErrorKind.KeywordPath()) > 0 {
				value.Keyword = clip(strings.Join(node.ErrorKind.KeywordPath(), "/"), 128)
			}
			switch detail := node.ErrorKind.(type) {
			case *kind.AdditionalProperties:
				value.Properties = names(detail.Properties)
			case *kind.Required:
				value.Properties = names(detail.Missing)
			case *kind.Type:
				value.Expected = names(detail.Want)
			}
			issues = append(issues, value)
		}
		walk(validation, 0)
	}
	if len(issues) == 0 {
		issues = append(issues, issue{Keyword: "json_or_schema"})
	}
	raw, _ := json.Marshal(map[string]any{"error": "arguments_invalid", "message": "No operation was performed. Correct these fields using the registered tool schema; do not repeat unchanged arguments.", "issues": issues})
	return agentsdk.ConversationToolResult{Status: "failed", ErrorCode: "arguments_invalid", Content: raw}
}
