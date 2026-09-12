package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

type schedulePlanStore struct {
	mu      sync.Mutex
	plans   map[string]schedulersdk.ScheduledPlan
	clients map[string]string
}

func (s *schedulePlanStore) CreateScheduledPlan(_ context.Context, input schedulersdk.ScheduledPlanCreate) (schedulersdk.ScheduledPlanReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id := s.clients[input.ClientID]; id != "" {
		return schedulersdk.ScheduledPlanReceipt{Plan: s.plans[id], Replay: true}, nil
	}
	if s.plans == nil {
		s.plans, s.clients = map[string]schedulersdk.ScheduledPlan{}, map[string]string{}
	}
	id := fmt.Sprintf("plan-%d", len(s.plans)+1)
	now := time.Now().UTC()
	plan := schedulersdk.ScheduledPlan{ID: id, Name: input.Name, Owner: input.Owner, Timezone: input.Timezone, Trigger: input.Trigger, Input: append(json.RawMessage(nil), input.Input...), AllowedActions: append([]string(nil), input.AllowedActions...), Target: input.Target, ConversationRef: input.ConversationRef, Status: schedulersdk.ScheduledPlanStatusEnabled, Revision: 1, CreatedAt: now, UpdatedAt: now}
	s.plans[id], s.clients[input.ClientID] = plan, id
	return schedulersdk.ScheduledPlanReceipt{Plan: plan}, nil
}
func (s *schedulePlanStore) get(input schedulersdk.ScheduledPlanLookup, includeDeleted bool) (schedulersdk.ScheduledPlan, error) {
	plan, found := s.plans[input.PlanID]
	if !found || plan.Owner != input.Owner || !includeDeleted && plan.Status == schedulersdk.ScheduledPlanStatusDeleted {
		return schedulersdk.ScheduledPlan{}, schedulersdk.ErrScheduledPlanNotFound
	}
	return plan, nil
}
func (s *schedulePlanStore) GetScheduledPlan(_ context.Context, input schedulersdk.ScheduledPlanLookup) (schedulersdk.ScheduledPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(input, false)
}
func (s *schedulePlanStore) ListScheduledPlans(_ context.Context, input schedulersdk.ScheduledPlanList) (schedulersdk.ScheduledPlanPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []schedulersdk.ScheduledPlan{}
	for _, plan := range s.plans {
		if plan.Owner == input.Owner && plan.Status != schedulersdk.ScheduledPlanStatusDeleted && (input.Status == "" || input.Status == plan.Status) && (input.Cursor == "" || plan.ID > input.Cursor) {
			items = append(items, plan)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return schedulersdk.ScheduledPlanPage{Items: items}, nil
}
func (s *schedulePlanStore) UpdateScheduledPlan(_ context.Context, input schedulersdk.ScheduledPlanUpdate) (schedulersdk.ScheduledPlanReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, err := s.get(schedulersdk.ScheduledPlanLookup{Owner: input.Owner, PlanID: input.PlanID}, false)
	if err != nil {
		return schedulersdk.ScheduledPlanReceipt{}, err
	}
	if plan.Revision != input.ExpectedRevision {
		return schedulersdk.ScheduledPlanReceipt{}, schedulersdk.ErrScheduledPlanConflict
	}
	plan.Name, plan.Timezone, plan.Trigger, plan.Input, plan.AllowedActions, plan.Target, plan.ConversationRef = input.Name, input.Timezone, input.Trigger, append(json.RawMessage(nil), input.Input...), append([]string(nil), input.AllowedActions...), input.Target, input.ConversationRef
	plan.Revision++
	plan.UpdatedAt = time.Now().UTC()
	s.plans[plan.ID] = plan
	return schedulersdk.ScheduledPlanReceipt{Plan: plan}, nil
}
func (s *schedulePlanStore) change(input schedulersdk.ScheduledPlanStatusChange, status string) (schedulersdk.ScheduledPlanReceipt, error) {
	plan, err := s.get(schedulersdk.ScheduledPlanLookup{Owner: input.Owner, PlanID: input.PlanID}, true)
	if err != nil {
		return schedulersdk.ScheduledPlanReceipt{}, err
	}
	if plan.Revision == input.ExpectedRevision+1 && plan.Status == status {
		return schedulersdk.ScheduledPlanReceipt{Plan: plan, Replay: true}, nil
	}
	if plan.Status == schedulersdk.ScheduledPlanStatusDeleted || plan.Revision != input.ExpectedRevision {
		return schedulersdk.ScheduledPlanReceipt{}, schedulersdk.ErrScheduledPlanConflict
	}
	plan.Status, plan.Revision, plan.UpdatedAt = status, plan.Revision+1, time.Now().UTC()
	s.plans[plan.ID] = plan
	return schedulersdk.ScheduledPlanReceipt{Plan: plan}, nil
}
func (s *schedulePlanStore) PauseScheduledPlan(_ context.Context, input schedulersdk.ScheduledPlanStatusChange) (schedulersdk.ScheduledPlanReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.change(input, schedulersdk.ScheduledPlanStatusPaused)
}
func (s *schedulePlanStore) ResumeScheduledPlan(_ context.Context, input schedulersdk.ScheduledPlanStatusChange) (schedulersdk.ScheduledPlanReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.change(input, schedulersdk.ScheduledPlanStatusEnabled)
}
func (s *schedulePlanStore) DeleteScheduledPlan(_ context.Context, input schedulersdk.ScheduledPlanStatusChange) (schedulersdk.ScheduledPlanDeleteReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, err := s.change(input, schedulersdk.ScheduledPlanStatusDeleted)
	return schedulersdk.ScheduledPlanDeleteReceipt{PlanID: input.PlanID, Revision: receipt.Plan.Revision, Deleted: err == nil, Replay: receipt.Replay}, err
}

func scheduleModel(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Tools    []struct{ Function struct{ Name string } }
		Messages []struct {
			Role, Content string
			CallID        string `json:"tool_call_id"`
		}
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Messages) == 0 {
		http.Error(w, "bad model input", http.StatusBadRequest)
		return
	}
	available := map[string]bool{}
	for _, definition := range input.Tools {
		available[definition.Function.Name] = true
	}
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(delta any, finish string) {
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
		w.(http.Flusher).Flush()
	}
	defer fmt.Fprint(w, "data: [DONE]\n\n")
	last := input.Messages[len(input.Messages)-1]
	if last.Role == "tool" {
		emit(map[string]any{"content": "计划已按当前账号和产品范围保存。"}, "stop")
		return
	}
	if !available[toolsdk.ScheduleCreateToolKey] {
		emit(map[string]any{"content": "计划工具不可用。"}, "stop")
		return
	}
	arguments := `{"kind":"background_task","name":"每周一整理待办","timezone":"Asia/Shanghai","trigger":{"type":"recurring","schedule":{"type":"weekly_at","time_of_day":"09:00","day_of_week":"monday"}},"details":{"goal":"整理本周待办","allowed_tools":["todo_list"]}}`
	if strings.Contains(last.Content, "周五提醒我") {
		arguments = `{"kind":"reminder","name":"周五提醒我提交周报","timezone":"Asia/Shanghai","trigger":{"type":"recurring","schedule":{"type":"weekly_at","time_of_day":"17:00","day_of_week":"friday"}},"details":{"title":"周报提醒","message":"请提交本周周报"}}`
	}
	emit(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "schedule-create", "type": "function", "function": map[string]any{"name": toolsdk.ScheduleCreateToolKey, "arguments": arguments}}}}, "tool_calls")
}

func grantScheduleTools(t *testing.T, f *accountFixture, admin *browser, user string) {
	t.Helper()
	mutateTestRolePermissions(t, f.host, admin, func(prior []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		out := []identitysdk.ProjectRolePermission{}
		managed := map[string]bool{}
		for _, definition := range agentsdk.PersonalConversationTools() {
			if definition.Key == "todo_list" {
				managed[definition.ActionKey] = true
			}
		}
		for _, definition := range toolmodule.ScheduleDefinitions() {
			managed[definition.ActionKey] = true
		}
		for _, route := range toolsdk.ToolSettingsRoutes() {
			managed[route.Action.Permission.Key] = true
		}
		for _, permission := range prior {
			if !managed[permission.PermissionKey] {
				out = append(out, permission)
			}
		}
		for key := range managed {
			out = append(out, identitysdk.ProjectRolePermission{PermissionKey: key, DataScope: identitysdk.DataScopeOwner})
		}
		return out
	}, user)
}

func runSchedulePhrase(t *testing.T, browser *browser, phrase string) agentsdk.ConversationRun {
	t.Helper()
	id := fmt.Sprintf("schedule-%d", time.Now().UnixNano())
	conversation := accountDecode[agentsdk.Conversation](t, browser.call(http.MethodPost, "/agent/conversations", accountJSON(agentsdk.ConversationCreate{ClientID: id, Title: phrase}), http.StatusOK))
	run := accountDecode[agentsdk.ConversationRun](t, browser.call(http.MethodPost, "/agent/conversations/"+conversation.ID+"/messages", accountJSON(agentsdk.ConversationSend{ClientMessageID: id, Message: phrase}), http.StatusAccepted))
	path := "/agent/conversations/" + conversation.ID + "/runs/" + run.ID
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		run = accountDecode[agentsdk.ConversationRun](t, browser.call(http.MethodGet, path, "", http.StatusOK))
		if run.Status == "waiting_confirmation" {
			if run.Interaction == nil || run.Interaction.Tool != toolsdk.ScheduleCreateToolKey {
				t.Fatalf("unexpected confirmation=%+v", run.Interaction)
			}
			browser.call(http.MethodPost, path+"/respond", accountJSON(agentsdk.ConversationInteractionResponse{InteractionID: run.Interaction.ID, ClientID: "approve-" + id, ExpectedRevision: run.Interaction.Revision, Decision: "approve"}), http.StatusOK)
		} else if run.Status == "needs_reconciliation" {
			browser.call(http.MethodPost, path+"/resume", "", http.StatusOK)
		} else if run.Terminal() {
			if run.Status != "completed" {
				t.Fatalf("schedule phrase run=%+v", run)
			}
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("schedule phrase timed out: %+v", run)
	return run
}

func TestScheduleNaturalLanguageAndProductManagementEndToEnd(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(scheduleModel))
	defer model.Close()
	plans := &schedulePlanStore{}
	f := newAccountFixture(t)
	f.close()
	f.options.ScheduleTools, f.options.ScheduledPlans = true, plans
	f.options.Agent = agentmodule.Options{ConversationURL: model.URL, ConversationModel: "schedule-protocol-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond, MaxSteps: 6, MaxToolCalls: 4}}
	f.open()
	files := fstest.MapFS{"index.html": {Data: []byte("schedule")}, "oauth-callback.html": {Data: []byte("callback")}}
	b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", accountInitial)
	b.changePassword(accountInitial, accountChanged)
	b.call(http.MethodPost, "/app/product/account-setup", `{}`, http.StatusOK)
	b.call(http.MethodPost, "/app/product/tool-settings-setup", `{}`, http.StatusOK)
	grantScheduleTools(t, f, b, "admin")
	settings := settingList(t, b)
	if !settings[toolsdk.ScheduleCreateToolKey].Available || !settings["todo_list"].Available {
		t.Fatalf("schedule catalog=%+v", settings)
	}
	definition, _ := scheduleDefinition(toolsdk.ScheduleCreateToolKey)
	probeRequest := toolsdk.Request{Authority: toolsdk.Authority{Known: true, RuntimeID: f.options.RuntimeID, WorkspaceID: f.options.WorkspaceID, UserID: "admin"}, ConversationID: "probe", RunID: "probe", Call: toolsdk.Call{ID: "probe", Name: definition.Key, Arguments: `{"kind":"background_task","name":"probe","timezone":"Asia/Shanghai","trigger":{"type":"recurring","schedule":{"type":"weekly_at","time_of_day":"09:00","day_of_week":"monday"}},"details":{"goal":"probe","allowed_tools":["todo_list"]}}`}, Definition: definition, IdempotencyKey: "probe"}
	probe, err := f.host.scheduleToolHost.InvokeConversationTool(t.Context(), probeRequest)
	if err != nil || probe.Status != "completed" {
		t.Fatalf("direct schedule composition result=%+v err=%v", probe, err)
	}
	if authorizer, ok := f.host.scheduleToolHost.(toolsdk.ResultAuthorizer); ok {
		if err := authorizer.AuthorizeConversationToolResult(t.Context(), probeRequest, probe); err != nil {
			t.Fatalf("direct schedule result authorization err=%v content=%s", err, probe.Content)
		}
	}
	var probeOutput struct {
		Plan struct{ ID string } `json:"plan"`
	}
	_ = json.Unmarshal(probe.Content, &probeOutput)
	_, _ = plans.DeleteScheduledPlan(t.Context(), schedulersdk.ScheduledPlanStatusChange{Owner: schedulersdk.ScheduledPlanOwner{WorkspaceID: f.options.WorkspaceID, UserID: "admin", ProductKey: f.options.ApplicationKey}, PlanID: probeOutput.Plan.ID, ExpectedRevision: 1})
	runSchedulePhrase(t, b, "每周一整理待办")
	runSchedulePhrase(t, b, "周五提醒我提交周报")

	list := accountDecode[struct {
		Items []struct {
			ID, Name, Kind, Status string
			Revision               int64
		} `json:"items"`
	}](t, b.call(http.MethodGet, "/app/product/plans?limit=50", "", http.StatusOK))
	if len(list.Items) != 2 {
		t.Fatalf("plans=%+v", list.Items)
	}
	var task, reminder struct {
		ID, Name, Kind, Status string
		Revision               int64
	}
	for _, item := range list.Items {
		if item.Kind == "background_task" {
			task = item
		} else if item.Kind == "reminder" {
			reminder = item
		}
	}
	if task.ID == "" || reminder.ID == "" {
		t.Fatalf("natural-language mapping=%+v", list.Items)
	}
	updated := accountDecode[struct {
		Plan struct {
			Name, Status string
			Revision     int64
		} `json:"plan"`
	}](t, b.call(http.MethodPut, "/app/product/plans/"+task.ID, `{"expected_revision":1,"name":"每周二整理待办","timezone":"Asia/Shanghai","trigger":{"type":"recurring","schedule":{"type":"weekly_at","time_of_day":"10:30","day_of_week":"tuesday"}},"details":{"goal":"整理本周待办","allowed_tools":["todo_list"]}}`, http.StatusOK))
	if updated.Plan.Name != "每周二整理待办" || updated.Plan.Revision != 2 {
		t.Fatalf("updated=%+v", updated)
	}
	b.call(http.MethodPut, "/app/product/plans/"+task.ID, `{"expected_revision":1,"name":"stale"}`, http.StatusConflict)
	paused := accountDecode[struct {
		Plan struct {
			Status   string
			Revision int64
		} `json:"plan"`
	}](t, b.call(http.MethodPost, "/app/product/plans/"+task.ID+"/pause", `{"expected_revision":2}`, http.StatusOK))
	if paused.Plan.Status != schedulersdk.ScheduledPlanStatusPaused || paused.Plan.Revision != 3 {
		t.Fatalf("paused=%+v", paused)
	}
	resumed := accountDecode[struct {
		Plan struct {
			Status   string
			Revision int64
		} `json:"plan"`
	}](t, b.call(http.MethodPost, "/app/product/plans/"+task.ID+"/resume", `{"expected_revision":3}`, http.StatusOK))
	if resumed.Plan.Status != schedulersdk.ScheduledPlanStatusEnabled || resumed.Plan.Revision != 4 {
		t.Fatalf("resumed=%+v", resumed)
	}
	deleted := accountDecode[struct {
		Deleted  bool
		Revision int64
	}](t, b.call(http.MethodDelete, "/app/product/plans/"+reminder.ID, `{"expected_revision":1}`, http.StatusOK))
	if !deleted.Deleted || deleted.Revision != 2 {
		t.Fatalf("deleted=%+v", deleted)
	}
	b.call(http.MethodGet, "/app/product/plans/"+reminder.ID, "", http.StatusNotFound)
	b.call(http.MethodPost, "/app/product/plans", `{"kind":"reminder","name":"bad","owner":{"user_id":"other"},"trigger":{"type":"once","at":"2026-09-18T09:00:00Z"},"details":{"title":"x","message":"y"}}`, http.StatusBadRequest)

	other := &browser{t: t, handler: b.handler, cookies: map[string]*http.Cookie{}}
	other.login("system_administrator@example.com", accountInitial)
	other.changePassword(accountInitial, accountChanged)
	otherID := other.readSession()["user_id"].(string)
	grantScheduleTools(t, f, b, otherID)
	otherList := accountDecode[struct {
		Items []json.RawMessage `json:"items"`
	}](t, other.call(http.MethodGet, "/app/product/plans?limit=50", "", http.StatusOK))
	if len(otherList.Items) != 0 {
		t.Fatalf("cross-user plans=%s", accountJSON(otherList))
	}
	other.call(http.MethodGet, "/app/product/plans/"+task.ID, "", http.StatusNotFound)

	f.close()
	f.open()
	b.handler = f.boundary("http://127.0.0.1:8091", files)
	b.login("admin@example.com", accountChanged)
	afterRestart := accountDecode[struct {
		Items []json.RawMessage `json:"items"`
	}](t, b.call(http.MethodGet, "/app/product/plans?limit=50", "", http.StatusOK))
	if len(afterRestart.Items) != 1 {
		t.Fatalf("Agent restart plans=%s", accountJSON(afterRestart))
	}
	if _, err := plans.GetScheduledPlan(t.Context(), schedulersdk.ScheduledPlanLookup{Owner: schedulersdk.ScheduledPlanOwner{WorkspaceID: f.options.WorkspaceID, UserID: "admin", ProductKey: f.options.ApplicationKey}, PlanID: reminder.ID}); !errors.Is(err, schedulersdk.ErrScheduledPlanNotFound) {
		t.Fatalf("deleted plan revived err=%v", err)
	}
}

var _ schedulersdk.ScheduledPlanService = (*schedulePlanStore)(nil)
