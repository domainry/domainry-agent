package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type libraryPermissionFixture struct {
	KBID         string `json:"kb_id"`
	DocumentID   string `json:"doc_id"`
	Marker       string `json:"marker"`
	PermissionID string `json:"permission_id"`
}
type libraryPermissionPlan struct {
	LibraryID        string `json:"library_id"`
	Query            string `json:"query"`
	DocumentID       string `json:"doc_id"`
	ForgePermissions bool   `json:"forge_permissions,omitempty"`
}

// A deterministic model makes unauthorized selectors reproducible. Identity,
// HTTP, persistence, Module and the official Connector are production code.
type libraryPermissionModel struct {
	libraryKnowledgeWebModel
	calls *atomic.Int32
}

func (m libraryPermissionModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	if m.calls != nil {
		m.calls.Add(1)
	}
	var plan libraryPermissionPlan
	for i := len(in.Messages) - 1; i >= 0; i-- {
		if in.Messages[i].Role == "user" {
			_ = json.Unmarshal([]byte(in.Messages[i].Content), &plan)
			break
		}
	}
	call := func(name, id string, args any) (agentsdk.ConversationStepResult, error) {
		raw, _ := json.Marshal(args)
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: id, Name: name, Arguments: string(raw)}}}, FinishReason: "tool_calls"}, nil
	}
	answer := func(value string) (agentsdk.ConversationStepResult, error) {
		if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: value}); err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: value}, FinishReason: "stop"}, nil
	}
	last := in.Messages[len(in.Messages)-1]
	if last.Role == "user" {
		args := map[string]any{"library_id": plan.LibraryID, "query": plan.Query}
		if plan.ForgePermissions {
			args["permission_ids"] = []string{"admin:all"}
		}
		return call("knowledge_search", "search", args)
	}
	var result agentsdk.ConversationToolResult
	var evidence agentsdk.ConversationKnowledgeResult
	if json.Unmarshal([]byte(last.Content), &result) != nil || result.Status != "completed" || json.Unmarshal(result.Content, &evidence) != nil {
		return answer("资料不可用。")
	}
	if last.ToolCallID == "search" {
		return call("knowledge_read", "read", map[string]string{"library_id": plan.LibraryID, "doc_id": plan.DocumentID})
	}
	for _, citation := range evidence.Citations {
		if strings.Contains(citation.Excerpt, plan.Query) {
			return answer(citation.Excerpt + " [[cite:" + citation.ID + "]]")
		}
	}
	return answer("没有可核对的正文片段。")
}

type libraryPermissionObservation struct {
	KBID          string   `json:"kb_id"`
	Operation     string   `json:"operation"`
	PermissionIDs []string `json:"permission_ids"`
	HTTPStatus    int      `json:"http_status"`
}
type libraryPermissionTransport struct {
	base         http.RoundTripper
	mu           sync.Mutex
	observations []libraryPermissionObservation
	pauseKB      string
	paused       chan struct{}
	release      chan struct{}
}

func (r *libraryPermissionTransport) RoundTrip(in *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(io.LimitReader(in.Body, 128*1024))
	if err != nil {
		return nil, err
	}
	in.Body = io.NopCloser(bytes.NewReader(raw))
	var body struct {
		KBID string   `json:"kb_id"`
		IDs  []string `json:"permission_ids"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	out, err := r.base.RoundTrip(in)
	status := 0
	if out != nil {
		status = out.StatusCode
	}
	r.mu.Lock()
	r.observations = append(r.observations, libraryPermissionObservation{body.KBID, filepath.Base(in.URL.Path), slices.Clone(body.IDs), status})
	var release chan struct{}
	if r.pauseKB == body.KBID && strings.HasSuffix(in.URL.Path, "/fetch") {
		release = r.release
		r.pauseKB = ""
		close(r.paused)
	}
	r.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-in.Context().Done():
			if out != nil {
				_ = out.Body.Close()
			}
			return nil, in.Context().Err()
		}
	}
	return out, err
}

func (r *libraryPermissionTransport) pauseNextFetch(kb string) (<-chan struct{}, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pauseKB, r.paused, r.release = kb, make(chan struct{}), make(chan struct{})
	release := r.release
	var once sync.Once
	return r.paused, func() { once.Do(func() { close(release) }) }
}
func (r *libraryPermissionTransport) snapshot() []libraryPermissionObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.observations)
}

func TestLibraryPrivatePermissionsIdentityHTTP(t *testing.T) {
	fixtures := []libraryPermissionFixture{{"private-a", "same-doc", "A-PRIVATE-MARKER", "scope:a:read"}, {"private-b", "same-doc", "B-PRIVATE-MARKER", "scope:b:read"}, {"shared", "shared-doc", "SHARED-MARKER", "scope:shared:read"}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			KBID       string   `json:"kb_id"`
			DocumentID string   `json:"doc_id"`
			IDs        []string `json:"permission_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		for _, f := range fixtures {
			if in.KBID != f.KBID {
				continue
			}
			if !slices.Equal(in.IDs, []string{f.PermissionID}) {
				t.Error("wrong library permission scope")
				http.Error(w, "denied", 403)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/search") {
				_ = json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"hits": []any{map[string]string{"doc_id": f.DocumentID, "title": "Synthetic guide", "snippet": f.Marker}}}})
			} else if in.DocumentID == f.DocumentID {
				_ = json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"doc_id": f.DocumentID, "title": "Synthetic guide", "chunks": []any{map[string]string{"content": f.Marker}}}})
			} else {
				_, _ = w.Write([]byte(`{"err_code":1004,"err_msg":"document not found"}`))
			}
			return
		}
		t.Error("unconfigured KB requested")
		http.Error(w, "not found", 404)
	}))
	defer upstream.Close()
	runLibraryPermissionIdentity(t, provider.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture-key", TeamID: "fixture-team"}, fixtures)
}

func TestLiveLibraryPrivatePermissionsIdentityHTTP(t *testing.T) {
	if os.Getenv("AGENT_LIBRARY_PERMISSIONS_LIVE") != "1" {
		t.Skip("opt-in private library permission acceptance")
	}
	var fixtures []libraryPermissionFixture
	if json.Unmarshal([]byte(os.Getenv("AGENT_LIBRARY_PERMISSIONS_FIXTURES")), &fixtures) != nil || len(fixtures) != 3 {
		t.Fatal("three dedicated private fixtures are required")
	}
	seen := map[string]bool{}
	for _, f := range fixtures {
		if f.KBID == "" || seen[f.KBID] || !strings.HasPrefix(f.DocumentID, "domainry-agent-acceptance-") || !strings.HasPrefix(f.Marker, "QINGHE-") || !strings.HasPrefix(f.PermissionID, "scope:domainry-k01:") {
			t.Fatal("invalid or shared private fixture scope")
		}
		seen[f.KBID] = true
	}
	config := provider.KnowledgeConfigFromEnvironment()
	if config.BaseURL != "https://api.verdent.ai" {
		t.Fatal("verified Verdent origin required")
	}
	runLibraryPermissionIdentity(t, config, fixtures)
}

func runLibraryPermissionIdentity(t *testing.T, config provider.KnowledgeConfig, fixtures []libraryPermissionFixture) {
	t.Helper()
	const initial, changed = "Initial-Library-Permissions!2", "Changed-Library-Permissions!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "library-permissions-test-signing-secret")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "library-permissions-test-data-secret")
	t.Setenv("APP_ENV", "development")
	transport := &libraryPermissionTransport{base: http.DefaultTransport}
	var modelCalls atomic.Int32
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "permissions.db"), RuntimeID: "library-permission-runtime", WorkspaceID: "library-permission-workspace", ApplicationKey: "library-permission-app", Agent: agentmodule.Options{ConversationProvider: libraryPermissionModel{calls: &modelCalls}, ConversationOptions: agentmodule.ConversationOptions{Poll: 10 * time.Millisecond}}}
	var host *Host
	var a, b *browser
	newBrowser := func() *browser {
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("permission test")}}})
		if err != nil {
			t.Fatal(err)
		}
		return &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	open := func(restart bool) {
		if host != nil {
			if err := host.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			host = nil
		}
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		a, b = newBrowser(), newBrowser()
		password := initial
		if restart {
			password = changed
		}
		a.login("admin@example.com", password)
		b.login("system_administrator@example.com", password)
		if !restart {
			a.changePassword(initial, changed)
			b.changePassword(initial, changed)
		}
	}
	open(false)
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	second := b.readSession()["user_id"].(string)
	grant := func(user string, enabled bool) {
		mutateTestRolePermissions(t, host, a, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, p := range previous {
				if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationActionPrefix+"libraries_") && !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationToolActionPrefix+"knowledge_") {
					out = append(out, p)
				}
			}
			for _, op := range agentsdk.ConversationHTTPDefinitions() {
				if p := agentsdk.KnowledgeLibraryPermission(op.Operation); p != nil && (enabled || op.Operation != "libraries_get") {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: p.Key, DataScope: identitysdk.DataScopeAll})
				}
			}
			for _, tool := range agentsdk.LibraryKnowledgeConversationTools() {
				out = append(out, identitysdk.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identitysdk.DataScopeOwner})
			}
			return out
		}, user)
	}
	grant("admin", true)
	grant(second, true)
	decode := func(raw []byte) agentsdk.KnowledgeLibrary {
		var out agentsdk.KnowledgeLibrary
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	personalA := decode(a.call("POST", "/agent/knowledge-libraries", `{"client_id":"personal-a","kind":"personal","name":"A 的个人资料"}`, 200).Body.Bytes())
	personalB := decode(b.call("POST", "/agent/knowledge-libraries", `{"client_id":"personal-b","kind":"personal","name":"B 的个人资料"}`, 200).Body.Bytes())
	shared := decode(a.call("POST", "/agent/knowledge-libraries", `{"client_id":"shared","kind":"shared","name":"共享权限验收资料"}`, 200).Body.Bytes())
	libraries := []agentsdk.KnowledgeLibrary{personalA, personalB, shared}
	metadata := "/data"
	for i, library := range libraries {
		c := config
		c.KBID, c.WorkspaceID = fixtures[i].KBID, options.WorkspaceID
		c.PermissionIDs, c.AuthorizeWorkspace, c.Transport = nil, nil, nil
		c.Client = &http.Client{Transport: transport, Timeout: 30 * time.Second}
		c.ResponseMapping = &agentmodule.KnowledgeResponseMapping{Search: &agentmodule.KnowledgeCitationMapping{Items: "/data/hits", Many: true, DocumentID: "/doc_id", Title: "/title", Excerpt: "/snippet"}, Fetch: &agentmodule.KnowledgeCitationMapping{Items: "/data/chunks", Many: true, MetadataObject: &metadata, DocumentID: "/doc_id", Title: "/title", Excerpt: "/content"}}
		options.Agent.KnowledgeLibraries = append(options.Agent.KnowledgeLibraries, agentmodule.KnowledgeLibraryConfig{LibraryID: library.ID, Knowledge: c, PermissionIDs: []string{fixtures[i].PermissionID}})
	}
	open(true)
	passed := []string{}
	runs := map[string]string{}
	var inflightModelBefore, inflightModelAfter int32
	var evidenceMu sync.Mutex
	defer func() {
		evidenceMu.Lock()
		defer evidenceMu.Unlock()
		if dir := os.Getenv("AGENT_LIVE_EVIDENCE_DIR"); filepath.IsAbs(dir) {
			raw, err := json.MarshalIndent(map[string]any{"passed": passed, "runs": runs, "observations": transport.snapshot(), "complete": !t.Failed(), "model": "deterministic permission probe", "real_identity": true, "real_knowledge": os.Getenv("AGENT_LIBRARY_PERMISSIONS_LIVE") == "1", "inflight_model_calls_before_revocation": inflightModelBefore, "inflight_model_calls_after_revocation": inflightModelAfter}, "", "  ")
			if err == nil {
				err = os.WriteFile(filepath.Join(dir, "library-permission-report.json"), raw, 0600)
			}
			if err != nil {
				t.Error(err)
			}
		}
	}()
	send := func(client *browser, name string, index int, forge bool, allow bool, documents ...string) string {
		t.Helper()
		var conversation agentsdk.Conversation
		body, _ := json.Marshal(agentsdk.ConversationCreate{ClientID: name, Title: "资料权限验收 " + name})
		_ = json.Unmarshal(client.call("POST", "/agent/conversations", string(body), 200).Body.Bytes(), &conversation)
		base := "/agent/conversations/" + conversation.ID
		doc := fixtures[index].DocumentID
		if len(documents) > 0 {
			doc = documents[0]
		}
		plan, _ := json.Marshal(libraryPermissionPlan{libraries[index].ID, fixtures[index].Marker, doc, forge})
		body, _ = json.Marshal(agentsdk.ConversationSend{ClientMessageID: "query", Message: string(plan)})
		var run agentsdk.ConversationRun
		_ = json.Unmarshal(client.call("POST", base+"/messages", string(body), 202).Body.Bytes(), &run)
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			_ = json.Unmarshal(client.call("GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
			if run.Terminal() || run.Waiting() {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		// Revoking membership while a fetch is in flight also invalidates the
		// earlier search evidence. The execution must fail closed before another
		// model step instead of requiring an assistant reply after revocation.
		revokedWhileRunning := name == "revoked-during-fetch" && run.Status == "failed" && run.ErrorCode == "knowledge_access_denied"
		if run.Status != "completed" && !revokedWhileRunning {
			t.Fatalf("%s run: %s %s", name, run.Status, run.ErrorCode)
		}
		var messages agentsdk.ConversationMessagePage
		_ = json.Unmarshal(client.call("GET", base+"/messages", "", 200).Body.Bytes(), &messages)
		found := false
		for _, message := range messages.Items {
			if !allow && message.Role == "assistant" && len(message.Citations) > 0 {
				t.Fatalf("%s returned unauthorized citations", name)
			}
			if message.Role == "assistant" && strings.Contains(message.Content, fixtures[index].Marker) && len(message.Citations) > 0 {
				found = true
			}
		}
		if found != allow {
			t.Fatalf("%s evidence visibility mismatch: %t", name, found)
		}
		if !allow {
			failedRead := false
			for _, step := range run.Steps {
				for _, call := range step.Calls {
					if len(documents) > 0 && call.Name == "knowledge_search" {
						continue
					}
					failedRead = failedRead || call.Name == "knowledge_read" && call.Status == "failed"
					if call.Status == "completed" || len(call.Citations) > 0 {
						t.Fatalf("%s unauthorized tool produced evidence", name)
					}
				}
			}
			if len(documents) > 0 && !failedRead && !revokedWhileRunning {
				t.Fatalf("%s did not reject the private read", name)
			}
		}
		evidenceMu.Lock()
		runs[name] = run.ID
		passed = append(passed, name)
		evidenceMu.Unlock()
		return base
	}
	noOutbound := func(name string, action func()) {
		t.Helper()
		before := len(transport.snapshot())
		action()
		if len(transport.snapshot()) != before {
			t.Fatalf("%s reached remote knowledge", name)
		}
	}
	send(a, "personal-a-owner", 0, false, true)
	send(b, "personal-b-owner", 1, false, true)
	noOutbound("personal-isolation", func() { send(b, "b-cannot-read-a", 0, false, false); send(a, "a-cannot-read-b", 1, false, false) })
	noOutbound("non-member", func() { send(b, "shared-non-member", 2, false, false) })
	path := "/agent/knowledge-libraries/" + shared.ID
	member := func(role string) {
		shared = decode(a.call("PUT", path+"/members/"+second, fmt.Sprintf(`{"role":%q,"expected_revision":%d}`, role, shared.Revision), 200).Body.Bytes())
	}
	member("reader")
	base := send(b, "shared-reader", 2, false, true)
	send(b, "foreign-document-id", 2, false, false, fixtures[0].DocumentID)
	noOutbound("model-scope-forgery", func() { send(b, "forged-permissions", 2, true, false) })
	visible := func(want bool) {
		t.Helper()
		raw := b.call("GET", base+"/messages", "", 200).Body.String()
		if strings.Contains(raw, `"citations":[{`) != want {
			t.Fatalf("shared history visibility: want %t", want)
		}
		var run agentsdk.ConversationRun
		_ = json.Unmarshal(b.call("GET", base+"/runs/"+runs["shared-reader"], "", 200).Body.Bytes(), &run)
		if !want {
			for _, step := range run.Steps {
				for _, call := range step.Calls {
					if len(call.Citations) > 0 {
						t.Fatal("revoked history retained citations")
					}
				}
			}
		}
	}
	open(true)
	visible(true)
	passed = append(passed, "host-restart")
	grant(second, false)
	noOutbound("identity-revocation", func() { visible(false) })
	passed = append(passed, "identity-revocation")
	grant(second, true)
	visible(true)
	passed = append(passed, "identity-restored")
	shared = decode(a.call("DELETE", path+"/members/"+second+"?expected_revision="+fmt.Sprint(shared.Revision), "", 200).Body.Bytes())
	noOutbound("membership-revocation", func() { visible(false); send(b, "removed-member-new-request", 2, false, false) })
	passed = append(passed, "membership-revocation")
	member("editor")
	visible(true)
	passed = append(passed, "editor-read")
	b.call("PATCH", path, fmt.Sprintf(`{"name":"Unauthorized rename","expected_revision":%d}`, shared.Revision), 403)
	member("manager")
	shared = decode(b.call("PATCH", path, fmt.Sprintf(`{"name":"共享权限验收资料","expected_revision":%d}`, shared.Revision), 200).Body.Bytes())
	visible(true)
	passed = append(passed, "manager-read-and-manage")
	member("reader")
	b.call("PATCH", path, fmt.Sprintf(`{"name":"Unauthorized rename","expected_revision":%d}`, shared.Revision), 403)
	visible(true)
	passed = append(passed, "reader-cannot-manage")
	paused, release := transport.pauseNextFetch(fixtures[2].KBID)
	defer release()
	done := make(chan struct{})
	go func() {
		defer close(done)
		send(b, "revoked-during-fetch", 2, false, false, fixtures[2].DocumentID)
	}()
	select {
	case <-paused:
	case <-time.After(time.Minute):
		t.Fatal("remote fetch did not reach the revocation barrier")
	}
	inflightModelBefore = modelCalls.Load()
	shared = decode(a.call("DELETE", path+"/members/"+second+"?expected_revision="+fmt.Sprint(shared.Revision), "", 200).Body.Bytes())
	release()
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatal("revoked remote fetch did not finish")
	}
	inflightModelAfter = modelCalls.Load()
	if inflightModelAfter != inflightModelBefore {
		t.Fatal("revoked in-flight evidence reached another model call")
	}
	member("reader")
	for _, seen := range transport.snapshot() {
		matched := false
		for _, f := range fixtures {
			if f.KBID == seen.KBID && slices.Equal(seen.PermissionIDs, []string{f.PermissionID}) {
				matched = true
			}
		}
		if !matched || seen.HTTPStatus != 200 {
			t.Fatal("unexpected remote scope or response")
		}
	}
	t.Logf("Private library acceptance: %d cases, %d remote requests", len(passed), len(transport.snapshot()))
}
