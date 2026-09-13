package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
	scheduler "github.com/domainry/domainry-scheduler-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
)

type scheduleReceiptHTTPStore struct{ schedulePlanStore }

func (s *scheduleReceiptHTTPStore) ReadScheduledPlanDeletion(ctx context.Context, lookup scheduler.ScheduledPlanLookup) (scheduler.ScheduledPlanDeleteReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, err := s.get(lookup, true)
	if err != nil {
		return scheduler.ScheduledPlanDeleteReceipt{}, err
	}
	if plan.Status != scheduler.ScheduledPlanStatusDeleted {
		return scheduler.ScheduledPlanDeleteReceipt{}, scheduler.ErrScheduledPlanNotFound
	}
	return scheduler.ScheduledPlanDeleteReceipt{PlanID: plan.ID, Revision: plan.Revision, Deleted: true}, ctx.Err()
}

func TestPeerScheduleDeliveryReadsSevenReceiptsAfterWriteRevocation(t *testing.T) {
	const initial, changed = "Schedule-Receipt-Initial!26", "Schedule-Receipt-Changed!26"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "schedule-receipt-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "schedule-receipt-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	plans := &scheduleReceiptHTTPStore{}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "schedule-receipts.db"), RuntimeID: "schedule-receipt-runtime", WorkspaceID: "schedule-receipt-workspace", ApplicationKey: "schedule-receipt-app", ScheduleTools: true, ScheduledPlans: plans, Agent: agentmodule.Options{ConversationProvider: &taskReceiptHTTPModel{}, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
	var host *Host
	open := func() http.Handler {
		t.Helper()
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		adapters, err := host.ToolSettingsAdapters()
		if err != nil {
			t.Fatal(err)
		}
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("schedule receipt reading")}}, ModuleAdapters: adapters, ApplicationRoutes: host.ToolSettingsSetupRoutes()})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	b := &browser{t: t, handler: open(), cookies: map[string]*http.Cookie{}}
	defer func() { _ = host.Close(context.Background()) }()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	grantCollaborationPermissions(t, host, b)
	setScheduleGrants := func(write, read bool) {
		mutateTestRolePermissions(t, host, b, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			managed := map[string]bool{}
			for _, d := range tools.ScheduleDefinitions() {
				managed[d.ActionKey] = true
			}
			out := []identity.ProjectRolePermission{}
			for _, p := range prior {
				if !managed[p.PermissionKey] {
					out = append(out, p)
				}
			}
			for _, d := range tools.ScheduleDefinitions() {
				if d.Effect == "read" && read || d.Effect == "write" && write {
					out = append(out, identity.ProjectRolePermission{PermissionKey: d.ActionKey, DataScope: identity.DataScopeOwner})
				}
			}
			return out
		})
	}
	setScheduleGrants(true, true)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	agent := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "schedule-source", Name: "计划资料来源", Description: "保留计划来源说明", Instructions: "完成计划说明", Tools: []string{}, SkillKeys: []string{}, Enabled: true, MaxConcurrent: 1}), 200))
	issuer := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"schedule-issuer","title":"计划协作"}`, 200))
	var d sdk.ConversationDelegationDetail
	input := sdk.ConversationDelegationCreate{ClientID: "schedule-delegation", ConversationID: issuer.ID, AgentID: agent.ID, Purpose: "核对计划原回执", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "完成计划说明", Deliverable: "计划回执", CompletionConditions: []string{"提供七类原回执"}}}
	if err := unmarshalPeerDetail(b.call("POST", "/agent/delegations", accountJSON(input), 200).Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	path := "/agent/delegations/" + d.ID
	read := func() sdk.ConversationDelegationDetail {
		t.Helper()
		var v sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", path, "", 200).Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for deadline := time.Now().Add(60 * time.Second); ; {
		d = read()
		root := accountDecode[sdk.Conversation](t, b.call("GET", "/agent/conversations/"+issuer.ID, "", 200))
		if d.Task != nil && d.Task.Status == "completed" && root.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) || d.Task != nil && d.Task.Status == "failed" {
			t.Fatal("source did not complete", d)
		}
		time.Sleep(10 * time.Millisecond)
	}
	owner := scheduler.ScheduledPlanOwner{WorkspaceID: options.WorkspaceID, UserID: "admin", ProductKey: options.ApplicationKey}
	seed := func(key string) string {
		t.Helper()
		receipt, err := plans.CreateScheduledPlan(t.Context(), scheduler.ScheduledPlanCreate{ClientID: key, Name: "原计划 " + key, Owner: owner, Timezone: "Asia/Shanghai", Trigger: scheduler.ScheduledPlanTrigger{Type: "recurring", Schedule: &scheduler.Schedule{Type: "daily_at", TimeOfDay: "09:00", Timezone: "Asia/Shanghai"}}, Input: json.RawMessage(`{"title":"原提醒","message":"保留原始内容"}`), AllowedActions: []string{"notification.reminder.publish"}, Target: scheduler.TargetRef{Type: "runtime_operation", Owner: "notification", Operation: "publish_reminder"}, ConversationRef: scheduler.ScheduledPlanConversationRef{ConversationID: d.ConversationID, RunID: d.Task.ExecutionRunID}})
		if err != nil {
			t.Fatal(err)
		}
		return receipt.Plan.ID
	}
	refs := []sdk.ConversationResultReference{}
	original := map[string]string{}
	confirmations := 0
	for _, key := range []string{"schedule_create", "schedule_get", "schedule_update", "schedule_pause", "schedule_resume", "schedule_delete", "schedule_list"} {
		args := `{}`
		if key == "schedule_create" {
			args = `{"kind":"reminder","name":"实际创建的提醒","timezone":"Asia/Shanghai","trigger":{"type":"recurring","schedule":{"type":"daily_at","time_of_day":"09:00"}},"details":{"title":"实际提醒","message":"原始提醒内容"}}`
		} else if key != "schedule_list" {
			id := seed(key)
			revision := int64(1)
			if key == "schedule_resume" {
				if _, err := plans.PauseScheduledPlan(t.Context(), scheduler.ScheduledPlanStatusChange{Owner: owner, PlanID: id, ExpectedRevision: 1}); err != nil {
					t.Fatal(err)
				}
				revision = 2
			}
			switch key {
			case "schedule_get":
				args = accountJSON(map[string]any{"plan_id": id})
			case "schedule_update":
				args = accountJSON(map[string]any{"plan_id": id, "expected_revision": revision, "name": "更新后的计划"})
			default:
				args = accountJSON(map[string]any{"plan_id": id, "expected_revision": revision})
			}
		}
		conversation := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: key, Title: key}), 200))
		base := "/agent/conversations/" + conversation.ID
		call := sdk.ConversationToolCall{ID: key, Name: key, Arguments: args}
		run := accountDecode[sdk.ConversationRun](t, b.call("POST", base+"/messages", accountJSON(sdk.ConversationSend{ClientMessageID: key, Message: "Task receipt call: " + accountJSON(call)}), 202))
		runPath := base + "/runs/" + run.ID
		responded := map[string]bool{}
		for deadline := time.Now().Add(60 * time.Second); !run.Terminal(); {
			if run.Status == "waiting_confirmation" && run.Interaction != nil && !responded[run.Interaction.ID] {
				i := run.Interaction
				b.call("POST", runPath+"/respond", accountJSON(sdk.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "approve-" + key, ExpectedRevision: i.Revision, Decision: "approve"}), 200)
				responded[i.ID] = true
				confirmations++
			}
			if time.Now().After(deadline) {
				t.Fatal("plan call timeout", key, run)
			}
			time.Sleep(10 * time.Millisecond)
			run = accountDecode[sdk.ConversationRun](t, b.call("GET", runPath, "", 200))
		}
		if run.Status != "completed" || run.AccessError != "" {
			t.Fatal("plan call failed", key, run)
		}
		for _, step := range run.Steps {
			for _, actual := range step.Calls {
				if actual.ID == key && actual.Status == "completed" && actual.ResultReference != nil {
					refs = append(refs, *actual.ResultReference)
				}
			}
		}
	}
	if len(refs) != 7 {
		t.Fatal("expected seven actual receipts", len(refs))
	}
	t.Logf("Seven real calls completed; current host requested %d confirmations", confirmations)
	d = read()
	update := sdk.ConversationDelegationUpdate{ClientID: "deliver-plans", ExpectedRevision: d.Revision, Action: "deliver", Reason: "核对原计划回执", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, Summary: "七类计划回执已核对", Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "七次实际工具执行", Receipts: refs}}}}
	b.call("POST", path+"/decisions", accountJSON(update), 200)
	for _, ref := range refs {
		original[ref.CallID] = string(readReleasedResult(t, b, d.ID, 0, ref).Content)
	}
	plans.mu.Lock()
	before := accountJSON(plans.plans)
	plans.mu.Unlock()
	grantPersonalTools(t, host, b, false)
	setScheduleGrants(false, true)
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read", "execution_read", "communicate")
	assertRead := func() {
		t.Helper()
		if value := read(); value.Delivery == nil {
			t.Fatal("plan delivery hidden", value)
		}
		history := accountDecode[sdk.ConversationDeliveryHistory](t, b.call("GET", path+"/deliveries", "", 200))
		if len(history.Items) == 0 {
			t.Fatal("missing delivery history")
		}
		for _, ref := range refs {
			for _, revision := range []int64{0, history.Items[0].Revision} {
				if got := readReleasedResultPages(t, b, d.ID, revision, ref, 256); string(got.Content) != original[ref.CallID] {
					t.Fatal("plan receipt changed", ref.CallID)
				}
			}
		}
		plans.mu.Lock()
		after := accountJSON(plans.plans)
		plans.mu.Unlock()
		if before != after {
			t.Fatal("reading mutated plans")
		}
	}
	assertRead()
	ref := refs[0]
	b.call("POST", fmt.Sprintf("/agent/conversations/%s/runs/%s/result", ref.ConversationID, ref.RunID), accountJSON(sdk.ConversationResultRead{Reference: ref}), 403)
	setScheduleGrants(false, false)
	if value := read(); value.Delivery != nil {
		t.Fatal("revoked plan data permission disclosed delivery")
	}
	b.call("POST", path+"/delivery-result", accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: refs[0]}}), 403)
	setScheduleGrants(false, true)
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read", "communicate")
	if value := read(); value.Delivery != nil {
		t.Fatal("plan origin bypassed private execution permission")
	}
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read", "execution_read", "communicate")
	assertRead()
	// Preferences expose only the current executable catalog. Change the
	// preference while that grant exists, then test reading after withdrawal.
	setScheduleGrants(true, true)
	setting := settingList(t, b)["schedule_delete"]
	b.call("PUT", "/tools/preferences/schedule_delete", accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	setScheduleGrants(false, true)
	if value := read(); value.Delivery != nil {
		t.Fatal("disabled schedule tool disclosed receipt")
	}
	setScheduleGrants(true, true)
	setting = settingList(t, b)["schedule_delete"]
	b.call("PUT", "/tools/preferences/schedule_delete", accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	setScheduleGrants(false, true)
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.handler = open()
	b.login("admin@example.com", changed)
	assertRead()
}
