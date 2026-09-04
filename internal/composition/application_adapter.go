package composition

import (
	"context"
	"fmt"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	agenthttp "github.com/domainry/domainry-agent/internal/transport/http/module"
	"github.com/domainry/domainry-foundation/modulehttp"
)

type ApplicationAdapterDependencies struct {
	Binding           agentsdk.Binding
	DialogState       agentsdk.AgentDialogStateService
	TaskState         agentpersistence.AgentTaskStateService
	InteractiveState  agentpersistence.AgentInteractiveStateService
	ToolLedger        agentpersistence.AgentToolCallLedger
	TaskExecution     *agentapplication.TaskExecutionService
	InteractiveRunner agentsdk.InteractiveRunner
	Host              modulehost.ApplicationHost
}

// BindApplicationAdapter is the single Module/SaaS composition path for all
// Agent-owned product use cases. Deployment bindings supply storage/provider
// adapters; the application graph and Host Port boundary remain identical.
func BindApplicationAdapter(dependencies ApplicationAdapterDependencies) (modulehttp.Adapter, error) {
	host := dependencies.Host
	if host == nil || host.InteractiveAgent() == nil || host.TaskAgent() == nil || host.ProposalAgent() == nil || host.AuditAgent() == nil || host.AnalysisAgent() == nil {
		return nil, fmt.Errorf("Agent application host is incomplete")
	}
	if dependencies.Binding == nil || dependencies.DialogState == nil || dependencies.TaskState == nil || dependencies.InteractiveState == nil || dependencies.ToolLedger == nil || dependencies.TaskExecution == nil || dependencies.InteractiveRunner == nil {
		return nil, fmt.Errorf("Agent application dependencies are incomplete")
	}
	if err := dependencies.TaskExecution.BindHost(host.TaskAgent()); err != nil {
		return nil, err
	}
	proposals := agentapplication.NewProposalService(dependencies.DialogState, host.ProposalAgent(), host.AuditAgent(), dependencies.TaskExecution)
	execution := agentapplication.NewInteractiveExecutionService(agentapplication.InteractiveExecutionDependencies{
		State: dependencies.InteractiveState, Runner: dependencies.InteractiveRunner, Host: host.InteractiveAgent(), Proposals: proposals,
		WakeTask: func(_ context.Context, workspaceID, runID string) {
			dependencies.TaskExecution.Wake(workspaceID, runID)
		},
	})
	applications := agenthttp.AdapterApplications{
		Interactive: execution,
		Proposals:   proposals,
		TaskOperations: agentapplication.NewTaskOperationsService(
			dependencies.TaskState, host.AuditAgent(), dependencies.TaskExecution.Wake,
		),
		TaskTools:   agentapplication.NewTaskToolService(dependencies.TaskState, dependencies.ToolLedger, host.TaskAgent(), proposals),
		Analysis:    agentapplication.NewAnalysisService(host.AnalysisAgent(), host.AuditAgent()),
		Diagnostics: agentapplication.NewDiagnosticsService(dependencies.DialogState, host.AuditAgent()),
	}
	return agenthttp.NewOwnedAdapter(dependencies.Binding, applications)
}
