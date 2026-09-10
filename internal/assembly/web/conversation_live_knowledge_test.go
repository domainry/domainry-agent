package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

// Requires an already indexed synthetic document created for this acceptance.
// It neither uploads private files nor deletes the caller's knowledge base.
func TestLiveKnowledgeThroughIdentityHTTP(t *testing.T) {
	if os.Getenv("AGENT_KNOWLEDGE_LIVE") != "1" {
		t.Skip("opt-in real model and knowledge service acceptance")
	}
	doc, marker := os.Getenv("AGENT_KNOWLEDGE_LIVE_DOC_ID"), os.Getenv("AGENT_KNOWLEDGE_LIVE_MARKER")
	if !strings.HasPrefix(doc, "domainry-agent-acceptance-") || !strings.HasPrefix(marker, "QINGHE-") {
		t.Fatal("synthetic acceptance document and marker are required")
	}
	modelConfig := provider.ConversationModelConfigFromEnvironment()
	model, err := provider.NewConversationModel(modelConfig)
	if err != nil {
		t.Fatal(err)
	}
	knowledge := provider.KnowledgeConfigFromEnvironment()
	knowledge.WorkspaceID = "live-knowledge-workspace"
	if knowledge.ResponseMapping == nil {
		t.Fatal("verified response mapping is required")
	}
	const initial, changed = "Initial-Live-Knowledge-Test!2", "Changed-Live-Knowledge-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "live-knowledge-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "live-knowledge-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "knowledge.db"), RuntimeID: "live-knowledge-runtime", WorkspaceID: knowledge.WorkspaceID, ApplicationKey: "live-knowledge-app", Agent: agentmodule.Options{ConversationProvider: model, ConversationModel: modelConfig.Model, Knowledge: knowledge, ConversationOptions: agentmodule.ConversationOptions{Poll: 20 * time.Millisecond}}}
	var host *Host
	var b *browser
	open := func() {
		t.Helper()
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		handler, e := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: modelConfig.Model, Files: fstest.MapFS{"index.html": {Data: []byte("live knowledge acceptance")}}})
		if e != nil {
			t.Fatal(e)
		}
		b = &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	open()
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	grant := func(enabled bool) {
		mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := make([]identitysdk.ProjectRolePermission, 0, len(previous)+2)
			for _, permission := range previous {
				if permission.PermissionKey != agentsdk.ConversationToolActionPrefix+"knowledge_search" && permission.PermissionKey != agentsdk.ConversationToolActionPrefix+"knowledge_read" {
					out = append(out, permission)
				}
			}
			if enabled {
				for _, tool := range agentsdk.KnowledgeConversationTools() {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identitysdk.DataScopeOwner})
				}
			}
			return out
		})
	}
	grant(true)
	decode := func(raw []byte, out any) {
		t.Helper()
		if e := json.Unmarshal(raw, out); e != nil {
			t.Fatal(e)
		}
	}
	record := func(name string, value any) {
		t.Helper()
		if directory := os.Getenv("AGENT_LIVE_EVIDENCE_DIR"); directory != "" {
			raw, e := json.MarshalIndent(value, "", "  ")
			if e == nil {
				e = os.WriteFile(filepath.Join(directory, name+".json"), raw, 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	var c agentsdk.Conversation
	decode(b.call("POST", "/agent/conversations", `{"client_id":"live-knowledge","title":"真实知识文档验收"}`, 200).Body.Bytes(), &c)
	base := "/agent/conversations/" + c.ID
	input, _ := json.Marshal(agentsdk.ConversationSend{ClientMessageID: "read-guide", Message: "检索验收标识 " + marker + " 对应的青禾合成验收指南，搜索后读取命中的文档。根据资料回答付款期限、周报的三个章节和费用样例合计，逐项引用实际取得的资料。如果文档还在索引或没有正文，请明确说明。"})
	var run agentsdk.ConversationRun
	decode(b.call("POST", base+"/messages", string(input), 202).Body.Bytes(), &run)
	deadline := time.Now().Add(5 * time.Minute)
	lastState := ""
	for time.Now().Before(deadline) {
		decode(b.call("GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
		calls := []string{}
		for _, step := range run.Steps {
			for _, call := range step.Calls {
				calls = append(calls, call.Name+":"+call.Status)
			}
		}
		state := run.Status + " " + strings.Join(calls, ",")
		if state != lastState {
			t.Log(state)
			lastState = state
		}
		if run.Terminal() || run.Waiting() {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	record("knowledge-run", run)
	if run.Status != "completed" || run.AccessError != "" {
		t.Fatalf("real knowledge execution failed: status=%s error=%s access=%s", run.Status, run.ErrorCode, run.AccessError)
	}
	called := map[string]bool{}
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.Status == "completed" {
				called[call.Name] = true
			}
			for _, citation := range call.Citations {
				if citation.DocumentID != doc || citation.Title == "" || citation.Excerpt == "" || citation.URL != "" {
					t.Fatal("citation metadata does not match the synthetic document")
				}
			}
		}
	}
	if !called["knowledge_search"] || !called["knowledge_read"] {
		t.Fatal("model did not actually search and read")
	}
	readReply := func() agentsdk.ConversationMessage {
		t.Helper()
		var page agentsdk.ConversationMessagePage
		decode(b.call("GET", base+"/messages", "", 200).Body.Bytes(), &page)
		for _, message := range page.Items {
			if message.Role == "assistant" && message.RunID == run.ID {
				return message
			}
		}
		t.Fatal("missing persisted reply")
		return agentsdk.ConversationMessage{}
	}
	reply := readReply()
	for _, fact := range []string{"30", "本周进展", "风险", "下周计划", "200"} {
		if !strings.Contains(reply.Content, fact) {
			t.Fatalf("reply omitted source fact %s", fact)
		}
	}
	if reply.AccessError != "" || len(reply.Citations) < 2 {
		t.Fatal("reply lacks verified citations")
	}
	for _, citation := range reply.Citations {
		if citation.DocumentID != doc || !strings.Contains(reply.Content, "[[cite:"+citation.ID+"]]") {
			t.Fatal("reply has an unbound citation")
		}
	}
	record("knowledge-reply", reply)
	assertHidden := func(name string, restricted agentsdk.ConversationMessage) {
		t.Helper()
		record(name, restricted)
		if restricted.AccessError == "" || len(restricted.Citations) != 0 || restricted.Content == reply.Content {
			t.Fatalf("%s retained source-derived reply or citations", name)
		}
		// The public API substitutes an unavailable-source notice. Check that
		// evidence is gone; an empty body is not part of the API contract.
		for _, fact := range []string{marker, "30", "本周进展", "风险", "下周计划", "200", "[[cite:"} {
			if strings.Contains(restricted.Content, fact) {
				t.Fatalf("%s retained source fact %s", name, fact)
			}
		}
	}
	stream := b.call("GET", base+"/runs/"+run.ID+"/events/stream?scope="+b.scope, "", 200).Body.String()
	if !strings.Contains(stream, reply.Citations[0].ID) || !strings.Contains(stream, "tool.completed") {
		t.Fatal("SSE lost real citations")
	}
	if err = host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	host = nil
	open()
	b.login("admin@example.com", changed)
	restored := readReply()
	if restored.Content != reply.Content || string(mustKnowledgeJSON(restored.Citations)) != string(mustKnowledgeJSON(reply.Citations)) {
		t.Fatal("restart changed persisted evidence")
	}
	grant(false)
	restricted := readReply()
	assertHidden("knowledge-after-action-revocation", restricted)
	var restrictedRun agentsdk.ConversationRun
	decode(b.call("GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &restrictedRun)
	if restrictedRun.AccessError == "" || restrictedRun.DraftText != "" || len(restrictedRun.Steps) != 0 {
		t.Fatal("host action revocation retained public execution evidence")
	}
	b.call("GET", base+"/runs/"+run.ID+"/events/stream?scope="+b.scope, "", http.StatusForbidden)
	grant(true)
	if restored = readReply(); restored.AccessError != "" || restored.Content != reply.Content {
		t.Fatal("restoring host access did not restore verified reply")
	}
	t.Log("verified real model search/read, source facts, citations, SSE, host restart and Identity action revocation/restoration; remote private-document ACL is not covered")
	servePersonalToolAcceptance(t, host, options, map[string]func(){"knowledge/revoke": func() { grant(false) }, "knowledge/restore": func() { grant(true) }})
	if os.Getenv("AGENT_KNOWLEDGE_LIVE_EXPECT_DELETED") == "1" {
		restricted = readReply()
		assertHidden("knowledge-after-delete", restricted)
		t.Log("verified remote deletion hides the persisted source-derived reply")
	}
}

func mustKnowledgeJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }
