package module

import "strings"

func (s *surface) OpenAPIOperations() map[string]map[string]any {
	all := agentOpenAPIOperations()
	owned := make(map[string]map[string]any, len(s.routes))
	for _, route := range s.routes {
		if operation := all[strings.TrimSpace(route.Pattern)]; len(operation) != 0 {
			owned[route.Pattern] = operation
		}
	}
	return owned
}

func agentOpenAPIOperations() map[string]map[string]any {
	operations := map[string]map[string]any{}
	add := func(pattern, operationID, summary string, body bool) {
		operation := map[string]any{
			"operationId": operationID,
			"tags":        []string{"Agent"},
			"summary":     summary,
			"security":    []map[string]any{{"BearerAuth": []string{}}},
			"responses": map[string]any{
				"200": map[string]any{"description": "Agent response", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}},
				"400": map[string]any{"description": "Invalid Agent request"},
				"403": map[string]any{"description": "Agent access denied"},
				"409": map[string]any{"description": "Agent state conflict"},
			},
		}
		if body {
			operation["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}}
		}
		operations[pattern] = operation
	}
	add("POST /agent-dialog/runs", "runAgent", "Run an Agent conversation turn", true)
	add("POST /agent-dialog/runs/stream", "runAgentStream", "Stream an Agent conversation turn", true)
	operations["POST /agent-dialog/runs/stream"]["responses"] = map[string]any{
		"200": map[string]any{"description": "Agent event stream", "content": map[string]any{"text/event-stream": map[string]any{"schema": map[string]any{"type": "string"}}}},
		"400": map[string]any{"description": "Invalid Agent request"},
		"403": map[string]any{"description": "Agent access denied"},
	}
	operations["POST /agent-dialog/runs/stream"]["x-domainry-runtime-client-method"] = "runAgentStream"
	add("GET /agent-dialog/sessions", "listAgentSessions", "List Agent sessions", false)
	add("POST /agent-dialog/sessions", "upsertAgentSession", "Create or update an Agent session", true)
	add("POST /agent-dialog/sessions/{externalSessionID}/archive", "archiveAgentSession", "Archive an Agent session", false)
	add("POST /agent-dialog/sessions/{externalSessionID}/restore", "restoreAgentSession", "Restore an Agent session", false)
	add("GET /agent-dialog/proposals", "listAgentProposals", "List Agent proposals", false)
	add("GET /agent-dialog/proposals/{proposalID}", "getAgentProposal", "Get an Agent proposal", false)
	add("POST /agent-dialog/proposals", "createAgentProposal", "Create an Agent proposal", true)
	add("POST /agent-dialog/proposals/{proposalID}/approve", "approveAgentProposal", "Approve an Agent proposal", true)
	add("POST /agent-dialog/proposals/{proposalID}/reject", "rejectAgentProposal", "Reject an Agent proposal", true)
	add("GET /agent-dialog/runs/{runID}", "getAgentRun", "Get an Agent conversation run", false)
	add("GET /agent-dialog/task-runs/{taskRunID}", "getAgentTaskRun", "Get an Agent task run", false)
	add("POST /agent-dialog/task-tools/invoke", "invokeAgentTaskTool", "Invoke a credential-scoped Agent task tool", true)
	operations["POST /agent-dialog/task-tools/invoke"]["security"] = []any{}
	add("POST /agent-dialog/analysis/query", "queryAgentAnalysis", "Run an Agent analysis query", true)
	operations["POST /agent-dialog/analysis/query"]["x-domainry-runtime-client-method"] = "queryAgentAnalysis"
	add("GET /agent-dialog/diagnostics", "getAgentDiagnostics", "Inspect Agent diagnostics", false)
	add("GET /operations/agent/tasks", "listAgentTasks", "List Agent task runs", false)
	add("GET /operations/agent/tasks/{taskRunID}", "getAgentOperationalTask", "Inspect an Agent task run", false)
	add("POST /operations/agent/tasks/{taskRunID}/retry", "retryAgentTask", "Retry an Agent task", true)
	add("POST /operations/agent/tasks/{taskRunID}/cancel", "cancelAgentTask", "Cancel an Agent task", true)
	add("POST /operations/agent/tasks/{taskRunID}/resolve", "resolveAgentTask", "Resolve an Agent task", true)
	add("POST /operations/agent/tasks/{taskRunID}/reconcile", "reconcileAgentTask", "Reconcile an Agent workflow callback", true)
	return operations
}
