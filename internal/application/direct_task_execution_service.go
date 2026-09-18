package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type DirectTaskExecutionRequest struct {
	TaskKey, TaskVersion, IdempotencyKey string
	Input                                map[string]any
	Attachments                          []agentsdk.TaskAttachment
	AttachmentSources                    []modulehost.TaskAttachmentSource
	Principal                            modulehost.Principal
}

// DirectTaskExecutionService is the Agent-owned authenticated ingress for
// explicit product tasks. It authorizes the published Task definition through
// Runtime, then hands the frozen command to the same durable execution service
// used by Workflow and interactive handoffs.
type DirectTaskExecutionService struct {
	tasks *TaskExecutionService
	host  modulehost.InteractiveHost
	now   func() time.Time
}

func NewDirectTaskExecutionService(tasks *TaskExecutionService, host modulehost.InteractiveHost) *DirectTaskExecutionService {
	return &DirectTaskExecutionService{tasks: tasks, host: host, now: time.Now}
}

func (s *DirectTaskExecutionService) Start(ctx context.Context, request DirectTaskExecutionRequest) (agentmodel.AgentTaskRun, bool, error) {
	request.TaskKey, request.TaskVersion = strings.TrimSpace(request.TaskKey), strings.TrimSpace(request.TaskVersion)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if s == nil || s.tasks == nil || s.host == nil {
		return agentmodel.AgentTaskRun{}, false, unavailable("agent.task.direct_execution_unavailable")
	}
	if !request.Principal.Known || strings.TrimSpace(request.Principal.WorkspaceID) == "" || strings.TrimSpace(request.Principal.UserID) == "" || strings.TrimSpace(request.Principal.RoleKey) == "" || request.TaskKey == "" || request.TaskVersion == "" || request.IdempotencyKey == "" {
		return agentmodel.AgentTaskRun{}, false, badRequest("agent.task.direct_request_invalid")
	}
	authorization, err := s.host.AuthorizeInteractiveTask(ctx, modulehost.InteractiveTaskAuthorizationRequest{
		Principal: request.Principal, TaskKey: request.TaskKey, TaskVersion: request.TaskVersion,
	})
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	attachmentCount := len(request.Attachments) + len(request.AttachmentSources)
	if !taskAttachmentCountAllowed(authorization.Task.AttachmentSchema, attachmentCount) {
		return agentmodel.AgentTaskRun{}, false, badRequest("agent.task.attachment_count_invalid")
	}
	resolved := append([]agentsdk.TaskAttachment(nil), request.Attachments...)
	if len(request.AttachmentSources) != 0 {
		resolver, ok := s.host.(modulehost.TaskAttachmentSourceResolver)
		if !ok || resolver == nil {
			return agentmodel.AgentTaskRun{}, false, unavailable("agent.task.attachment_source_unavailable")
		}
		for _, source := range request.AttachmentSources {
			attachment, resolveErr := resolver.ResolveTaskAttachmentSource(ctx, modulehost.TaskAttachmentSourceRequest{
				Principal: request.Principal, TaskKey: request.TaskKey, TaskVersion: request.TaskVersion, Source: source,
			})
			if resolveErr != nil {
				return agentmodel.AgentTaskRun{}, false, resolveErr
			}
			resolved = append(resolved, attachment)
		}
	}
	runID := directTaskRunID(request.Principal, request.IdempotencyKey)
	attachments := make([]agentsdk.TaskAttachment, 0, len(resolved))
	for index, source := range resolved {
		attachment := cloneTaskAttachment(source, true)
		attachment.ID = directTaskAttachmentID(runID, index)
		attachments = append(attachments, attachment)
	}
	maxAttempts := 2
	command := agentsdk.TaskRequest{
		TaskRunID: runID, WorkspaceID: request.Principal.WorkspaceID, Task: authorization.Task, Identity: authorization.Identity,
		Input: cloneTaskMap(request.Input), Attachments: attachments,
		AllowedObjects: append([]string(nil), authorization.Evidence.AllowedObjects...), AllowedActions: append([]string(nil), authorization.Evidence.AllowedActions...),
		AllowedOutcomes: append([]string(nil), authorization.Evidence.AllowedOutcomes...), AllowedTools: append([]string(nil), authorization.Evidence.AllowedTools...),
		CorrelationID: request.Principal.CorrelationID, IdempotencyKey: request.IdempotencyKey, MaxAttempts: maxAttempts,
	}
	if timeout := authorization.Task.ExecutionLimits.TimeoutSeconds; timeout > 0 {
		command.Deadline = s.now().UTC().Add(time.Duration(timeout) * time.Second)
	}
	authorizedContext := agentsdk.WithAuthorizedServiceAction(ctx, agentsdk.ActionAgentTaskExecutionStart, agentsdk.AgentRuntimeServiceAudience)
	run, replayed, err := s.tasks.StartRun(authorizedContext, command)
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	return run, replayed, nil
}

func directTaskRunID(principal modulehost.Principal, key string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{principal.WorkspaceID, principal.UserID, strings.TrimSpace(key)}, "\x00")))
	return "agent_task_" + hex.EncodeToString(digest[:16])
}

func directTaskAttachmentID(runID string, index int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", strings.TrimSpace(runID), index)))
	return "att_" + hex.EncodeToString(digest[:16])
}
