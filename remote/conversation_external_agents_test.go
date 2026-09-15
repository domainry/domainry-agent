package remote

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/server"
)

type externalAgentRemoteFixture struct {
	releasedResultService
	operation string
	taskID    string
	query     sdk.ConversationExternalAgentAssignmentQuery
	claim     sdk.ConversationExternalAgentClaim
	report    sdk.ConversationExternalAgentReport
}

func (s *externalAgentRemoteFixture) ConversationExternalAgentAssignments(_ context.Context, in sdk.ConversationExternalAgentAssignmentQuery, a sdk.ConversationAuthority) (sdk.ConversationExternalAgentAssignmentPage, error) {
	s.calls++
	s.operation, s.query, s.authority = "query", in, a
	return sdk.ConversationExternalAgentAssignmentPage{Items: []sdk.ConversationExternalAgentAssignment{{Delegation: sdk.ConversationDelegationDetail{ConversationDelegation: sdk.ConversationDelegation{ID: "delegation"}}}}, Complete: true}, nil
}

func (s *externalAgentRemoteFixture) ClaimConversationExternalAgentTask(_ context.Context, taskID string, in sdk.ConversationExternalAgentClaim, a sdk.ConversationAuthority) (sdk.ConversationExternalAgentClaimReceipt, error) {
	s.calls++
	s.operation, s.taskID, s.claim, s.authority = "claim", taskID, in, a
	return sdk.ConversationExternalAgentClaimReceipt{Task: sdk.ConversationTaskDetail{ConversationTaskSummary: sdk.ConversationTaskSummary{ID: taskID}}}, nil
}

func (s *externalAgentRemoteFixture) ReportConversationExternalAgentTask(_ context.Context, taskID string, in sdk.ConversationExternalAgentReport, a sdk.ConversationAuthority) (sdk.ConversationExternalAgentReportReceipt, error) {
	s.calls++
	s.operation, s.taskID, s.report, s.authority = "report", taskID, in, a
	return sdk.ConversationExternalAgentReportReceipt{Task: sdk.ConversationTaskDetail{ConversationTaskSummary: sdk.ConversationTaskSummary{ID: taskID}}}, nil
}

func TestSaaSExternalAgentProtocolPreservesExactAuthorityCapabilitiesAndEventCursor(t *testing.T) {
	source := &externalAgentRemoteFixture{}
	svc, err := server.New(server.Config{APIKey: "external-agent-runtime-secret", Conversations: source, ConversationRuntimeID: "runtime", ConversationWorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(svc.Handler())
	defer httpServer.Close()
	opened, err := NewFactory(Options{BaseURL: httpServer.URL, APIKey: "external-agent-runtime-secret", Client: httpServer.Client()}).OpenSaaS(t.Context(), sdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close(context.Background())
	client, ok := opened.(sdk.ConversationBinding).Conversations().(sdk.ConversationExternalAgentService)
	if !ok {
		t.Fatal("SaaS omitted the external Agent protocol")
	}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "executor", RoleKey: "external-role"}
	query := sdk.ConversationExternalAgentAssignmentQuery{AgentID: "external-agent", Limit: 7}
	if page, err := client.ConversationExternalAgentAssignments(t.Context(), query, a); err != nil || len(page.Items) != 1 || source.operation != "query" || source.query != query || source.authority != a {
		t.Fatal("assignment query changed", page, err)
	}
	capabilities := sdk.ConversationExternalAgentCapabilities{Steering: true, Cancellation: true, Resume: true, StructuredOutput: true, ExecutionDetails: true}
	claim := sdk.ConversationExternalAgentClaim{ClientID: "claim", AgentID: query.AgentID, Capabilities: capabilities}
	if receipt, err := client.ClaimConversationExternalAgentTask(t.Context(), "task", claim, a); err != nil || receipt.Task.ID != "task" || source.operation != "claim" || source.taskID != "task" || !reflect.DeepEqual(source.claim, claim) || source.authority != a {
		t.Fatal("claim changed", receipt, err)
	}
	report := sdk.ConversationExternalAgentReport{ClientID: "report", AgentID: query.AgentID, SessionID: "session", ExpectedLastEventSeq: 11, AcknowledgedMessages: []string{"message"}, Events: []sdk.ConversationExternalAgentEvent{{Seq: 12, Kind: "progress", Summary: "working"}}}
	if receipt, err := client.ReportConversationExternalAgentTask(t.Context(), "task", report, a); err != nil || receipt.Task.ID != "task" || source.operation != "report" || !reflect.DeepEqual(source.report, report) || source.authority != a {
		t.Fatal("report changed", receipt, err)
	}
	before := source.calls
	a.WorkspaceID = "foreign"
	if _, err := client.ConversationExternalAgentAssignments(t.Context(), query, a); err == nil || source.calls != before {
		t.Fatal("foreign workspace reached external Agent service", err)
	}
}
