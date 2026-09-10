package application

import (
	"context"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

// Both provider startup and individual callbacks resolve the current task
// authority through the host. Historical evidence is an upper scope bound,
// never a substitute for a fresh grant or the effect gateway's credential and
// record/field authorization. Conversation uses its independent tool host.
func authorizeTaskRun(ctx context.Context, host modulehost.TaskHost, run agentmodel.AgentTaskRun) (modulehost.TaskAuthorization, error) {
	if err := ctx.Err(); err != nil {
		return modulehost.TaskAuthorization{}, err
	}
	authorization, err := host.AuthorizeTask(ctx, modulehost.TaskAuthorizationRequest{
		TaskRunID: run.ID, WorkspaceID: run.WorkspaceID, ProcessID: run.ProcessID, NodeInstanceID: run.NodeInstanceID,
		TaskKey: run.TaskKey, TaskVersion: run.TaskVersion, Identity: run.Identity, CorrelationID: run.CorrelationID,
		PreviousEvidence: append([]agentmodel.AgentAuthorizationEvidence(nil), run.Evidence.Authorization...),
	})
	if err != nil {
		return modulehost.TaskAuthorization{}, err
	}
	if !authorization.Principal.Known || authorization.Principal.UserID == "" || authorization.Principal.WorkspaceID != run.WorkspaceID || authorization.Task.Key != run.TaskKey || authorization.Task.Version != run.TaskVersion {
		return modulehost.TaskAuthorization{}, forbidden("agent.task.authorization_scope_denied")
	}
	if err = ctx.Err(); err != nil {
		return modulehost.TaskAuthorization{}, err
	}
	return authorization, nil
}
