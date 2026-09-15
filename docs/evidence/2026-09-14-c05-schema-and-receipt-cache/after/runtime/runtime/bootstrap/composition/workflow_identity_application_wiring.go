package composition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func (p runtimeWorkflowSchemaProvider) WorkflowSchemaSnapshot(ctx context.Context, principal principalmodel.Principal) workflowapplication.WorkflowSchemaSnapshot {
	if ctx.Err() != nil {
		return workflowapplication.WorkflowSchemaSnapshot{}
	}
	snapshot := p.records.Schema()
	if principal.Known {
		snapshot = appschemaservice.SnapshotForPrincipal(snapshot, principal)
	}
	if p.records.actionService != nil {
		// Schema() is an immutable generation snapshot. The workflow adapter
		// substitutes its executable action view in a private slice.
		snapshot.Actions = append([]definitionmodel.ActionSchema(nil), snapshot.Actions...)
		executable := make(map[string]definitionmodel.ActionSchema)
		for _, action := range p.records.actionService.Definitions() {
			executable[strings.TrimSpace(action.Key)] = action
		}
		for index := range snapshot.Actions {
			if action, exists := executable[strings.TrimSpace(snapshot.Actions[index].Key)]; exists {
				snapshot.Actions[index] = action
			}
		}
	}
	return workflowapplication.WorkflowSchemaSnapshot{Actions: snapshot.Actions, AgentTasks: snapshot.AgentTasks, AgentServicePrincipals: snapshot.AgentServicePrincipals, Dictionaries: snapshot.Dictionaries, Integrations: snapshot.Integrations}
}

func (p runtimeWorkflowSchemaProvider) ConnectorAdapterExists(ctx context.Context, key string) bool {
	if ctx.Err() != nil || p.records.connectorRegistry == nil {
		return false
	}
	key = strings.TrimSpace(key)
	return p.records.connectorRegistry.ConnectorDeclared(key)
}

type runtimeWorkflowRecordTimers struct {
	recordTimers *recordtimerapplication.RecordTimerApplicationService
}

func (s runtimeWorkflowRecordTimers) ScheduleWorkflowWaitTimer(ctx context.Context, request workflowapplication.WorkflowWaitTimerRequest) (string, error) {
	contract := request.Contract
	schedule := recordtimerapplication.RecordTimerSchedule{
		TimerKey: strings.TrimSpace(contract.TimerKey), ObjectKey: "workflow_process", RecordID: request.ProcessID,
		Purpose: strings.TrimSpace(contract.Purpose), SourceField: strings.TrimSpace(contract.SourceField), OffsetSeconds: contract.OffsetSeconds,
		Timezone: strings.TrimSpace(contract.Timezone), BusinessCalendarKey: strings.TrimSpace(contract.BusinessCalendarKey),
		TargetType: "workflow", TargetKey: "resume_node", Priority: 0, Sequence: request.CreatedAt.UnixNano(),
	}
	if schedule.TimerKey == "" {
		schedule.TimerKey = request.ProcessID + ":" + request.NodeID
	}
	if schedule.Purpose == "" {
		schedule.Purpose = "resume:" + request.NodeID
	}
	payload, _ := json.Marshal(map[string]any{"process_id": request.ProcessID, "node_id": request.NodeID, "object_key": request.ObjectKey, "record_id": request.RecordID})
	schedule.PayloadJSON = string(payload)
	switch {
	case contract.DurationSeconds > 0 && schedule.BusinessCalendarKey == "":
		schedule.ScheduleMode, schedule.DueAt = "absolute", request.CreatedAt.Add(time.Duration(contract.DurationSeconds)*time.Second)
	case schedule.BusinessCalendarKey != "":
		schedule.ScheduleMode = "business_calendar"
		if strings.TrimSpace(contract.At) != "" {
			schedule.DueAt, _ = time.Parse(time.RFC3339Nano, strings.TrimSpace(contract.At))
		}
	case schedule.SourceField != "":
		schedule.ScheduleMode = "relative_field"
	default:
		schedule.ScheduleMode = "absolute"
		schedule.DueAt, _ = time.Parse(time.RFC3339Nano, strings.TrimSpace(contract.At))
	}
	sourceData := request.Variables
	if nested, ok := request.Variables["after"].(map[string]any); ok {
		sourceData = nested
	}
	timer, err := s.recordTimers.Schedule(ctx, request.WorkspaceID, schedule, recordmodel.Record{ID: request.RecordID, Data: sourceData}, recordtimerapplication.StandardRecordTimerBusinessCalendar{}, request.CreatedAt, principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, fmt.Sprintf("schedule workflow timer %s", request.NodeID)))
	if err != nil {
		return "", err
	}
	return timer.ID, nil
}

func (s runtimeWorkflowRecordTimers) ScheduleWorkflowApprovalDeadlineTimer(ctx context.Context, request workflowapplication.WorkflowApprovalDeadlineTimerRequest) (string, error) {
	phase := strings.TrimSpace(request.Phase)
	if phase != "reminder" && phase != "escalation" {
		return "", fmt.Errorf("unsupported workflow approval deadline phase %q", phase)
	}
	payload, _ := json.Marshal(map[string]any{"process_id": request.ProcessID, "node_id": request.NodeID, "task_id": request.TaskID, "phase": phase})
	timer, err := s.recordTimers.Schedule(ctx, request.WorkspaceID, recordtimerapplication.RecordTimerSchedule{
		TimerKey: request.TaskID + ":" + phase, ObjectKey: "workflow_task", RecordID: request.TaskID,
		Purpose: "approval_" + phase, ScheduleMode: "absolute", DueAt: request.DueAt, Timezone: "UTC",
		TargetType: "workflow", TargetKey: "approval_deadline", PayloadJSON: string(payload), Sequence: request.CreatedAt.UnixNano(),
	}, recordmodel.Record{ID: request.TaskID}, nil, request.CreatedAt, principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, fmt.Sprintf("schedule workflow approval %s timer", phase)))
	if err != nil {
		return "", err
	}
	return timer.ID, nil
}
