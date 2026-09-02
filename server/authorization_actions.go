package server

import (
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulecapability"
)

const (
	agentSaaSRepositoryActionPrefix = "agent.saas.repository."

	actionAgentSaaSDescriptorRead       = "agent.saas.descriptor.read"
	actionAgentSaaSReadinessRead        = "agent.saas.readiness.read"
	actionAgentSaaSTaskProviderStart    = "agent.saas.task_provider.start"
	actionAgentSaaSTaskProviderPoll     = "agent.saas.task_provider.poll"
	actionAgentSaaSTaskProviderCancel   = "agent.saas.task_provider.cancel"
	actionAgentSaaSInteractiveRun       = "agent.saas.interactive_provider.run"
	actionAgentSaaSSessionsQuery        = "agent.saas.dialog_state.sessions.query"
	actionAgentSaaSSessionsUpsert       = "agent.saas.dialog_state.sessions.upsert"
	actionAgentSaaSSessionsSetArchived  = "agent.saas.dialog_state.sessions.set_archived"
	actionAgentSaaSProposalsQuery       = "agent.saas.dialog_state.proposals.query"
	actionAgentSaaSProposalsGet         = "agent.saas.dialog_state.proposals.get"
	actionAgentSaaSProposalsStore       = "agent.saas.dialog_state.proposals.store"
	actionAgentSaaSProposalsDecide      = "agent.saas.dialog_state.proposals.decide"
	actionAgentSaaSCapabilitySummary    = "agent.saas.capability.summary"
	actionAgentSaaSCapabilityCategory   = "agent.saas.capability.category.get"
	actionAgentSaaSCapabilityValidation = "agent.saas.capability.validate"
)

type saasHTTPActionSpec struct {
	key, pattern, capabilityKey, capabilityLabel, label string
	effect                                              actioncontract.EffectClass
	risk                                                actioncontract.RiskLevel
	idempotency                                         string
}

// SaaSAuthorizationActions is the single source-owned manifest for Agent's
// standalone service protocol. Every route is service-identity-only and owns no
// human Role Permission; product Actions remain in agentsdk.AgentAuthorizationActions.
func SaaSAuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	read, write := actioncontract.EffectRead, actioncontract.EffectWrite
	low, medium, high := actioncontract.RiskLow, actioncontract.RiskMedium, actioncontract.RiskHigh
	specs := []saasHTTPActionSpec{
		{actionAgentSaaSDescriptorRead, "GET /api/v1/descriptor", "agent.saas.discovery", "Agent SaaS discovery", "Read service descriptor", read, low, "not_applicable"},
		{actionAgentSaaSReadinessRead, "GET /readyz", "agent.saas.discovery", "Agent SaaS discovery", "Read service readiness", read, low, "not_applicable"},
		{actionAgentSaaSTaskProviderStart, "POST /api/v1/task-runs", "agent.saas.task_provider", "Agent provider execution", "Start provider task run", write, medium, "caller_key_required"},
		{actionAgentSaaSTaskProviderPoll, "GET /api/v1/task-runs/{id}", "agent.saas.task_provider", "Agent provider execution", "Poll provider task run", read, low, "not_applicable"},
		{actionAgentSaaSTaskProviderCancel, "POST /api/v1/task-runs/{id}/cancel", "agent.saas.task_provider", "Agent provider execution", "Cancel provider task run", write, medium, "caller_key_required"},
		{actionAgentSaaSInteractiveRun, "POST /api/v1/interactive-runs", "agent.saas.interactive_provider", "Agent interactive provider", "Run interactive provider", write, medium, "caller_key_required"},
		{actionAgentSaaSSessionsQuery, "POST /api/v1/dialog-state/sessions/query", "agent.saas.dialog_state", "Agent SaaS dialog state", "Query sessions", read, low, "not_applicable"},
		{actionAgentSaaSSessionsUpsert, "POST /api/v1/dialog-state/sessions/upsert", "agent.saas.dialog_state", "Agent SaaS dialog state", "Upsert session", write, medium, "request_contract"},
		{actionAgentSaaSSessionsSetArchived, "POST /api/v1/dialog-state/sessions/{id}/archive", "agent.saas.dialog_state", "Agent SaaS dialog state", "Set session archive state", write, medium, "natural"},
		{actionAgentSaaSProposalsQuery, "POST /api/v1/dialog-state/proposals/query", "agent.saas.dialog_state", "Agent SaaS dialog state", "Query proposals", read, low, "not_applicable"},
		{actionAgentSaaSProposalsGet, "POST /api/v1/dialog-state/proposals/{id}/get", "agent.saas.dialog_state", "Agent SaaS dialog state", "Read proposal", read, low, "not_applicable"},
		{actionAgentSaaSProposalsStore, "POST /api/v1/dialog-state/proposals/store", "agent.saas.dialog_state", "Agent SaaS dialog state", "Store proposal", write, medium, "request_contract"},
		{actionAgentSaaSProposalsDecide, "POST /api/v1/dialog-state/proposals/decide", "agent.saas.dialog_state", "Agent SaaS dialog state", "Decide proposal", write, high, "request_contract"},
		{actionAgentSaaSCapabilitySummary, "GET " + modulecapability.SummaryPath, "agent.saas.capability", "Agent capability protocol", "Read capability summary", read, low, "not_applicable"},
		{actionAgentSaaSCapabilityCategory, "GET " + modulecapability.CategoriesPath + "{key}", "agent.saas.capability", "Agent capability protocol", "Read capability category", read, low, "not_applicable"},
		{actionAgentSaaSCapabilityValidation, "POST " + modulecapability.ValidationPath, "agent.saas.capability", "Agent capability protocol", "Validate capability candidate", read, low, "not_applicable"},

		{agentSaaSRepositoryActionPrefix + "definitions.sync", "POST /api/v1/definitions/sync", "agent.saas.repository.definitions", "Agent definition repository", "Synchronize definitions", write, high, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "definitions.snapshot", "POST /api/v1/definitions/snapshot", "agent.saas.repository.definitions", "Agent definition repository", "Read definition snapshot", read, low, "not_applicable"},
		{agentSaaSRepositoryActionPrefix + "state.list", "POST /api/v1/internal/dialog-state/records/query", "agent.saas.repository.dialog_state", "Agent dialog-state repository", "Query state records", read, low, "not_applicable"},
		{agentSaaSRepositoryActionPrefix + "state.get", "POST /api/v1/internal/dialog-state/records/get", "agent.saas.repository.dialog_state", "Agent dialog-state repository", "Read state record", read, low, "not_applicable"},
		{agentSaaSRepositoryActionPrefix + "state.put", "POST /api/v1/internal/dialog-state/records/put", "agent.saas.repository.dialog_state", "Agent dialog-state repository", "Store state record", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "state.put_batch", "POST /api/v1/internal/dialog-state/records/put-batch", "agent.saas.repository.dialog_state", "Agent dialog-state repository", "Store state record batch", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "state.compare_and_swap", "POST /api/v1/internal/dialog-state/records/compare-and-swap", "agent.saas.repository.dialog_state", "Agent dialog-state repository", "Compare and swap state record", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "task.create", "POST /api/v1/internal/execution-state/task-runs/create", "agent.saas.repository.task_state", "Agent task-state repository", "Create task run", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "task.get", "POST /api/v1/internal/execution-state/task-runs/get", "agent.saas.repository.task_state", "Agent task-state repository", "Read task run", read, low, "not_applicable"},
		{agentSaaSRepositoryActionPrefix + "task.list", "POST /api/v1/internal/execution-state/task-runs/query", "agent.saas.repository.task_state", "Agent task-state repository", "Query task runs", read, low, "not_applicable"},
		{agentSaaSRepositoryActionPrefix + "task.claim_next", "POST /api/v1/internal/execution-state/task-runs/claim-next", "agent.saas.repository.task_state", "Agent task-state repository", "Claim next task run", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "task.heartbeat", "POST /api/v1/internal/execution-state/task-runs/heartbeat", "agent.saas.repository.task_state", "Agent task-state repository", "Heartbeat task lease", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "task.save_running", "POST /api/v1/internal/execution-state/task-runs/save-running", "agent.saas.repository.task_state", "Agent task-state repository", "Save running task", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "task.save_waiting_approval", "POST /api/v1/internal/execution-state/task-runs/save-waiting", "agent.saas.repository.task_state", "Agent task-state repository", "Save waiting-approval task", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "task.save_terminal_override", "POST /api/v1/internal/execution-state/task-runs/override", "agent.saas.repository.task_state", "Agent task-state repository", "Save terminal task override", write, high, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "task.save_operational_transition", "POST /api/v1/internal/execution-state/task-runs/operate", "agent.saas.repository.task_state", "Agent task-state repository", "Save task operational transition", write, high, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "task.request_cancel", "POST /api/v1/internal/execution-state/task-runs/request-cancel", "agent.saas.repository.task_state", "Agent task-state repository", "Request task cancellation", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "task.worker_list", "POST /api/v1/internal/execution-state/task-runs/worker/query", "agent.saas.repository.task_state", "Agent task-state repository", "Query worker tasks", read, low, "not_applicable"},
		{agentSaaSRepositoryActionPrefix + "task.worker_claim", "POST /api/v1/internal/execution-state/task-runs/worker/claim", "agent.saas.repository.task_state", "Agent task-state repository", "Claim worker task", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "task.direct_claim", "POST /api/v1/internal/execution-state/task-runs/claim", "agent.saas.repository.task_state", "Agent task-state repository", "Claim task directly", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "interactive.create", "POST /api/v1/internal/execution-state/interactive-runs/create", "agent.saas.repository.interactive_state", "Agent interactive-state repository", "Create interactive run", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "interactive.get", "POST /api/v1/internal/execution-state/interactive-runs/get", "agent.saas.repository.interactive_state", "Agent interactive-state repository", "Read interactive run", read, low, "not_applicable"},
		{agentSaaSRepositoryActionPrefix + "interactive.list", "POST /api/v1/internal/execution-state/interactive-runs/query", "agent.saas.repository.interactive_state", "Agent interactive-state repository", "Query interactive runs", read, low, "not_applicable"},
		{agentSaaSRepositoryActionPrefix + "interactive.save", "POST /api/v1/internal/execution-state/interactive-runs/save", "agent.saas.repository.interactive_state", "Agent interactive-state repository", "Save interactive run", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "interactive.handoff", "POST /api/v1/internal/execution-state/interactive-runs/handoff", "agent.saas.repository.interactive_state", "Agent interactive-state repository", "Commit interactive handoff", write, high, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "tool.begin", "POST /api/v1/internal/execution-state/tool-invocations/begin", "agent.saas.repository.tool_ledger", "Agent tool-call ledger", "Begin tool invocation", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "tool.finish", "POST /api/v1/internal/execution-state/tool-invocations/finish", "agent.saas.repository.tool_ledger", "Agent tool-call ledger", "Finish tool invocation", write, medium, "request_contract"},
		{agentSaaSRepositoryActionPrefix + "lifecycle.list", "POST /api/v1/lifecycle/executions/query", "agent.saas.repository.lifecycle", "Agent lifecycle repository", "Query lifecycle executions", read, low, "not_applicable"},
		{agentSaaSRepositoryActionPrefix + "lifecycle.delete", "POST /api/v1/lifecycle/executions/delete", "agent.saas.repository.lifecycle", "Agent lifecycle repository", "Delete lifecycle execution", write, high, "request_contract"},
	}

	registry := actioncontract.NewRegistry()
	for _, spec := range specs {
		definition, err := buildSaaSAction(spec)
		if err != nil {
			return nil, err
		}
		if err := registry.Register(definition); err != nil {
			return nil, fmt.Errorf("register Agent SaaS Action %q: %w", definition.Key, err)
		}
	}
	if err := registry.Freeze(); err != nil {
		return nil, fmt.Errorf("freeze Agent SaaS Action manifest: %w", err)
	}
	return registry.Definitions(), nil
}

func buildSaaSAction(spec saasHTTPActionSpec) (actioncontract.ActionDefinition, error) {
	method, route, found := strings.Cut(strings.TrimSpace(spec.pattern), " ")
	separator := strings.LastIndex(spec.key, ".")
	if !found || separator <= 0 || separator == len(spec.key)-1 {
		return actioncontract.ActionDefinition{}, fmt.Errorf("Agent SaaS Action %q has an invalid identity or route", spec.key)
	}
	definition := actioncontract.ActionDefinition{
		Key: spec.key, Owner: agentsdk.AgentAuthorizationOwner, SourceKind: "service_protocol",
		CapabilityKey: spec.capabilityKey, CapabilityLabel: spec.capabilityLabel,
		OperationKey: spec.key[separator+1:], OperationLabel: spec.label, Label: spec.label,
		Exposures:     []actioncontract.Exposure{actioncontract.ExposureOps},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationServiceIdentity, PolicyKey: "agent.saas_api_key", Audiences: []string{agentsdk.AgentRuntimeServiceAudience}},
		HTTP:          &actioncontract.HTTPBinding{Method: method, RouteTemplate: route},
		EffectClass:   spec.effect, RiskLevel: spec.risk, IdempotencyDecision: spec.idempotency,
		AuditClass: "agent_saas_protocol", LifecycleStatus: actioncontract.LifecycleActive,
	}
	normalized, err := actioncontract.NormalizeDefinition(definition)
	if err != nil {
		return actioncontract.ActionDefinition{}, fmt.Errorf("normalize Agent SaaS Action %q: %w", spec.key, err)
	}
	return normalized, nil
}
