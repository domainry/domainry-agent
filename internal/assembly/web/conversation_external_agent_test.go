package web

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
)

func TestExternalPeerAgentProtocolUsesExactIdentityAndShowsReportedExecution(t *testing.T) {
	const initial, changed = "External-Agent-Initial!26", "External-Agent-Changed!26"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "external-agent-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "external-agent-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &peerWebModel{modelKey: "external-protocol-host"}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "external-agent.db"), RuntimeID: "external-agent-runtime", WorkspaceID: "external-agent-workspace", ApplicationKey: "external-agent-app", Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "external-protocol-host", Files: fstest.MapFS{"index.html": {Data: []byte("external peer")}}})
	if err != nil {
		t.Fatal(err)
	}
	receiver := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	issuer := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	receiver.login("admin@example.com", initial)
	receiver.changePassword(initial, changed)
	issuer.login("system_administrator@example.com", initial)
	issuer.changePassword(initial, changed)
	receiverID := receiver.readSession()["user_id"].(string)
	issuerID := issuer.readSession()["user_id"].(string)
	grant := func(user string) {
		mutateTestRolePermissions(t, host, receiver, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, permission := range previous {
				if !strings.HasPrefix(permission.PermissionKey, sdk.ConversationCollaborationPermissionPrefix) {
					out = append(out, permission)
				}
			}
			for _, operation := range sdk.ConversationCollaborationOperations() {
				out = append(out, identity.ProjectRolePermission{PermissionKey: sdk.ConversationCollaborationPermission(operation).Key, DataScope: identity.DataScopeAll})
			}
			return out
		}, user)
	}
	grant(receiverID)
	grant(issuerID)
	capabilities := sdk.ConversationExternalAgentCapabilities{Steering: true, Cancellation: true, Resume: true, StructuredOutput: true, ExecutionDetails: true}
	users, mode := []string{issuerID}, "owner"
	agent := accountDecode[sdk.ConversationAgent](t, receiver.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "http-external-agent", Name: "External audit Agent", Description: "Runs in another service", Instructions: "Audit the supplied rows", External: &sdk.ConversationExternalAgentConfig{Protocol: sdk.ConversationExternalAgentProtocolV1, Version: "1", Capabilities: capabilities}, Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users, DelegationExecution: &mode}), 200))
	if agent.External == nil || agent.OwnerUserID != receiverID || agent.ModelKey != "" {
		t.Fatal("external Agent configuration was rewritten", agent)
	}
	issuer.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "external-direct-chat", AgentID: agent.ID, Title: "must delegate"}), 400)
	source := accountDecode[sdk.Conversation](t, issuer.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "external-source", Title: "Audit source"}), 200))
	brief := sdk.ConversationTaskBrief{Version: 1, Goal: "Audit supplied totals", Deliverable: "Structured audit", CompletionConditions: []string{"All totals checked"}, ExplicitFields: []string{"goal", "deliverable", "audience", "constraints", "completion_conditions", "assumptions"}}
	created := sdk.ConversationDelegationCreate{ClientID: "external-http-delegation", ConversationID: source.ID, AgentID: agent.ID, Purpose: "Independent external audit", Brief: brief, Input: "Rows 10, 20 and 30", OutputSchema: []byte(`{"type":"object","properties":{"total":{"type":"integer"}},"required":["total"],"additionalProperties":false}`)}
	var delegation sdk.ConversationDelegationDetail
	if err = unmarshalPeerDetail(issuer.call("POST", "/agent/delegations", accountJSON(created), 200).Body.Bytes(), &delegation); err != nil {
		t.Fatal(err)
	}
	if delegation.ExecutionSubject == nil || delegation.ExecutionSubject.UserID != receiverID || delegation.Task == nil || delegation.Task.ExternalExecution == nil || delegation.Task.ExecutionRunID != "" {
		t.Fatal("external assignment lost identity or entered local execution", delegation)
	}
	wrongList := accountDecode[sdk.ConversationExternalAgentAssignmentPage](t, issuer.call("POST", "/agent/external-agents/assignments/query", accountJSON(sdk.ConversationExternalAgentAssignmentQuery{AgentID: agent.ID}), 200))
	if len(wrongList.Items) != 0 {
		t.Fatal("issuer saw receiver's external assignment queue", wrongList)
	}
	issuer.call("POST", "/agent/external-agents/tasks/"+delegation.TaskID+"/claim", accountJSON(sdk.ConversationExternalAgentClaim{ClientID: "wrong-identity", AgentID: agent.ID, Capabilities: capabilities}), 404)
	assignments := accountDecode[sdk.ConversationExternalAgentAssignmentPage](t, receiver.call("POST", "/agent/external-agents/assignments/query", accountJSON(sdk.ConversationExternalAgentAssignmentQuery{AgentID: agent.ID}), 200))
	if len(assignments.Items) != 1 || assignments.Items[0].Delegation.ID != delegation.ID {
		t.Fatal("receiver assignment queue missing exact work", assignments)
	}
	wrongCapabilities := capabilities
	wrongCapabilities.ExecutionDetails = false
	receiver.call("POST", "/agent/external-agents/tasks/"+delegation.TaskID+"/claim", accountJSON(sdk.ConversationExternalAgentClaim{ClientID: "wrong-capabilities", AgentID: agent.ID, Capabilities: wrongCapabilities}), 409)
	claim := accountDecode[sdk.ConversationExternalAgentClaimReceipt](t, receiver.call("POST", "/agent/external-agents/tasks/"+delegation.TaskID+"/claim", accountJSON(sdk.ConversationExternalAgentClaim{ClientID: "claim-http", AgentID: agent.ID, Capabilities: capabilities}), 200))
	if claim.Replay || claim.Task.ExternalExecution == nil || claim.Task.ExternalExecution.SessionID == "" || claim.Task.Status != sdk.ConversationTaskStatusRunning {
		t.Fatal("external claim was not durable", claim)
	}
	claimReplay := accountDecode[sdk.ConversationExternalAgentClaimReceipt](t, receiver.call("POST", "/agent/external-agents/tasks/"+delegation.TaskID+"/claim", accountJSON(sdk.ConversationExternalAgentClaim{ClientID: "claim-http", AgentID: agent.ID, Capabilities: capabilities}), 200))
	if !claimReplay.Replay || claimReplay.Task.ExternalExecution.SessionID != claim.Task.ExternalExecution.SessionID {
		t.Fatal("claim replay changed session", claimReplay)
	}
	message := accountDecode[sdk.ConversationAgentMessage](t, issuer.call("POST", "/agent/delegations/"+delegation.ID+"/messages", accountJSON(sdk.ConversationAgentMessageSend{ClientID: "external-steering", ToAgentID: agent.ID, Content: "Include the currency check", BriefVersion: 1, AgreementRevision: 1}), 200))
	report := sdk.ConversationExternalAgentReport{ClientID: "external-progress", AgentID: agent.ID, SessionID: claim.Task.ExternalExecution.SessionID, ExpectedLastEventSeq: 0, AcknowledgedMessages: []string{message.ID}, Events: []sdk.ConversationExternalAgentEvent{{Seq: 1, Kind: "progress", Phase: "audit", Summary: "Checked three rows", Detail: "10 + 20 + 30 = 60"}, {Seq: 2, Kind: "tool", Phase: "audit", Summary: "Read remote ledger", Tool: "ledger.read"}}}
	reported := accountDecode[sdk.ConversationExternalAgentReportReceipt](t, receiver.call("POST", "/agent/external-agents/tasks/"+delegation.TaskID+"/reports", accountJSON(report), 200))
	if reported.Replay || reported.Task.ExternalExecution.LastEventSeq != 2 {
		t.Fatal("external progress not projected", reported)
	}
	reportReplay := accountDecode[sdk.ConversationExternalAgentReportReceipt](t, receiver.call("POST", "/agent/external-agents/tasks/"+delegation.TaskID+"/reports", accountJSON(report), 200))
	if !reportReplay.Replay || reportReplay.Task.ExternalExecution.LastEventSeq != 2 {
		t.Fatal("external report replay duplicated events", reportReplay)
	}
	receiver.call("POST", "/agent/external-agents/tasks/"+delegation.TaskID+"/reports", accountJSON(sdk.ConversationExternalAgentReport{ClientID: "finish-too-early", AgentID: agent.ID, SessionID: claim.Task.ExternalExecution.SessionID, ExpectedLastEventSeq: 2, Events: []sdk.ConversationExternalAgentEvent{{Seq: 3, Kind: "completed", Summary: "Audit complete"}}}), 409)
	var current sdk.ConversationDelegationDetail
	if err = unmarshalPeerDetail(receiver.call("GET", "/agent/delegations/"+delegation.ID, "", 200).Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	delivery := sdk.ConversationDelegationUpdate{ClientID: "external-delivery", ExpectedRevision: current.Revision, Action: "deliver", Reason: "Submit structured audit", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Three totals checked", Data: []byte(`{"total":60}`), Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Three rows sum to 60"}}, Evidence: []sdk.ConversationRunReference{}, Unresolved: []string{}}}
	receiver.call("POST", "/agent/delegations/"+delegation.ID+"/decisions", accountJSON(delivery), 200)
	finished := accountDecode[sdk.ConversationExternalAgentReportReceipt](t, receiver.call("POST", "/agent/external-agents/tasks/"+delegation.TaskID+"/reports", accountJSON(sdk.ConversationExternalAgentReport{ClientID: "finish-after-delivery", AgentID: agent.ID, SessionID: claim.Task.ExternalExecution.SessionID, ExpectedLastEventSeq: 2, Events: []sdk.ConversationExternalAgentEvent{{Seq: 3, Kind: "completed", Summary: "Audit complete"}}}), 200))
	if finished.Task.Status != sdk.ConversationTaskStatusCompleted {
		t.Fatal("formal delivery did not complete external task", finished)
	}
	if err = unmarshalPeerDetail(issuer.call("GET", "/agent/delegations/"+delegation.ID, "", 200).Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if current.Task == nil || current.Task.ExternalExecution == nil || len(current.Task.ExternalExecution.Events) != 3 || current.Task.Result != nil || current.Delivery == nil || string(current.Delivery.Data) != `{"total":60}` {
		t.Fatal("issuer projection substituted final text for protocol details", current)
	}
	accepted := sdk.ConversationDelegationUpdate{ClientID: "accept-external-delivery", ExpectedRevision: current.Revision, Action: "accept_delivery", Reason: "Structured output and process evidence verified", Review: &sdk.ConversationDeliveryReview{DeliveryDigest: current.Verification.DeliveryDigest, Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Reviewed structured total and ordered process events"}}}}
	issuer.call("POST", "/agent/delegations/"+delegation.ID+"/decisions", accountJSON(accepted), 200)
}
