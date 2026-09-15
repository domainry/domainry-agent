package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

// Dispatch through the model wire rather than the human delegation endpoint.
// The target and all receipt references come from actual HTTP/tool responses.
type sourceReleaseIssuerModel struct{ peerWebModel }

const sourceReleaseImagePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func (m *sourceReleaseIssuerModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	if result, matched := executionToolsModelStep(in); matched {
		return result, nil
	}
	target, dispatched := "", false
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "Delegate fixture request:\n") {
			target = strings.TrimPrefix(message.Content, "Delegate fixture request:\n")
		}
		dispatched = dispatched || message.Role == "tool" && message.ToolCallID == "source-release-dispatch"
	}
	if target == "" || dispatched {
		return m.peerWebModel.StreamConversationStep(ctx, in, emit)
	}
	brief := sdk.ConversationTaskBrief{Version: 1, Goal: "Read the actual current time", Deliverable: "Clock receipt and conclusion", Constraints: []string{}, Assumptions: []string{}, CompletionConditions: []string{"Read the actual current time"}, VerificationRules: []sdk.ConversationCompletionRule{{Condition: 0, Kind: "receipt", Tool: "time_now", ResultSchema: json.RawMessage(`{"type":"object"}`)}}}
	budget := sdk.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 8192, TimeoutSeconds: 60}
	call := sdk.ConversationToolCall{ID: "source-release-dispatch", Name: "agent_delegate", Arguments: accountJSON(map[string]any{"agent_id": target, "purpose": "Independent clock verification", "brief": brief, "budget": budget, "input": "Use the current clock and submit its immutable receipt", "requirements": sdk.ConversationAgentRequirements{Tools: []string{"time_now"}}})}
	return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}, nil
}

func TestSourceReleaseHTTPAgentDispatchMessageAndAutomaticDeliveryAcrossSubjects(t *testing.T) {
	const initial, changed = "Source-Initial!26", "Source-Changed!26"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "source-release-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "source-release-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	worker := &peerWebModel{collaborate: true, modelKey: "clock-professional"}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "source-release.db"), RuntimeID: "source-runtime", WorkspaceID: "source-workspace", ApplicationKey: "source-app", Agent: agentmodule.Options{ConversationProvider: &sourceReleaseIssuerModel{}, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"clock-professional": worker}, Poll: 5 * time.Millisecond, MaxSteps: 16}}}
	var host *Host
	open := func() http.Handler {
		t.Helper()
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("source release")}}})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	receiver := &browser{t: t, handler: open(), cookies: map[string]*http.Cookie{}}
	defer func() { _ = host.Close(context.Background()) }()
	issuer := &browser{t: t, handler: receiver.handler, cookies: map[string]*http.Cookie{}}
	receiver.login("admin@example.com", initial)
	receiver.changePassword(initial, changed)
	issuer.login("system_administrator@example.com", initial)
	issuer.changePassword(initial, changed)
	receiverID := receiver.readSession()["user_id"].(string)
	issuerID := issuer.readSession()["user_id"].(string)
	grant := func(user string, professional bool) {
		t.Helper()
		mutateTestRolePermissions(t, host, receiver, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range previous {
				if !strings.HasPrefix(p.PermissionKey, sdk.ConversationCollaborationPermissionPrefix) && !strings.HasPrefix(p.PermissionKey, sdk.ConversationToolActionPrefix) && !strings.HasPrefix(p.PermissionKey, sdk.ConversationActionPrefix+"attachments_") && p.PermissionKey != sdk.ConversationInteractionPermission().Key {
					out = append(out, p)
				}
			}
			for _, op := range sdk.ConversationCollaborationOperations() {
				if professional || op != "receive" {
					out = append(out, identity.ProjectRolePermission{PermissionKey: sdk.ConversationCollaborationPermission(op).Key, DataScope: identity.DataScopeAll})
				}
			}
			for _, tool := range sdk.ConversationCollaborationTools() {
				out = append(out, identity.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identity.DataScopeOwner})
			}
			for _, definition := range sdk.ConversationHTTPDefinitions() {
				if permission := sdk.ConversationAttachmentPermission(definition.Operation); permission != nil {
					out = append(out, identity.ProjectRolePermission{PermissionKey: permission.Key, DataScope: identity.DataScopeOwner})
				}
			}
			out = append(out, identity.ProjectRolePermission{PermissionKey: sdk.ConversationInteractionPermission().Key, DataScope: identity.DataScopeOwner})
			if professional {
				plan := sdk.ConversationPlanUpdateTool()
				out = append(out, identity.ProjectRolePermission{PermissionKey: plan.ActionKey, DataScope: identity.DataScopeOwner})
				for _, tool := range sdk.PersonalConversationTools() {
					if tool.Key == "time_now" {
						out = append(out, identity.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identity.DataScopeOwner})
					}
				}
			}
			return out
		}, user)
	}
	grant(receiverID, true)
	grant(issuerID, false)
	users, mode := []string{issuerID}, "owner"
	agent := accountDecode[sdk.ConversationAgent](t, receiver.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "source-clock-agent", Name: "Clock verifier", Instructions: "Read time, communicate and deliver its real receipt", Tools: []string{"time_now", "delegation_get", "agent_message", "delegation_update"}, SkillKeys: []string{}, ModelKey: "clock-professional", Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users, DelegationExecution: &mode}), 200))
	source := accountDecode[sdk.Conversation](t, issuer.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "source-dispatch", Title: "Delegate via actual model tool"}), 200))
	imageData, err := base64.StdEncoding.DecodeString(sourceReleaseImagePNG)
	if err != nil {
		t.Fatal(err)
	}
	attachmentPath := "/agent/conversations/" + source.ID + "/attachments"
	upload := httptest.NewRequest("POST", "http://127.0.0.1:8091"+attachmentPath+"?"+url.Values{"client_id": {"delegated-image"}, "filename": {"pixel.png"}}.Encode(), bytes.NewReader(imageData))
	upload.Header.Set("Content-Type", "application/octet-stream")
	upload.Header.Set("Origin", "http://127.0.0.1:8091")
	upload.Header.Set("X-Agent-Scope", issuer.scope)
	for _, cookie := range issuer.cookies {
		upload.AddCookie(cookie)
	}
	uploadResponse := httptest.NewRecorder()
	issuer.handler.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusOK {
		t.Fatalf("image upload: %d %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	attachment := accountDecode[sdk.ConversationAttachment](t, uploadResponse)
	message := "Delegate fixture request:\n" + agent.ID
	sent := accountDecode[sdk.ConversationRun](t, issuer.call("POST", "/agent/conversations/"+source.ID+"/messages", accountJSON(sdk.ConversationSend{ClientMessageID: "dispatch-model", Message: message, Content: []sdk.ConversationContentBlock{{Type: "text", Text: message}, {Type: "image", Image: &sdk.ConversationImageReference{AttachmentID: attachment.ID, Detail: "high"}}}}), 202))
	approve := func(b *browser, cID string, run sdk.ConversationRun) {
		t.Helper()
		if i := run.Interaction; i != nil && i.Status == "pending" {
			b.call("POST", "/agent/conversations/"+cID+"/runs/"+run.ID+"/respond", accountJSON(sdk.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "approve-" + i.ID, ExpectedRevision: i.Revision, Decision: "approve"}), 200)
		}
	}
	var d sdk.ConversationDelegationDetail
	var execution sdk.ConversationRun
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		run := accountDecode[sdk.ConversationRun](t, issuer.call("GET", "/agent/conversations/"+source.ID+"/runs/"+sent.ID, "", 200))
		approve(issuer, source.ID, run)
		page := accountDecode[sdk.ConversationDelegationPage](t, issuer.call("GET", "/agent/delegations", "", 200))
		if len(page.Items) > 0 {
			if err := unmarshalPeerDetail(receiver.call("GET", "/agent/delegations/"+page.Items[0].ID, "", 200).Body.Bytes(), &d); err != nil {
				t.Fatal(err)
			}
			if d.Task != nil && d.Task.ExecutionRunID != "" {
				execution = accountDecode[sdk.ConversationRun](t, receiver.call("GET", "/agent/conversations/"+d.ConversationID+"/runs/"+d.Task.ExecutionRunID, "", 200))
				approve(receiver, d.ConversationID, execution)
				if execution.Terminal() {
					break
				}
			}
		}
		if run.Terminal() && len(page.Items) == 0 {
			t.Fatalf("model dispatch did not admit work: %+v", run)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if d.ID == "" || d.BriefSource == nil || d.InputSource == nil || d.ExecutionSubject == nil || d.ExecutionSubject.UserID != receiverID || execution.Status != "completed" || d.Status != "delivered" || d.Delivery == nil || d.Verification == nil {
		t.Fatalf("cross-subject autonomous flow incomplete: delegation=%+v run=%+v", d, execution)
	}
	worker.mu.Lock()
	sharedImage := false
	for _, request := range worker.requests {
		for _, modelMessage := range request.Messages {
			for _, block := range modelMessage.ContentBlocks {
				sharedImage = sharedImage || block.Type == "image" && block.Image != nil && block.Image.AttachmentID == attachment.ID && block.Image.Source != nil && bytes.Equal(block.Image.Data, imageData)
			}
		}
	}
	worker.mu.Unlock()
	if !sharedImage {
		t.Fatal("delegated Agent did not receive the exact authorized source image")
	}
	issuer.call("GET", "/agent/conversations/"+d.ConversationID, "", 404)
	receiver.call("GET", "/agent/conversations/"+source.ID, "", 404)
	var published sdk.ConversationDelegationDetail
	if err := unmarshalPeerDetail(issuer.call("GET", "/agent/delegations/"+d.ID, "", 200).Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	if published.Delivery == nil || published.Verification == nil || published.Verification.ActorID != receiverID || len(published.Delivery.Conditions) != 1 || len(published.Delivery.Conditions[0].Receipts) != 1 {
		t.Fatalf("issuer cannot read exact automatic delivery: %+v", published)
	}
	messageFound := false
	for _, m := range published.Messages {
		messageFound = messageFound || m.FromAgentID == agent.ID && m.SenderUserID == receiverID && m.SenderRoleKey == agent.DelegationRoleKey && m.Source != nil
	}
	if !messageFound {
		t.Fatal("real peer message and its source were not shared", published.Messages)
	}
	ref := published.Delivery.Conditions[0].Receipts[0]
	readPath := "/agent/delegations/" + d.ID + "/delivery-result"
	issuer.call("POST", readPath, accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: ref}}), 200)
	wrong := ref
	wrong.CallID = "unpublished-call"
	issuer.call("POST", readPath, accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: wrong}}), 403)
	published = exerciseParticipantDeliveryHTTP(t, host, receiver, issuer, published)
	review := sdk.ConversationDelegationUpdate{ClientID: "source-accept", ExpectedRevision: published.Revision, Action: "accept_delivery", Reason: "Checked the immutable clock receipt", Review: &sdk.ConversationDeliveryReview{DeliveryDigest: published.Verification.DeliveryDigest}}
	issuer.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(review), 200)
	exerciseContractPublicationBrowser(t, host, options, d.SourceConversationID, d.ID, changed, func(allowed bool) {
		if allowed {
			grant(issuerID, false)
			return
		}
		mutateTestRolePermissions(t, host, receiver, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range previous {
				if p.PermissionKey != sdk.ConversationCollaborationPermission("share").Key {
					out = append(out, p)
				}
			}
			return out
		}, issuerID)
	})
	exerciseDeliveryPublicationBrowser(t, host, options, d.ConversationID, d.ID, changed, func(allowed bool) {
		if allowed {
			grant(receiverID, true)
			return
		}
		mutateTestRolePermissions(t, host, receiver, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range previous {
				if p.PermissionKey != sdk.ConversationCollaborationPermission("share").Key {
					out = append(out, p)
				}
			}
			return out
		}, receiverID)
	})
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	receiver.handler = open()
	issuer.handler = receiver.handler
	issuer.login("system_administrator@example.com", changed)
	receiver.login("admin@example.com", changed)
	issuer.call("POST", readPath, accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: ref}}), 200)
	remove := func(user, key string) {
		t.Helper()
		mutateTestRolePermissions(t, host, receiver, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range previous {
				if p.PermissionKey != key {
					out = append(out, p)
				}
			}
			return out
		}, user)
	}
	remove(issuerID, sdk.ConversationCollaborationPermission("delivery_read").Key)
	issuer.call("POST", readPath, accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: ref}}), 403)
	grant(issuerID, false)
	remove(receiverID, sdk.ConversationCollaborationPermission("share").Key)
	issuer.call("POST", readPath, accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: ref}}), 403)
	if os.Getenv("AGENT_RECOVERY_CONTROLS_BROWSER") == "1" {
		grant(receiverID, true)
		grant(issuerID, false)
		recovery := accountDecode[sdk.ConversationDelegationDetail](t, issuer.call("POST", "/agent/delegations", accountJSON(sdk.ConversationDelegationCreate{
			ClientID: "recovery-controls", ConversationID: source.ID, AgentID: agent.ID, Purpose: "Stop work when original source authorization is withdrawn",
			Brief:        sdk.ConversationTaskBrief{Version: 1, Goal: "Protected recovery control goal", Deliverable: "Clock receipt", CompletionConditions: []string{"Read the actual current time"}},
			Requirements: sdk.ConversationAgentRequirements{Sources: []sdk.ConversationRunReference{{ConversationID: source.ID, RunID: sent.ID, BeforeStep: 2}}},
			Budget:       sdk.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 8192, TimeoutSeconds: 60},
		}), 200))
		exerciseRecoveryControlsBrowser(t, host, options, source.ID, recovery.ID, changed, func(profile string) {
			grant(receiverID, true)
			grant(issuerID, false)
			if profile != "all" {
				// The original dispatch receipt includes the old task projection.
				// Reusing it requires current execution-read access to that source.
				remove(issuerID, sdk.ConversationCollaborationPermission("execution_read").Key)
			}
			if profile == "manage-denied" {
				remove(issuerID, sdk.ConversationCollaborationPermission("manage").Key)
			}
		})
	}
}
