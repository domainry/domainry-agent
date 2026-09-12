package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentmodule "github.com/domainry/domainry-agent/module"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

func TestScheduleManagementBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_SCHEDULE_BROWSER") != "1" {
		t.Skip("opt-in built schedule management acceptance")
	}
	project, _ := filepath.Abs("../../..")
	output := os.Getenv("AGENT_UI_TEST_OUTPUT")
	if output == "" {
		t.Fatal("AGENT_UI_TEST_OUTPUT required")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	plans := &schedulePlanStore{}
	modelService := httptest.NewServer(http.HandlerFunc(scheduleModel))
	defer modelService.Close()
	f := newAccountFixture(t)
	f.close()
	f.options.ScheduleTools, f.options.ScheduledPlans = true, plans
	f.options.Agent = agentmodule.Options{ConversationURL: modelService.URL, ConversationModel: "schedule-browser-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond, MaxSteps: 6, MaxToolCalls: 4}}
	f.open()
	files := os.DirFS(filepath.Join(project, "frontend/dist"))
	admin := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	admin.login("admin@example.com", accountInitial)
	admin.changePassword(accountInitial, accountChanged)
	admin.call(http.MethodPost, "/app/product/account-setup", `{}`, http.StatusOK)
	admin.call(http.MethodPost, "/app/product/tool-settings-setup", `{}`, http.StatusOK)
	grantScheduleTools(t, f, admin, "admin")
	admin.call(http.MethodPost, "/app/product/plans", `{"kind":"background_task","name":"每周一整理待办","timezone":"Asia/Shanghai","trigger":{"type":"recurring","schedule":{"type":"weekly_at","time_of_day":"09:00","day_of_week":"monday"}},"details":{"goal":"整理本周待办","allowed_tools":["todo_list"]}}`, http.StatusOK)
	admin.call(http.MethodPost, "/app/product/plans", `{"kind":"reminder","name":"周五提醒我提交周报","timezone":"Asia/Shanghai","trigger":{"type":"recurring","schedule":{"type":"weekly_at","time_of_day":"17:00","day_of_week":"friday"}},"details":{"title":"周报提醒","message":"请提交本周周报"}}`, http.StatusOK)

	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler := f.boundary(origin, files)
	var gate sync.RWMutex
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__acceptance/") {
			gate.Lock()
			defer gate.Unlock()
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			switch r.URL.Path {
			case "/__acceptance/restart":
				f.close()
				f.open()
				handler = f.boundary(origin, files)
				admin.handler = f.boundary("http://127.0.0.1:8091", files)
				admin.login("admin@example.com", accountChanged)
			default:
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		gate.RLock()
		defer gate.RUnlock()
		handler.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/schedule-management.browser.mjs"))
	command.Dir = project
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatal("schedule management browser", err)
	}

	plans.mu.Lock()
	defer plans.mu.Unlock()
	visible := []schedulersdk.ScheduledPlan{}
	deleted := 0
	for _, plan := range plans.plans {
		if plan.Status == schedulersdk.ScheduledPlanStatusDeleted {
			deleted++
		} else {
			visible = append(visible, plan)
		}
	}
	if len(visible) != 1 || visible[0].Name != "每周二整理待办" || visible[0].Status != schedulersdk.ScheduledPlanStatusEnabled || visible[0].Revision != 4 || deleted != 1 {
		t.Fatalf("unexpected final plans visible=%+v deleted=%d", visible, deleted)
	}
	raw, _ := json.MarshalIndent(map[string]any{"complete": true, "compiled_frontend": true, "real_identity_http": true, "same_tool_adapter": true, "scheduler_sdk_service": true, "agent_restarted": true, "visible_plans": len(visible), "deleted_tombstones": deleted}, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "host-audit.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleFollowUpBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_FOLLOW_UP_BROWSER") != "1" {
		t.Skip("opt-in built follow-up acceptance")
	}
	project, _ := filepath.Abs("../../..")
	output := os.Getenv("AGENT_UI_TEST_OUTPUT")
	if output == "" {
		t.Fatal("AGENT_UI_TEST_OUTPUT required")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	plans := &schedulePlanStore{}
	modelService := httptest.NewServer(http.HandlerFunc(scheduleModel))
	defer modelService.Close()
	f := newAccountFixture(t)
	f.close()
	f.options.ScheduleTools, f.options.ScheduledPlans = true, plans
	f.options.Agent = agentmodule.Options{ConversationURL: modelService.URL, ConversationModel: "follow-up-browser-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond, MaxSteps: 6, MaxToolCalls: 4}}
	f.open()
	files := os.DirFS(filepath.Join(project, "frontend/dist"))
	admin := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	admin.login("admin@example.com", accountInitial)
	admin.changePassword(accountInitial, accountChanged)
	admin.call(http.MethodPost, "/app/product/account-setup", `{}`, http.StatusOK)
	admin.call(http.MethodPost, "/app/product/tool-settings-setup", `{}`, http.StatusOK)
	grantScheduleTools(t, f, admin, "admin")
	admin.call(http.MethodPost, "/app/product/plans", `{"kind":"follow_up","name":"跟进发布阻塞项","timezone":"Asia/Shanghai","trigger":{"type":"recurring","schedule":{"type":"daily_at","time_of_day":"09:00"}},"details":{"goal":"检查发布阻塞项","allowed_tools":["todo_list"],"completion_condition":"所有发布阻塞项均已关闭"}}`, http.StatusOK)

	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler := f.boundary(origin, files)
	var gate sync.RWMutex
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__acceptance/restart" {
			gate.Lock()
			defer gate.Unlock()
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			f.close()
			f.open()
			handler = f.boundary(origin, files)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		gate.RLock()
		defer gate.RUnlock()
		handler.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/schedule-follow-up.browser.mjs"))
	command.Dir = project
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatal("follow-up browser", err)
	}

	plans.mu.Lock()
	defer plans.mu.Unlock()
	plan := plans.plans["plan-1"]
	if len(plans.plans) != 1 || plan.Status != schedulersdk.ScheduledPlanStatusPaused || plan.Revision != 2 || !strings.Contains(string(plan.Input), `"completion_condition":"所有发布阻塞项均已关闭"`) {
		t.Fatalf("unexpected final plans %+v", plans.plans)
	}
	raw, _ := json.MarshalIndent(map[string]any{"complete": true, "compiled_frontend": true, "real_identity_http": true, "same_tool_adapter": true, "scheduler_sdk_service": true, "agent_restarted": true, "follow_up_status": "paused", "revision": 2}, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "host-audit.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
