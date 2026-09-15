package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

const failedExecutionDraft = "UNVERIFIED-FAILURE-DRAFT: no successful receipt proves this page's contents."

type sharedFailedExecutionWireModel struct{ peerWebModel }

func (m *sharedFailedExecutionWireModel) StreamConversationStep(_ context.Context, in sdk.ConversationStepRequest, _ func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	for _, message := range in.Messages {
		if message.Role == "tool" && message.ToolCallID == "failed-web-fetch" {
			var wire sdk.ConversationToolResult
			if err := json.Unmarshal([]byte(message.Content), &wire); err != nil {
				return sdk.ConversationStepResult{}, err
			}
			return sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: failedExecutionDraft}}, nil
		}
	}
	return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: "failed-web-fetch", Name: "web_fetch", Arguments: accountJSON(map[string]string{"url": webFixtureURL})}}}}, nil
}

func TestSharedExecutionHidesRealFailedToolPayloadAndUnverifiedReplyAfterRestart(t *testing.T) {
	f := newWebProductFixture(t)
	f.close()
	f.mode.Store(4) // The actual vendor HTTP returns 503 with a private failure body.
	worker := &sharedFailedExecutionWireModel{peerWebModel: peerWebModel{modelKey: "failed-page"}}
	f.options.Agent.ConversationURL = ""
	f.options.Agent.ConversationProvider = &peerWebModel{}
	f.options.Agent.ConversationOptions.AgentModels = map[string]sdk.ConversationModel{"failed-page": worker}
	gate := &sharedAccountPendingTransport{Transport: f.providerTransport, entered: make(chan struct{}), released: make(chan struct{})}
	f.providerTransport = gate
	released := false
	defer func() {
		if !released {
			close(gate.released)
		}
	}()
	f.open()
	files := fstest.MapFS{"index.html": {Data: []byte("failed execution")}, "oauth-callback.html": {Data: []byte("callback")}}
	owner := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	reader := &browser{t: t, handler: owner.handler, cookies: map[string]*http.Cookie{}}
	owner.login("admin@example.com", accountInitial)
	owner.changePassword(accountInitial, accountChanged)
	reader.login("system_administrator@example.com", accountInitial)
	reader.changePassword(accountInitial, accountChanged)
	owner.call("POST", "/app/product/account-setup", `{}`, 200)
	owner.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	ownerID, readerID := owner.readSession()["user_id"].(string), reader.readSession()["user_id"].(string)
	grantSharedAccountPolicy(t, f.accountFixture, owner, ownerID, toolmodule.WebDefinitions(), true)
	grantSharedAccountPolicy(t, f.accountFixture, owner, readerID, toolmodule.WebDefinitions(), false)
	f.connect()
	mode, users := "owner", []string{readerID}
	agent := accountDecode[sdk.ConversationAgent](t, owner.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "failed-page", Name: "Page verifier", Instructions: "Fetch the exact page", Tools: []string{"web_fetch"}, SkillKeys: []string{}, ModelKey: "failed-page", Enabled: true, MaxConcurrent: 1, DelegationExecution: &mode, SharedWithUserIDs: &users}), 200))
	source := accountDecode[sdk.Conversation](t, reader.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "failed-source", Title: "Failed source verification"}), 200))
	var d sdk.ConversationDelegationDetail
	if err := unmarshalPeerDetail(reader.call("POST", "/agent/delegations", accountJSON(sdk.ConversationDelegationCreate{ClientID: "failed-page-work", ConversationID: source.ID, AgentID: agent.ID, Purpose: "Observe a failed original call", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "Verify the original page", Deliverable: "Successful original page receipt", CompletionConditions: []string{"Read the original page"}}, Budget: sdk.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 8192, TimeoutSeconds: 60}}), 200).Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	path := "/agent/delegations/" + d.ID
	read := func() sdk.ConversationDelegationDetail {
		t.Helper()
		var current sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(owner.call("GET", path, "", 200).Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		return current
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		d = read()
		if d.Task != nil && d.Task.ExecutionRunID != "" {
			select {
			case <-gate.entered:
				goto pending
			default:
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("actual failed tool did not reach the running boundary")
pending:
	ref := sdk.ConversationRunReference{ConversationID: d.ConversationID, RunID: d.Task.ExecutionRunID}
	owner.call("POST", path+"/execution-publications", accountJSON(sdk.ConversationExecutionShare{ClientID: "failed-run-share", Reference: ref, ExpectedRevision: d.Revision, Reason: "Share exact execution while the tool is pending"}), 200)
	reader.call("POST", path+"/execution", accountJSON(ref), 200)
	close(gate.released)
	released = true
	runPath := "/agent/conversations/" + ref.ConversationID + "/runs/" + ref.RunID
	var original sdk.ConversationRun
	for time.Now().Before(deadline) {
		original = accountDecode[sdk.ConversationRun](t, owner.call("GET", runPath, "", 200))
		if original.Terminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	privateReply := strings.Contains(original.DraftText, failedExecutionDraft)
	for _, step := range original.Steps {
		privateReply = privateReply || strings.Contains(step.Text, failedExecutionDraft)
	}
	if !original.Terminal() || f.vendorCalls.Load() != 1 || !privateReply {
		t.Fatal("fixture did not preserve the real failed operation and subsequent reply", original.Status, original.DraftText, f.vendorCalls.Load())
	}
	var failed sdk.ConversationToolView
	for _, step := range original.Steps {
		for _, call := range step.Calls {
			if call.ID == "failed-web-fetch" {
				failed = call
			}
		}
	}
	if failed.Status != "failed" || failed.StartedAt == nil || failed.Arguments == "" || failed.ErrorCode == "" {
		t.Fatal("original private run did not record an actually started failed call", failed)
	}
	assertHidden := func() {
		t.Helper()
		observed := accountDecode[sdk.ConversationRun](t, reader.call("POST", path+"/execution", accountJSON(ref), 200))
		if observed.ID != original.ID || observed.Status != original.Status || observed.DraftText != "" || observed.DraftBytes != 0 || observed.Interaction != nil || observed.WriteScope != nil {
			t.Fatal("failed shared run lost state or exposed unverified reply/control", observed)
		}
		found := false
		for _, step := range observed.Steps {
			if step.Text != "" {
				t.Fatal("failed shared run exposed unverified model text")
			}
			for _, call := range step.Calls {
				if call.ID != failed.ID {
					continue
				}
				found = true
				if call.Status != failed.Status || call.StartedAt == nil || call.AccessError != "execution_result_not_readable" || call.Arguments != "" || call.ResultPreview != "" || call.ErrorCode != "" || call.ResultReference != nil || call.Authorization != nil || call.Confirmation != nil || call.ResourceID != "" || len(call.Citations) != 0 {
					t.Fatal("failed tool shared unverified private payload", call)
				}
			}
		}
		if !found || f.vendorCalls.Load() != 1 {
			t.Fatal("shared failure disappeared or repeated the vendor operation")
		}
		if failed.ResultReference != nil {
			reader.call("POST", path+"/execution-result", accountJSON(sdk.ConversationResultRead{Reference: *failed.ResultReference}), 403)
		}
		reader.call("GET", runPath, "", 404)
	}
	assertHidden()
	grantSharedAccountPolicy(t, f.accountFixture, owner, ownerID, toolmodule.WebDefinitions(), false)
	assertHidden()
	f.close()
	f.open()
	owner.handler = f.boundary("http://127.0.0.1:8091", files)
	reader.handler = owner.handler
	owner.login("admin@example.com", accountChanged)
	reader.login("system_administrator@example.com", accountChanged)
	assertHidden()
	t.Log("actual connector HTTP 503, durably started failed call and subsequent model reply; shared run retains exact state/timing while failed parameters/results/reply/control stay hidden after execution revocation and restart; no repeat vendor IO")
}
