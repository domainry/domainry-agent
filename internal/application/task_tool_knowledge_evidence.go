package application

import (
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

func taskKnowledgeReceipt(request TaskToolInvocation, result modulehost.TaskToolResult) (*agentmodel.AgentTaskKnowledgeEvidence, error) {
	invalid := func() (*agentmodel.AgentTaskKnowledgeEvidence, error) {
		return nil, unavailable("agent.tool.knowledge_evidence_invalid")
	}
	tool := strings.TrimSpace(request.Tool)
	if tool != agentsdk.AgentToolKnowledgeSearch && tool != agentsdk.AgentToolKnowledgeRead {
		if result.Knowledge != nil {
			return invalid()
		}
		return nil, nil
	}
	e := result.Knowledge
	if result.Tool != tool || result.Status != "executed" || e == nil || e.Validate() != nil || len(request.Input) != 2 {
		return invalid()
	}
	library, _ := request.Input["library_id"].(string)
	if e.LibraryID != strings.TrimSpace(library) {
		return invalid()
	}
	operation := "search"
	if tool == agentsdk.AgentToolKnowledgeSearch {
		query, _ := request.Input["query"].(string)
		if strings.TrimSpace(query) == "" {
			return invalid()
		}
	}
	if tool == agentsdk.AgentToolKnowledgeRead {
		operation = "fetch"
		document, _ := request.Input["document_id"].(string)
		if strings.TrimSpace(document) == "" {
			return invalid()
		}
		for _, c := range e.Citations {
			if c.DocumentID != strings.TrimSpace(document) {
				return invalid()
			}
		}
	}
	if e.Operation != operation {
		return invalid()
	}
	return e.Clone(), nil
}
