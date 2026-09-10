package application

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"strings"
)

const conversationCitationInstruction = " When a knowledge result supplies citations, cite its exact ID as [[cite:ID]] next to the supported claim. Use only IDs from completed knowledge results obtained in this run. Never invent a citation ID or quote a truncated excerpt as a complete document. If no structured citations are supplied, preserve only actually supplied source details without inventing verified citation markers."

// Message projections contain only source IDs actually cited in that reply.
// Other retrieved documents remain available in the corresponding tool record.
func conversationCitations(run agentsdk.ConversationRun, text string) []agentsdk.ConversationCitation {
	var out []agentsdk.ConversationCitation
	seen := map[string]bool{}
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.Status != "completed" {
				continue
			}
			for _, citation := range call.Citations {
				if seen[citation.ID] || !strings.Contains(text, "[[cite:"+citation.ID+"]]") {
					continue
				}
				seen[citation.ID] = true
				out = append(out, citation)
			}
		}
	}
	return out
}
