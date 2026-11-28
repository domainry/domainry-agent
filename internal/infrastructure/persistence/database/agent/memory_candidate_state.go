package agent

import agentsdk "github.com/domainry/domainry-agent-sdk"

const memoryCandidateStatePending = "pending"

func memoryCandidateState(evidence *agentsdk.AgentMemoryEvidence) string {
	if evidence == nil || len(evidence.Candidates) == 0 {
		return ""
	}
	if len(evidence.CandidateReceipts) == 0 {
		return memoryCandidateStatePending
	}
	for _, receipt := range evidence.CandidateReceipts {
		if receipt.Disposition == agentsdk.AgentMemoryCandidateFailed {
			return memoryCandidateStatePending
		}
	}
	return ""
}
