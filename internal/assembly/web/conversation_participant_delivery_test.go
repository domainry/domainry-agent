package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	identityhttp "github.com/domainry/domainry-identity-sdk/httpapi"
)

func exerciseParticipantDeliveryHTTP(t *testing.T, host *Host, executor, issuer *browser, d sdk.ConversationDelegationDetail) sdk.ConversationDelegationDetail {
	t.Helper()
	mux := http.NewServeMux()
	for _, adapter := range host.Identity.(identityhttp.Provider).HTTPAdapters() {
		for _, route := range adapter.Routes() {
			mux.Handle(route.Pattern(), adapter.Handler())
		}
	}
	identityCall := func(method, path, body, hash string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+executor.cookies["domainry_agent_access"].Value)
		r.Header.Set("X-Workspace-ID", string(host.application.WorkspaceID))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Expected-Schema-Hash", hash)
		r.Header.Set("Idempotency-Key", fmt.Sprintf("delivery-reader-%d", time.Now().UnixNano()))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("Identity %s %s: %d, want %d, body %s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	const readerID, readerEmail, readerPassword = "delivery-reader", "delivery-reader@example.com", "Delivery-Reader-Changed!26"
	user := map[string]any{"id": readerID, "name": "交付读者", "email": readerEmail, "status": "active"}
	created := accountDecode[struct {
		InitialPassword string `json:"initial_password"`
	}](t, identityCall("POST", "/identity/users", accountJSON(user), "", 201))
	roles, err := host.Identity.Projection().ListUserRoleAssignments(t.Context(), identity.UserRoleAssignmentQuery{UserID: identity.SubjectID(d.OwnerUserID)})
	if err != nil || len(roles) == 0 {
		t.Fatal("missing real issuer role", roles, err)
	}
	assignment := identityCall("GET", "/identity/users/"+readerID+"/role-assignments", "", "", 200)
	identityCall("PUT", "/identity/users/"+readerID+"/account-and-roles", accountJSON(map[string]any{"user": user, "assignments": []map[string]any{{"role_id": roles[0].RoleID}}}), assignment.Header().Get("X-Resource-Hash"), 200)
	reader := &browser{t: t, handler: issuer.handler, cookies: map[string]*http.Cookie{}}
	reader.login(readerEmail, created.InitialPassword)
	reader.changePassword(created.InitialPassword, readerPassword)
	if id := reader.readSession()["user_id"].(string); id != readerID || id == d.OwnerUserID || id == d.ExecutionSubject.UserID {
		t.Fatal("three distinct authenticated users required", id, d.OwnerUserID, d.ExecutionSubject)
	}
	path := "/agent/delegations/" + d.ID
	read := func(b *browser) sdk.ConversationDelegationDetail {
		t.Helper()
		var result sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", path, "", 200).Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	setScope := func(client string, operations []string) {
		t.Helper()
		current := read(issuer)
		participants := []sdk.ConversationDelegationParticipantInput{{UserID: readerID, Operations: operations}}
		issuer.call("POST", path+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: client, ExpectedRevision: current.Revision, Action: "set_participants", Reason: "明确参与人的交付阅读范围", Participants: &participants}), 200)
	}
	ref := d.Delivery.Conditions[0].Receipts[0]
	input := sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: ref, MaxBytes: 256}}
	setScope("delivery-reader-view", []string{"view"})
	if onlyView := read(reader); onlyView.Delivery != nil || onlyView.Verification != nil || onlyView.Access.DeliveryRead {
		t.Fatal("view scope exposed delivery", onlyView)
	}
	reader.call("POST", path+"/delivery-result", accountJSON(input), 403)
	setScope("delivery-reader-grant", []string{"view", "delivery_read"})
	first := accountDecode[sdk.ConversationResultSlice](t, reader.call("POST", path+"/delivery-result", accountJSON(input), 200))
	shared := read(reader)
	if shared.Access == nil || !shared.Access.DeliveryRead || shared.Access.ExecutionRead || shared.Access.Manage || shared.Task != nil || shared.TaskID != "" || shared.ConversationID != "" || shared.SourceConversationID != "" || shared.Delivery == nil || shared.Verification == nil || shared.Delivery.Summary != d.Delivery.Summary || len(shared.Messages) != 0 {
		t.Fatal("explicit delivery reader received wrong projection", shared)
	}
	if first.Reference != ref || first.NextOffset < 1 {
		t.Fatal("fixture did not return the original submitted result", first)
	}
	input.Offset = first.NextOffset
	reader.call("POST", path+"/delivery-result", accountJSON(input), 200)
	history := accountDecode[sdk.ConversationDeliveryHistory](t, reader.call("GET", path+"/deliveries", "", 200))
	if len(history.Items) < 1 || history.Items[0].Delivery.Summary != d.Delivery.Summary {
		t.Fatal("submitted history was not shared", history)
	}
	input.DeliveryRevision = history.Items[0].Revision
	reader.call("POST", path+"/delivery-result", accountJSON(input), 200)
	input.DeliveryRevision = 0
	wrong := input
	wrong.Reference.CallID = "unsubmitted-call"
	reader.call("POST", path+"/delivery-result", accountJSON(wrong), 403)
	reader.call("GET", "/agent/conversations/"+d.ConversationID, "", 404)
	reader.call("GET", "/agent/tasks/"+d.TaskID, "", 404)
	reader.call("GET", "/agent/conversations/"+d.ConversationID+"/runs/"+d.Task.ExecutionRunID, "", 404)
	setSharing := func(user string, allowed bool) {
		t.Helper()
		mutateTestRolePermissions(t, host, executor, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, permission := range previous {
				if permission.PermissionKey != sdk.ConversationCollaborationPermission("share").Key {
					out = append(out, permission)
				}
			}
			if allowed {
				out = append(out, identity.ProjectRolePermission{PermissionKey: sdk.ConversationCollaborationPermission("share").Key, DataScope: identity.DataScopeAll})
			}
			return out
		}, user)
	}
	setSharing(d.ExecutionSubject.UserID, false)
	reader.call("POST", path+"/delivery-result", accountJSON(input), 403)
	if denied := read(reader); denied.Delivery != nil || denied.Verification != nil || !denied.DeliveryOmitted {
		t.Fatal("original publisher withdrawal exposed submitted data", denied)
	}
	setSharing(d.ExecutionSubject.UserID, true)
	reader.call("POST", path+"/delivery-result", accountJSON(input), 200)
	setSharing(d.OwnerUserID, false)
	reader.call("POST", path+"/delivery-result", accountJSON(input), 403)
	setSharing(d.OwnerUserID, true)
	setScope("delivery-reader-withdraw", []string{"view"})
	reader.call("POST", path+"/delivery-result", accountJSON(input), 403)
	reader.call("GET", path+"/deliveries", "", 403)
	if withdrawn := read(reader); withdrawn.Delivery != nil || withdrawn.Verification != nil || withdrawn.Access.DeliveryRead {
		t.Fatal("withdrawn delivery scope retained data", withdrawn)
	}
	setScope("delivery-reader-restore", []string{"view", "delivery_read"})
	reader.call("POST", path+"/delivery-result", accountJSON(input), 200)
	exerciseParticipantDeliveryBrowser(t, host, d.SourceConversationID, readerID, readerEmail, readerPassword)
	exerciseParticipantExecutionHTTP(t, host, executor, issuer, reader, d, read, setScope, setSharing)
	return read(issuer)
}
