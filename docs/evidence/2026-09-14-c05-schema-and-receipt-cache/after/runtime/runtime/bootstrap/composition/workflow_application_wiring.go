package composition

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func assembleWorkflowApplication(records *runtimeAssembly) *workflowapplication.WorkflowApplicationService {
	return workflowapplication.NewWorkflowApplicationService(workflowDependencies(records))
}

func assembleWorkflowProcessEngine(records *runtimeAssembly) *workflowapplication.WorkflowProcessEngine {
	return workflowapplication.NewWorkflowProcessEngine(workflowDependencies(records))
}

type runtimeWorkflowRegistry struct{ records *runtimeAssembly }

func (r runtimeWorkflowRegistry) List() []definitionmodel.WorkflowSchema {
	r.records.mu.RLock()
	defer r.records.mu.RUnlock()
	values := make([]definitionmodel.WorkflowSchema, 0, len(r.records.workflows))
	for _, value := range r.records.workflows {
		values = append(values, value)
	}
	return values
}

func (r runtimeWorkflowRegistry) Get(key string) (definitionmodel.WorkflowSchema, bool) {
	r.records.mu.RLock()
	defer r.records.mu.RUnlock()
	value, ok := r.records.workflows[strings.TrimSpace(key)]
	return value, ok
}

func (r runtimeWorkflowRegistry) Set(key string, value definitionmodel.WorkflowSchema) {
	r.records.mu.Lock()
	defer r.records.mu.Unlock()
	r.records.workflows[strings.TrimSpace(key)] = value
	r.records.schemaGeneration++
}

func (r runtimeWorkflowRegistry) Delete(key string) {
	r.records.mu.Lock()
	defer r.records.mu.Unlock()
	delete(r.records.workflows, strings.TrimSpace(key))
	r.records.schemaGeneration++
}

func (r runtimeWorkflowRegistry) Count() int {
	r.records.mu.RLock()
	defer r.records.mu.RUnlock()
	return len(r.records.workflows)
}

type runtimeWorkflowSchemaProvider struct{ records *runtimeAssembly }

type runtimeInteractiveWorkflowStarter struct{ records *runtimeAssembly }

func (s runtimeInteractiveWorkflowStarter) StartInteractiveAgentWorkflow(ctx context.Context, workflowKey string, input map[string]any, interactiveRunID, idempotencyKey string, principal principalmodel.Principal) (string, error) {
	if s.records == nil {
		return "", apperror.New(apperror.KindUnavailable, "agent.interactive.workflow_handoff_unavailable", nil, nil)
	}
	if s.records.workflowApplicationService == nil {
		return "", apperror.New(apperror.KindUnavailable, "agent.interactive.workflow_handoff_unavailable", nil, nil)
	}
	return startInteractiveAgentWorkflow(ctx, s.records.workflowApplicationService, workflowKey, input, interactiveRunID, idempotencyKey, principal)
}

type interactiveWorkflowRunner interface {
	RunAgentWorkflow(context.Context, string, map[string]any, principalmodel.Principal) (workflowmodel.WorkflowRunResult, error)
}

func startInteractiveAgentWorkflow(ctx context.Context, workflows interactiveWorkflowRunner, workflowKey string, input map[string]any, interactiveRunID, idempotencyKey string, principal principalmodel.Principal) (string, error) {
	payload := make(map[string]any, len(input)+1)
	for key, value := range input {
		payload[key] = value
	}
	payload["agent_handoff_idempotency_key"] = strings.TrimSpace(idempotencyKey)
	payload["agent_interactive_run_id"] = strings.TrimSpace(interactiveRunID)
	result, err := workflows.RunAgentWorkflow(ctx, workflowKey, payload, principal)
	if err != nil {
		return "", err
	}
	processID := strings.TrimSpace(result.Execution.ProcessID)
	if processID == "" {
		return "", apperror.New(apperror.KindConflict, "agent.interactive.workflow_process_required", nil, nil)
	}
	return processID, nil
}
