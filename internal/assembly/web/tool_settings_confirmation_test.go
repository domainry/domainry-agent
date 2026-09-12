package web

import (
	sdk "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestToolSettingsConfirmationAcrossRestartRechecksDurableChoice(t *testing.T) {
	f := newToolSettingsFixture(t)
	f.close()
	f.options.Agent.ConversationOptions.Agent.Tools = []string{"calculate", "memory_save"}
	f.open()
	files := fstest.MapFS{"index.html": {Data: []byte("settings")}, "oauth-callback.html": {Data: []byte("callback")}}
	b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", accountInitial)
	b.changePassword(accountInitial, accountChanged)
	f.grantSettings(b, "admin", true)
	mutateTestRolePermissions(t, f.host, b, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		return append(prior, identity.ProjectRolePermission{PermissionKey: sdk.ConversationInteractionPermission().Key, DataScope: identity.DataScopeOwner})
	})
	c := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"settings-confirm","title":"settings-confirm"}`, 200))
	run := accountDecode[sdk.ConversationRun](t, b.call("POST", "/agent/conversations/"+c.ID+"/messages", `{"client_message_id":"settings-write","message":"保存记忆"}`, 202))
	path := "/agent/conversations/" + c.ID + "/runs/" + run.ID
	wait := func(expected string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			run = accountDecode[sdk.ConversationRun](t, b.call("GET", path, "", 200))
			if run.Status == expected {
				return
			}
			if run.Terminal() {
				t.Fatalf("expected %s got %+v", expected, run)
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("run did not reach %s: %+v", expected, run)
	}
	wait("waiting_confirmation")
	current := settingList(t, b)["memory_save"]
	b.call("PUT", "/tools/preferences/memory_save", accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: current.Revision, ToolVersion: current.Version}), 200)
	response := sdk.ConversationInteractionResponse{InteractionID: run.Interaction.ID, ClientID: "approved-before-disabled-check", ExpectedRevision: run.Interaction.Revision, Decision: "approve"}
	// Confirmation rejects a tool that the user has disabled since the request.
	b.call("POST", path+"/respond", accountJSON(response), 403)
	wait("waiting_confirmation")
	var count int
	if err := f.host.db.QueryRow("SELECT count(*) FROM _agent_user_memories").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("confirmation bypassed disabled tool")
	}
	originalRun := run.ID
	f.close()
	f.open()
	b.handler = f.boundary("http://127.0.0.1:8091", files)
	b.login("admin@example.com", accountChanged)
	b.call("POST", path+"/respond", accountJSON(response), 403)
	wait("waiting_confirmation")
	if run.ID != originalRun {
		t.Fatal("resume replaced run")
	}
	current = settingList(t, b)["memory_save"]
	b.call("PUT", "/tools/preferences/memory_save", accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: current.Revision, ToolVersion: current.Version}), 200)
	b.call("POST", path+"/respond", accountJSON(response), 200)
	wait("completed")
	if err := f.host.db.QueryRow("SELECT count(*) FROM _agent_user_memories").Scan(&count); err != nil || count != 1 {
		t.Fatal("resume did not commit exactly once", count, err)
	}
	if !strings.Contains(b.call("GET", "/agent/conversations/"+c.ID+"/messages", "", 200).Body.String(), "F01 settings") {
		t.Fatal("completed result absent")
	}
	t.Log("PASS disabled after confirmation request, confirmation denied before mutation, disabled choice survives reopen, explicit enable permits original confirmation and one actual memory write")
}
