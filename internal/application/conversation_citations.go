package application

import (
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

const conversationCitationInstruction = " Cite source-backed final answers with an exact [[cite:ID]] supplied by a completed tool; never invent IDs. Uncited source-backed answers are rejected."

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

func conversationHasCitableEvidence(run agentsdk.ConversationRun) bool {
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.Status == "completed" && len(call.Citations) > 0 {
				return true
			}
		}
	}
	return false
}

func requireConversationAnswerCitation(run agentsdk.ConversationRun, text string) error {
	if conversationHasCitableEvidence(run) && len(conversationCitations(run, text)) == 0 {
		return conversationFailure("conflict", "answer_citation_required")
	}
	return nil
}
