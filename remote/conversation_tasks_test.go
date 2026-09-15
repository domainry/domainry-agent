package remote

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/server"
)

type remoteConversationTaskService struct {
	releasedResultService
	operation string
	query     sdk.ConversationTaskQuery
	update    sdk.ConversationTaskAgreementUpdate
	before    int64
}

func (s *remoteConversationTaskService) task(id, operation string, a sdk.ConversationAuthority) (sdk.ConversationTaskDetail, error) {
	s.calls++
	s.id, s.operation, s.authority = id, operation, a
	if s.denied {
		return sdk.ConversationTaskDetail{}, &sdk.Error{Class: "forbidden", Code: "agent.conversation.task_access_denied"}
	}
	return sdk.ConversationTaskDetail{ConversationTaskSummary: sdk.ConversationTaskSummary{ID: id, Goal: operation, AgreementRevision: 2}}, nil
}

func (s *remoteConversationTaskService) ConversationTask(_ context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTaskDetail, error) {
	return s.task(id, "get", a)
}

func (s *remoteConversationTaskService) ConversationTasks(_ context.Context, in sdk.ConversationTaskQuery, a sdk.ConversationAuthority) (sdk.ConversationTaskPage, error) {
	s.calls++
	s.operation, s.query, s.authority = "list", in, a
	return sdk.ConversationTaskPage{Items: []sdk.ConversationTaskSummary{{ID: "task", Goal: in.Query}}, Complete: true}, nil
}

func (s *remoteConversationTaskService) CancelConversationTask(_ context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTaskDetail, error) {
	return s.task(id, "cancel", a)
}

func (s *remoteConversationTaskService) ResumeConversationTask(_ context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTaskDetail, error) {
	return s.task(id, "resume", a)
}

func (s *remoteConversationTaskService) UpdateConversationTaskAgreement(_ context.Context, id string, in sdk.ConversationTaskAgreementUpdate, a sdk.ConversationAuthority) (sdk.ConversationTaskDetail, error) {
	s.update = in
	return s.task(id, "update", a)
}

func (s *remoteConversationTaskService) ConversationTaskPlans(_ context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationPlanHistory, error) {
	s.calls++
	s.id, s.operation, s.before, s.authority = id, "plans", before, a
	return sdk.ConversationPlanHistory{Items: []sdk.ConversationPlan{{TaskID: id, Version: before - 1}}, Complete: true}, nil
}

func TestSaaSConversationTasksPreserveAgreementAndActualAuthority(t *testing.T) {
	source := &remoteConversationTaskService{}
	svc, err := server.New(server.Config{APIKey: "task-runtime-secret", Conversations: source, ConversationRuntimeID: "runtime", ConversationWorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(svc.Handler())
	defer httpServer.Close()
	opened, err := NewFactory(Options{BaseURL: httpServer.URL, APIKey: "task-runtime-secret", Client: httpServer.Client()}).OpenSaaS(t.Context(), sdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close(context.Background())
	client := opened.(sdk.ConversationBinding).Conversations()
	reader := client.(sdk.ConversationTaskService)
	controls := client.(sdk.ConversationTaskControlService)
	agreements := client.(sdk.ConversationTaskAgreementService)
	plans := client.(sdk.ConversationTaskPlanService)
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "owner", RoleKey: "actual-role"}

	if task, err := reader.ConversationTask(t.Context(), "task", a); err != nil || task.ID != "task" || source.operation != "get" || source.authority != a {
		t.Fatal("task get changed request", task, err)
	}
	query := sdk.ConversationTaskQuery{Query: "release", Status: sdk.ConversationTaskStatusFailed, SourceConversationID: "conversation", Cursor: "cursor", Limit: 7}
	if page, err := reader.ConversationTasks(t.Context(), query, a); err != nil || len(page.Items) != 1 || page.Items[0].Goal != query.Query || source.query != query || source.authority != a {
		t.Fatal("task list changed request", page, err)
	}
	if task, err := controls.CancelConversationTask(t.Context(), "task", a); err != nil || task.Goal != "cancel" || source.operation != "cancel" {
		t.Fatal("task cancel changed request", task, err)
	}
	if task, err := controls.ResumeConversationTask(t.Context(), "task", a); err != nil || task.Goal != "resume" || source.operation != "resume" {
		t.Fatal("task resume changed request", task, err)
	}
	brief := sdk.DefaultConversationTaskBrief("核对新版发布")
	brief.Version = 2
	update := sdk.ConversationTaskAgreementUpdate{ClientID: "agreement-v2", ExpectedRevision: 1, Reason: "用户调整发布范围", Brief: brief}
	if task, err := agreements.UpdateConversationTaskAgreement(t.Context(), "task", update, a); err != nil || task.Goal != "update" || !reflect.DeepEqual(source.update, update) || source.operation != "update" || source.authority != a {
		t.Fatal("task agreement changed request", task, err)
	}
	if history, err := plans.ConversationTaskPlans(t.Context(), "task", 5, a); err != nil || len(history.Items) != 1 || history.Items[0].TaskID != "task" || history.Items[0].Version != 4 || source.operation != "plans" || source.before != 5 || source.authority != a {
		t.Fatal("task plan history changed request", history, err)
	}

	source.denied = true
	if task, err := agreements.UpdateConversationTaskAgreement(t.Context(), "task", update, a); err == nil || task.ID != "" {
		t.Fatal("current agreement denial was hidden", task, err)
	}
	before := source.calls
	a.WorkspaceID = "foreign"
	if _, err := reader.ConversationTask(t.Context(), "task", a); err == nil || source.calls != before {
		t.Fatal("foreign workspace reached task service", err)
	}
}
