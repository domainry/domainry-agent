package web

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

func waitAccountWriteRun(t *testing.T, b *browser, path, status string) sdk.ConversationRun {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	var last sdk.ConversationRun
	for time.Now().Before(deadline) {
		run := accountDecode[sdk.ConversationRun](t, b.call("GET", path, "", 200))
		last = run
		if run.Status == status {
			return run
		}
		if run.Terminal() {
			t.Fatalf("expected %s, got %s/%s: %+v", status, run.Status, run.ErrorCode, run)
		}
		time.Sleep(400 * time.Millisecond)
	}
	t.Fatalf("run did not reach %s; last=%+v", status, last)
	return sdk.ConversationRun{}
}
func startAccountWrite(t *testing.T, b *browser, message string) (string, sdk.ConversationRun) {
	t.Helper()
	c := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", accountJSON(map[string]string{"client_id": "account-write-flow"}), 200))
	run := accountDecode[sdk.ConversationRun](t, b.call("POST", "/agent/conversations/"+c.ID+"/messages", accountJSON(map[string]string{"client_message_id": "start", "message": message}), 202))
	path := "/agent/conversations/" + c.ID + "/runs/" + run.ID
	return path, waitAccountWriteRun(t, b, path, "waiting_confirmation")
}

func TestAccountWritesThroughIdentityProviderHTTPAndRestart(t *testing.T) {
	for _, scenario := range []string{"approved_restart", "single_then_reject", "revoke_before_approval", "unknown_original_receipt"} {
		t.Run(scenario, func(t *testing.T) {
			f := newAccountWriteProductFixture(t)
			defer func() {
				t.Logf("vendor HTTP=%d model HTTP=%d effects=%v", f.requests.Load(), f.modelRequests.Load(), f.counts())
			}()
			files := fstest.MapFS{"index.html": {Data: []byte("account-write")}, "oauth-callback.html": {Data: []byte("callback")}}
			b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
			b.login("admin@example.com", accountInitial)
			b.changePassword(accountInitial, accountChanged)
			b.call("POST", "/app/product/account-setup", `{}`, 200)
			b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
			grantAccountWriteConfirmation(t, f.host, b)
			defs := append(toolmodule.CalendarWriteDefinitions(), toolmodule.MailWriteDefinitions()...)
			initialSettings := settingList(t, b)
			for _, d := range defs {
				if setting := initialSettings[d.Key]; setting.Available || setting.State != "connection_unavailable" {
					t.Fatalf("unconfigured %s %+v", d.Key, setting)
				}
			}
			account := f.connect(b)
			settings := settingList(t, b)
			for _, d := range defs {
				if !settings[d.Key].Available {
					t.Fatalf("granted write unavailable %s %+v", d.Key, settings[d.Key])
				}
			}
			restart := func() {
				f.close()
				f.open()
				b.handler = f.boundary("http://127.0.0.1:8091", files)
				b.login("admin@example.com", accountChanged)
			}
			message := "创建日程、清空原事件说明地点参与者，并发送和回复完整邮件"
			if scenario == "unknown_original_receipt" {
				message = "未知发送：受理后断线"
			}
			path, waiting := startAccountWrite(t, b, message)
			if waiting.Interaction == nil || len(f.counts()) != 0 {
				t.Fatal("missing confirmation or effect before approval")
			}
			if scenario != "unknown_original_receipt" && len(waiting.Interaction.Operations) != 4 {
				t.Fatalf("not an exact four-operation list: %+v", waiting.Interaction)
			}
			for _, operation := range waiting.Interaction.Operations {
				var args struct {
					Key      string `json:"account_key"`
					Revision string `json:"account_updated_at"`
				}
				_ = json.Unmarshal([]byte(operation.Arguments), &args)
				if args.Key != account.Key || args.Revision != account.UpdatedAt {
					t.Fatal("confirmation changed account target")
				}
			}
			response := sdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "approval", ExpectedRevision: waiting.Interaction.Revision, Decision: "approve", Scope: "listed_operations"}
			if scenario == "single_then_reject" || scenario == "unknown_original_receipt" {
				response.Scope = ""
			}
			if scenario == "approved_restart" {
				restart()
				reloaded := waitAccountWriteRun(t, b, path, "waiting_confirmation")
				if !reflect.DeepEqual(reloaded.Interaction, waiting.Interaction) || len(f.counts()) != 0 {
					t.Fatal("restart lost exact pending approval")
				}
			}
			if scenario == "revoke_before_approval" {
				mutateTestRolePermissions(t, f.host, b, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
					out := []identity.ProjectRolePermission{}
					for _, p := range prior {
						if p.PermissionKey != integration.ActionIntegrationConnectionAccountsWrite {
							out = append(out, p)
						}
					}
					return out
				})
				b.call("POST", path+"/respond", accountJSON(response), 403)
				if len(f.counts()) != 0 {
					t.Fatal("revoked write permission had an effect")
				}
				b.call("POST", "/app/product/account-setup", `{}`, 200)
				response.Decision = "reject"
				response.Scope = ""
				b.call("POST", path+"/respond", accountJSON(response), 200)
				waitAccountWriteRun(t, b, path, "cancelled")
				return
			}
			b.call("POST", path+"/respond", accountJSON(response), 200)
			b.call("POST", path+"/respond", accountJSON(response), 200)
			if scenario == "single_then_reject" {
				next := waitAccountWriteRun(t, b, path, "waiting_confirmation")
				if next.Interaction.CallID != "aw-update" || !reflect.DeepEqual(f.counts(), map[string]int{"calendar_create": 1}) {
					t.Fatal("single approval expanded", f.counts())
				}
				b.call("POST", path+"/respond", accountJSON(sdk.ConversationInteractionResponse{InteractionID: next.Interaction.ID, ClientID: "reject-rest", ExpectedRevision: next.Interaction.Revision, Decision: "reject"}), 200)
				waitAccountWriteRun(t, b, path, "cancelled")
				return
			}
			if scenario == "unknown_original_receipt" {
				first := waitAccountWriteRun(t, b, path, "needs_reconciliation")
				before := f.requests.Load()
				restart()
				for n := 0; n < 2; n++ {
					b.call("POST", path+"/resume", "", 200)
					next := waitAccountWriteRun(t, b, path, "needs_reconciliation")
					if next.ID != first.ID {
						t.Fatal("reconciliation changed run")
					}
				}
				if !reflect.DeepEqual(f.counts(), map[string]int{"mail_send": 1}) || before != f.requests.Load() {
					t.Fatalf("unknown replay called vendor: %+v %d->%d", f.counts(), before, f.requests.Load())
				}
				return
			}
			done := waitAccountWriteRun(t, b, path, "completed")
			if !reflect.DeepEqual(f.counts(), map[string]int{"calendar_create": 1, "calendar_update": 1, "mail_send": 1, "mail_reply": 1}) {
				t.Fatal("wrong external effects", f.counts())
			}
			for _, step := range done.Steps {
				for _, call := range step.Calls {
					if call.Status != "completed" {
						t.Fatalf("call failed %+v", call)
					}
					if call.Name == "mail_send" || call.Name == "mail_reply" {
						if !strings.Contains(call.ResultPreview, `"delivery":"unknown"`) {
							t.Fatal("mail result claimed delivery")
						}
					}
				}
			}
			f.vendorMu.Lock()
			if len(f.mailText) != 2 || f.mailText[0] != accountWriteMailText || f.mailText[1] != "完整回复正文" {
				t.Error("full approved text changed")
			}
			f.vendorMu.Unlock()
			before := f.requests.Load()
			restart()
			read := accountDecode[sdk.ConversationRun](t, b.call("GET", path, "", 200))
			if read.Status != "completed" || f.requests.Load() != before {
				t.Fatal("completed read performed vendor operation")
			}
			b.call("POST", "/integration/connection-accounts/"+account.Key+"/write", `{}`, 404)
			setting := settingList(t, b)["mail_send"]
			b.call("PUT", "/tools/preferences/mail_send", accountJSON(toolsdk.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
			hidden := b.call("GET", strings.Split(path, "/runs/")[0]+"/messages", "", 200).Body.String()
			if strings.Contains(hidden, "两封邮件已由服务受理") {
				t.Fatal("disabled write tool's historical conclusion remained visible")
			}
			t.Logf("actual OAuth/Identity/Agent/Tools/Integration/Provider chain: vendor HTTP=%d model HTTP=%d effects=%v", f.requests.Load(), f.modelRequests.Load(), f.counts())
		})
	}
}

func grantAccountWriteConfirmation(t *testing.T, host *Host, b *browser) {
	t.Helper()
	mutateTestRolePermissions(t, host, b, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		return append(prior, identity.ProjectRolePermission{PermissionKey: sdk.ConversationInteractionPermission().Key, DataScope: identity.DataScopeOwner})
	})
}

// Existing read flags remain read-only even with a newer Integration binding.
func TestAccountWriteCompositionRequiresExplicitOptInAndConfirmationPort(t *testing.T) {
	for _, mode := range []string{"calendar", "mail", "both"} {
		t.Run(mode, func(t *testing.T) {
			options := Options{CalendarWriteTools: mode != "mail", MailWriteTools: mode != "calendar"}
			if err := configureAccountDefinitions(&options); err != nil {
				t.Fatal(err)
			}
			h := &Host{}
			if err := h.bindAccountTools(&options); err != nil {
				t.Fatal(err)
			}
			if _, err := options.Agent.ConversationOptions.AssembleTools(&confirmationWebFixture{}); err == nil {
				t.Fatal("missing durable verifier silently accepted")
			}
			if options.Agent.ConversationOptions.MaxArgumentBytes != 1<<20 || options.Agent.ConversationOptions.ContextBytes != 2<<20 {
				t.Fatal("complete write content cannot fit")
			}
			override := Options{CalendarWriteTools: options.CalendarWriteTools, MailWriteTools: options.MailWriteTools}
			override.Agent.ConversationOptions.MaxArgumentBytes = 8192
			override.Agent.ConversationOptions.ContextBytes = 98304
			if err := configureAccountDefinitions(&override); err != nil || override.Agent.ConversationOptions.MaxArgumentBytes != 8192 || override.Agent.ConversationOptions.ContextBytes != 98304 {
				t.Fatal("explicit deployment budgets changed", err)
			}
		})
	}
	permission := integration.ConnectionAccountWritePermission()
	if permission.Key != integration.ActionIntegrationConnectionAccountsWrite || permission.ResourceKey != "integration.connection_accounts" || permission.OperationKey != "write" || permission.Label == "" {
		t.Fatal("owner permission mismatch")
	}
	for _, route := range integration.IntegrationHTTPAdapterContract().Routes {
		if route.Action.Key == permission.Key {
			t.Fatal("host write permission became browser endpoint")
		}
	}
}
