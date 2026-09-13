package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
)

type peerArtifactDeliveryModel struct{ peerReceiptDeliveryModel }

func readReleasedResult(t *testing.T, b *browser, delegation string, revision int64, ref sdk.ConversationResultReference) sdk.ConversationToolResult {
	t.Helper()
	return readReleasedResultPages(t, b, delegation, revision, ref, 8192)
}

func readReleasedResultPages(t *testing.T, b *browser, delegation string, revision int64, ref sdk.ConversationResultReference, maxBytes int) sdk.ConversationToolResult {
	t.Helper()
	var raw strings.Builder
	for offset, pages := 0, 0; pages < 1024; pages++ {
		in := sdk.ConversationDeliveryResultRead{DeliveryRevision: revision, ConversationResultRead: sdk.ConversationResultRead{Reference: ref, Offset: offset, MaxBytes: maxBytes}}
		page := accountDecode[sdk.ConversationResultSlice](t, b.call("POST", "/agent/delegations/"+delegation+"/delivery-result", accountJSON(in), 200))
		if page.Reference != ref || page.Offset != offset || page.NextOffset != offset+len(page.JSONText) || !page.Complete && page.NextOffset <= offset {
			t.Fatalf("invalid released receipt pagination: %+v", page)
		}
		raw.WriteString(page.JSONText)
		offset = page.NextOffset
		if page.Complete {
			hash := sha256.Sum256([]byte(raw.String()))
			if hex.EncodeToString(hash[:]) != ref.SHA256 || page.TotalBytes != raw.Len() {
				t.Fatal("released receipt is not the immutable original result")
			}
			var result sdk.ConversationToolResult
			if err := json.Unmarshal([]byte(raw.String()), &result); err != nil {
				t.Fatal(err)
			}
			return result
		}
	}
	t.Fatal("released receipt exceeded pagination bound")
	return sdk.ConversationToolResult{}
}

func (m *peerArtifactDeliveryModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	var created, edited sdk.ConversationArtifact
	for _, message := range in.Messages {
		if message.Role != "tool" {
			continue
		}
		var wire sdk.ConversationToolResult
		var value struct {
			Artifact sdk.ConversationArtifact `json:"artifact"`
		}
		if json.Unmarshal([]byte(message.Content), &wire) != nil || wire.Status != "completed" || json.Unmarshal(wire.Content, &value) != nil {
			continue
		}
		switch message.ToolCallID {
		case "create-original":
			created = value.Artifact
		case "edit-original":
			edited = value.Artifact
		}
	}
	model := m.peerReceiptDeliveryModel
	model.sourceCalls = []sdk.ConversationToolCall{
		{ID: "create-original", Name: "artifact_create", Arguments: accountJSON(map[string]any{"title": "协作成果", "content": sdk.ConversationArtifactContent{Kind: "markdown", Markdown: "原始结论。\n" + strings.Repeat("资料段落。\n", 100) + "完整正文最后一行。"}})},
		{ID: "edit-original", Name: "artifact_edit", Arguments: accountJSON(map[string]any{"id": created.ID, "expected_version": created.Version, "patch": map[string]any{"text": []any{map[string]string{"find": "原始结论。", "replace": "已经核对的结论。"}}}})},
		{ID: "export-original", Name: "artifact_export", Arguments: accountJSON(map[string]any{"id": edited.ID, "version": edited.Version, "format": "markdown"})},
		{ID: "read-original", Name: "artifact_read", Arguments: accountJSON(map[string]any{"id": edited.ID, "version": edited.Version, "max_bytes": 256})},
	}
	return model.StreamConversationStep(ctx, in, emit)
}

func TestPeerArtifactDeliveryReadsKnowledgeVersionsWithoutMutationRights(t *testing.T) {
	const initial, changed = "Peer-Artifact-Initial!2026", "Peer-Artifact-Changed!2026"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "peer-artifact-identity-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "peer-artifact-identity-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	worker := &peerArtifactDeliveryModel{peerReceiptDeliveryModel{peerWebModel: peerWebModel{modelKey: "artifact-review"}, summary: "成果已创建、修订并导出，附原版本回执"}}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "peer-artifact.db"), RuntimeID: "artifact-runtime", WorkspaceID: "artifact-workspace", ApplicationKey: "artifact-app", Agent: agentmodule.Options{ConversationProvider: &peerWebModel{}, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"artifact-review": worker}, Poll: 5 * time.Millisecond, MaxSteps: 12}}}
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
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("artifact collaboration")}}, ModuleAdapters: adapters, ApplicationRoutes: host.ToolSettingsSetupRoutes()})
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
	grantArtifacts := func(write, read bool) {
		mutateTestRolePermissions(t, host, b, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range prior {
				if !strings.HasPrefix(p.PermissionKey, sdk.ConversationToolActionPrefix+"artifact_") {
					out = append(out, p)
				}
			}
			for _, d := range sdk.ArtifactConversationTools() {
				if d.Effect == "write" && write || d.Effect != "write" && (d.Key != "artifact_read" || read) {
					out = append(out, identity.ProjectRolePermission{PermissionKey: d.ActionKey, DataScope: identity.DataScopeOwner})
				}
			}
			return out
		})
	}
	grantArtifacts(true, true)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	recipient := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", `{"client_id":"artifact-review","name":"成果协作","description":"创建并修订成果","instructions":"逐项请求确认后执行并交付原版本回执","tools":["artifact_create","artifact_edit","artifact_export","artifact_read","delegation_get","delegation_update"],"skill_keys":[],"model_key":"artifact-review","enabled":true,"max_concurrent":1}`, 200))
	conversation := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"artifact-delivery","title":"成果阅读"}`, 200))
	input := sdk.ConversationDelegationCreate{ClientID: "artifact-review-work", ConversationID: conversation.ID, AgentID: recipient.ID, Purpose: "创建并修订成果", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "完成受权成果编辑", Deliverable: "提交成果和原版本回执", CompletionConditions: []string{"创建、编辑、导出和阅读均附原回执"}}}
	var detail sdk.ConversationDelegationDetail
	if err := unmarshalPeerDetail(b.call("POST", "/agent/delegations", accountJSON(input), 200).Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	path := "/agent/delegations/" + detail.ID
	read := func() sdk.ConversationDelegationDetail {
		t.Helper()
		var d sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", path, "", 200).Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	approvals := map[string]bool{}
	deadline := time.Now().Add(120 * time.Second)
	for {
		detail = read()
		if detail.Task != nil && detail.Task.ExecutionRunID != "" {
			runPath := "/agent/conversations/" + detail.ConversationID + "/runs/" + detail.Task.ExecutionRunID
			run := accountDecode[sdk.ConversationRun](t, b.call("GET", runPath, "", 200))
			if run.Terminal() && run.Status != "completed" {
				t.Fatalf("artifact worker ended before delivery: status=%s error=%s steps=%d", run.Status, run.ErrorCode, len(run.Steps))
			}
			if run.Status == "waiting_confirmation" && run.Interaction != nil {
				item := run.Interaction
				if item.CallID != "create-original" && item.CallID != "edit-original" && item.CallID != "export-original" || approvals[item.CallID] {
					t.Fatalf("unexpected or repeated operation confirmation: %+v", item)
				}
				b.call("POST", runPath+"/respond", accountJSON(sdk.ConversationInteractionResponse{InteractionID: item.ID, ClientID: "approve-" + item.CallID, ExpectedRevision: item.Revision, Decision: "approve"}), 200)
				approvals[item.CallID] = true
			}
		}
		root := accountDecode[sdk.Conversation](t, b.call("GET", "/agent/conversations/"+conversation.ID, "", 200))
		if detail.Status == "delivered" && detail.Task != nil && detail.Task.Status == "completed" && root.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("artifact delivery did not finish: %+v", detail)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if detail.Delivery == nil || len(detail.Delivery.Conditions) != 1 || len(detail.Delivery.Conditions[0].Receipts) != 4 || len(approvals) != 3 {
		t.Fatalf("missing real artifact effects/confirmations/receipts: %+v approvals=%v", detail, approvals)
	}
	refs := detail.Delivery.Conditions[0].Receipts
	grantArtifacts(false, true)
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read")
	assertRead := func() {
		t.Helper()
		d := read()
		if d.Delivery == nil || d.Delivery.Summary != worker.summary || d.Task != nil || len(d.Messages) != 0 {
			t.Fatalf("Knowledge delivery reading coupled to mutation or execution exposed: %+v", d)
		}
		if body := b.call("GET", path+"/deliveries", "", 200).Body.String(); !strings.Contains(body, worker.summary) {
			t.Fatal("artifact delivery history hidden")
		}
		for _, ref := range refs {
			if result := readReleasedResult(t, b, d.ID, 0, ref); result.Status != "completed" {
				t.Fatal("released artifact result unavailable")
			}
		}
	}
	assertRead()
	readReleasedResultPages(t, b, detail.ID, 0, refs[0], 256)
	original := readReleasedResult(t, b, detail.ID, 0, refs[0])
	var created struct {
		Artifact sdk.ConversationArtifact `json:"artifact"`
	}
	if err := json.Unmarshal(original.Content, &created); err != nil {
		t.Fatal(err)
	}
	resource := sdk.ConversationDeliveryArtifactRead{Reference: refs[0], ArtifactID: created.Artifact.ID, Version: created.Artifact.Version}
	full := accountDecode[sdk.ConversationArtifactVersion](t, b.call("POST", path+"/delivery-artifact", accountJSON(resource), 200))
	if full.Artifact.Version != 1 || !strings.HasSuffix(full.Content.Markdown, "完整正文最后一行。") {
		t.Fatal("metadata receipt did not open the exact complete artifact")
	}
	resource.Version = 2
	b.call("POST", path+"/delivery-artifact", accountJSON(resource), 403)
	resource.Reference = refs[1]
	full = accountDecode[sdk.ConversationArtifactVersion](t, b.call("POST", path+"/delivery-artifact", accountJSON(resource), 200))
	if !strings.HasPrefix(full.Content.Markdown, "已经核对的结论。") || !strings.HasSuffix(full.Content.Markdown, "完整正文最后一行。") {
		t.Fatal("edited full artifact changed or truncated")
	}
	var savedExport struct {
		Export sdk.ConversationArtifactExport `json:"export"`
	}
	if err := json.Unmarshal(readReleasedResult(t, b, detail.ID, 0, refs[2]).Content, &savedExport); err != nil {
		t.Fatal(err)
	}
	download := resource
	download.Reference, download.ExportID = refs[2], savedExport.Export.ID
	file := b.call("POST", path+"/delivery-export", accountJSON(download), 200)
	hash := sha256.Sum256(file.Body.Bytes())
	if hex.EncodeToString(hash[:]) != savedExport.Export.SHA256 || !strings.Contains(file.Body.String(), "完整正文最后一行。") {
		t.Fatal("existing export download did not match original file")
	}
	b.call("GET", "/agent/artifact-exports/"+download.ExportID+"/download", "", 403)
	unreleased := download
	unreleased.Reference = refs[1]
	b.call("POST", path+"/delivery-export", accountJSON(unreleased), 403)
	history := accountDecode[sdk.ConversationDeliveryHistory](t, b.call("GET", path+"/deliveries", "", 200))
	for _, ref := range refs {
		readReleasedResult(t, b, detail.ID, history.Items[0].Revision, ref)
	}
	for _, mutate := range []func(*sdk.ConversationDeliveryResultRead){
		func(in *sdk.ConversationDeliveryResultRead) { in.Reference.CallID = "agreement" },
		func(in *sdk.ConversationDeliveryResultRead) { in.Reference.SHA256 = strings.Repeat("0", 64) },
		func(in *sdk.ConversationDeliveryResultRead) { in.Reference.RunID = "unreleased-run" },
		func(in *sdk.ConversationDeliveryResultRead) { in.DeliveryRevision = history.Items[0].Revision + 100 },
	} {
		in := sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: refs[0]}}
		mutate(&in)
		b.call("POST", path+"/delivery-result", accountJSON(in), 403)
	}
	setTestCollaborationPermissions(t, host, b, "view")
	b.call("POST", path+"/delivery-result", accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: refs[0]}}), 403)
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read")
	for _, ref := range refs {
		run := "/agent/conversations/" + ref.ConversationID + "/runs/" + ref.RunID
		b.call("GET", run, "", 403)
		b.call("POST", run+"/result", accountJSON(sdk.ConversationResultRead{Reference: ref, MaxBytes: 4096}), 403)
	}
	// Preference editing uses the current executable catalog. Restore the
	// action grants to change settings, then remove them before the restart.
	grantArtifacts(true, true)
	for _, key := range []string{"artifact_create", "artifact_edit", "artifact_export", "artifact_read"} {
		setting, ok := settingList(t, b)[key]
		if !ok {
			t.Fatal("artifact missing from preference catalog", key)
		}
		b.call("PUT", "/tools/preferences/"+key, accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
		if d := read(); d.Delivery != nil || d.Verification != nil {
			t.Fatal("disabled artifact tool retained released evidence", key)
		}
		b.call("POST", path+"/delivery-result", accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: refs[3]}}), 503)
		setting = settingList(t, b)[key]
		b.call("PUT", "/tools/preferences/"+key, accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	}
	grantArtifacts(false, true)
	assertRead()
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.handler = open()
	b.login("admin@example.com", changed)
	assertRead()
	resource.DeliveryRevision, download.DeliveryRevision = history.Items[0].Revision, history.Items[0].Revision
	b.call("POST", path+"/delivery-artifact", accountJSON(resource), 200)
	b.call("POST", path+"/delivery-export", accountJSON(download), 200)
	grantArtifacts(false, false)
	b.call("POST", path+"/delivery-artifact", accountJSON(resource), 403)
	b.call("POST", path+"/delivery-export", accountJSON(download), 403)
	if d := read(); d.Delivery != nil || d.Verification != nil {
		t.Fatal("revoked Knowledge artifact read exposed delivery")
	}
	if body := b.call("GET", path+"/deliveries", "", 403).Body.String(); strings.Contains(body, worker.summary) {
		t.Fatal("revoked artifact read exposed immutable delivery history")
	}
	for _, ref := range refs {
		b.call("POST", path+"/delivery-result", accountJSON(sdk.ConversationDeliveryResultRead{DeliveryRevision: history.Items[0].Revision, ConversationResultRead: sdk.ConversationResultRead{Reference: ref}}), 403)
	}
	grantArtifacts(false, true)
	assertRead()
	if os.Getenv("AGENT_ARTIFACT_DELIVERY_BROWSER") == "1" {
		project, err := filepath.Abs("../../..")
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(nil)
		origin := "http://" + server.Listener.Addr().String()
		ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Model: "peer", Files: os.DirFS(filepath.Join(project, "frontend/dist"))})
		if err != nil {
			t.Fatal(err)
		}
		server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" && r.URL.Path == "/fixture/artifact-read" {
				grantArtifacts(false, r.URL.Query().Get("allowed") == "true")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			ui.ServeHTTP(w, r)
		})
		server.Start()
		defer server.Close()
		command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/delivery-result.browser.mjs"))
		command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_CONVERSATION="+conversation.ID)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err := command.Run(); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("real Knowledge/Identity/Tools/Agent/SQLite: three confirmed writes and four saved receipts; delivery reading survives mutation grant revocation/restart, enforces tool preferences and artifact read revocation, raw execution stays hidden")
}
