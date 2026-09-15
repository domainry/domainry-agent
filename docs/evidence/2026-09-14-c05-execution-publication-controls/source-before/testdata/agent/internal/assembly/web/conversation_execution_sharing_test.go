package web

import (
	sdk "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	"os"
	"testing"
)

func exerciseParticipantExecutionHTTP(t *testing.T, host *Host, executor, issuer, reader *browser, d sdk.ConversationDelegationDetail, read func(*browser) sdk.ConversationDelegationDetail, setScope func(string, []string), setSharing func(string, bool)) {
	t.Helper()
	path := "/agent/delegations/" + d.ID
	ref := sdk.ConversationRunReference{ConversationID: d.ConversationID, RunID: d.Task.ExecutionRunID}
	resultRef := d.Delivery.Conditions[0].Receipts[0]
	reader.call("GET", path+"/executions", "", 403)
	reader.call("POST", path+"/execution", accountJSON(ref), 403)
	setScope("execution-reader-only", []string{"view", "execution_read"})
	if detail := read(reader); !detail.Access.ExecutionRead || detail.Access.DeliveryRead || detail.Delivery != nil || detail.Task != nil || detail.TaskID != "" || detail.ConversationID != "" {
		t.Fatal("execution reading changed private or delivery access", detail)
	}
	if items := accountDecode[[]sdk.ConversationExecutionPublication](t, reader.call("GET", path+"/executions", "", 200)); len(items) != 0 {
		t.Fatal("delivery implicitly published full execution", items)
	}
	reader.call("POST", path+"/execution", accountJSON(ref), 403)
	publication := sdk.ConversationExecutionShare{Reference: ref, ClientID: "original-executor-share", ExpectedRevision: read(issuer).Revision, Reason: "由实际执行人明确共享这次运行"}
	issuer.call("POST", path+"/execution-publications", accountJSON(publication), 404)
	actual := accountDecode[sdk.ConversationExecutionPublication](t, executor.call("POST", path+"/execution-publications", accountJSON(publication), 200))
	if actual.Publisher.UserID != d.ExecutionSubject.UserID || actual.Reference != ref {
		t.Fatal("publication lost actual executor", actual)
	}
	items := accountDecode[[]sdk.ConversationExecutionPublication](t, reader.call("GET", path+"/executions", "", 200))
	if len(items) != 1 || items[0].Publisher != actual.Publisher {
		t.Fatal("third execution reader missed original publication", items)
	}
	// This run inherits an actual agent_message. execution_read alone must
	// not release that communication; its independent scope is also required.
	reader.call("POST", path+"/execution", accountJSON(ref), 403)
	setScope("execution-reader-communication", []string{"view", "execution_read", "communicate"})
	// Its inherited original history also includes a submitted delivery.
	// Preserve that independent source permission instead of borrowing it.
	reader.call("POST", path+"/execution", accountJSON(ref), 403)
	setScope("execution-reader-all-source-scopes", []string{"view", "execution_read", "communicate", "delivery_read"})
	run := accountDecode[sdk.ConversationRun](t, reader.call("POST", path+"/execution", accountJSON(ref), 200))
	if run.ID != ref.RunID || run.ConversationID != ref.ConversationID || len(run.Steps) == 0 || run.BackgroundTask == nil || run.BackgroundTask.DelegationID != d.ID || run.Interaction != nil || run.WriteScope != nil {
		t.Fatal("shared run lost original execution or exposed controls", run)
	}
	input := sdk.ConversationResultRead{Reference: resultRef, MaxBytes: 256}
	page := accountDecode[sdk.ConversationResultSlice](t, reader.call("POST", path+"/execution-result", accountJSON(input), 200))
	if page.Reference != resultRef || page.NextOffset < 1 {
		t.Fatal("shared full result is not exact", page)
	}
	input.Offset = page.NextOffset
	reader.call("POST", path+"/execution-result", accountJSON(input), 200)
	reader.call("POST", path+"/delivery-result", accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: input}), 200)
	reader.call("GET", "/agent/conversations/"+ref.ConversationID+"/runs/"+ref.RunID, "", 404)
	reader.call("POST", "/agent/conversations/"+ref.ConversationID+"/runs/"+ref.RunID+"/cancel", "{}", 404)
	wrong := ref
	wrong.ConversationID = d.SourceConversationID
	reader.call("POST", path+"/execution", accountJSON(wrong), 403)
	setSharing(d.ExecutionSubject.UserID, false)
	reader.call("POST", path+"/execution", accountJSON(ref), 403)
	reader.call("POST", path+"/execution-result", accountJSON(input), 403)
	setSharing(d.ExecutionSubject.UserID, true)
	setReading := func(user string, allowed bool) {
		mutateTestRolePermissions(t, host, executor, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, permission := range previous {
				if permission.PermissionKey != sdk.ConversationCollaborationPermission("execution_read").Key {
					out = append(out, permission)
				}
			}
			if allowed {
				out = append(out, identity.ProjectRolePermission{PermissionKey: sdk.ConversationCollaborationPermission("execution_read").Key, DataScope: identity.DataScopeAll})
			}
			return out
		}, user)
	}
	setReading(d.OwnerUserID, false)
	reader.call("POST", path+"/execution", accountJSON(ref), 403)
	reader.call("POST", path+"/execution-result", accountJSON(input), 403)
	setReading(d.OwnerUserID, true)
	setScope("execution-reader-withdraw", []string{"view"})
	reader.call("POST", path+"/execution", accountJSON(ref), 403)
	reader.call("POST", path+"/execution-result", accountJSON(input), 403)
	setScope("execution-reader-restore", []string{"view", "execution_read", "communicate", "delivery_read"})
	reader.call("POST", path+"/execution", accountJSON(ref), 200)
	exerciseExecutionToolsHTTP(t, executor, issuer, reader, d, read, setScope)
	if os.Getenv("AGENT_EXECUTION_SHARING_BROWSER") == "1" {
		exerciseParticipantBrowser(t, host, "execution-sharing.browser.mjs", d.SourceConversationID, "delivery-reader", "delivery-reader@example.com", "Delivery-Reader-Changed!26")
	}
	withdraw := publication
	withdraw.ClientID = "original-executor-withdraw"
	withdraw.ExpectedRevision = read(issuer).Revision
	withdraw.Withdraw = true
	setReading(d.ExecutionSubject.UserID, false)
	executor.call("GET", "/agent/conversations/"+ref.ConversationID+"/runs/"+ref.RunID, "", 403)
	executor.call("POST", path+"/execution-publications", accountJSON(withdraw), 200)
	setReading(d.ExecutionSubject.UserID, true)
	reader.call("POST", path+"/execution", accountJSON(ref), 403)
	reader.call("POST", path+"/execution-result", accountJSON(input), 403)
	// Exact original request replay returns its immutable receipt, not a new release.
	executor.call("POST", path+"/execution-publications", accountJSON(publication), 200)
	reader.call("POST", path+"/execution", accountJSON(ref), 403)
	setScope("delivery-reader-post-execution", []string{"view", "delivery_read"})
	reader.call("POST", path+"/delivery-result", accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: input}), 200)
}
