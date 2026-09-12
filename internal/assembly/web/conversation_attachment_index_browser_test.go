package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-orm/query"
)

func TestPrivateAttachmentIndexBrowserRecovery(t *testing.T) {
	if os.Getenv("AGENT_ATTACHMENT_INDEX_BROWSER") != "1" {
		t.Skip("opt-in built-client browser acceptance")
	}
	const initial, changed = "Initial-Index-UI!2", "Changed-Index-UI!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "index-ui-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "index-ui-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	output := os.Getenv("AGENT_UI_TEST_OUTPUT")
	if output == "" {
		t.Fatal("AGENT_UI_TEST_OUTPUT required")
	}
	if err = os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(project, "integration/testdata/file-preview/two-page.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(output, "original.pdf"), original, 0600); err != nil {
		t.Fatal(err)
	}
	var wire sync.Mutex
	unconfirmed := os.Getenv("AGENT_UI_INDEX_CASE") == "unconfirmed"
	hidden, hiddenFetches := unconfirmed, 0
	remote, permission := "", ""
	var body []byte
	puts, deletes := 0, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire.Lock()
		defer wire.Unlock()
		if r.URL.Path == "/v1/kb/kbs/attachments/documents" {
			if r.Method == "POST" {
				puts++
				remote = r.URL.Query().Get("doc_id")
				body, _ = io.ReadAll(r.Body)
				var ids []string
				_ = json.Unmarshal([]byte(r.Header.Get("X-KB-Permission-Ids")), &ids)
				if len(ids) != 1 || !strings.HasPrefix(ids[0], "scope:agent:attachment:") || r.Header.Get("X-KB-Request-ID") == "" {
					t.Error("private scope missing")
				} else {
					permission = ids[0]
				}
				// The actual source accepted these bytes; the caller cannot see its ACK.
				http.Error(w, "synthetic lost upstream acknowledgement", 503)
				return
			}
			if r.Method == "DELETE" {
				deletes++
				remote = ""
				io.WriteString(w, `{"err_code":0}`)
				return
			}
			w.WriteHeader(405)
			return
		}
		var in struct {
			ID          string   `json:"doc_id"`
			Permissions []string `json:"permission_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if hidden && remote != "" && in.ID == remote {
			hiddenFetches++
			io.WriteString(w, `{"err_code":1004}`)
			return
		}
		if remote == "" || in.ID != remote || len(in.Permissions) != 1 || in.Permissions[0] != permission {
			io.WriteString(w, `{"err_code":1004}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"err_code": 0, "data": map[string]any{"doc_id": remote, "status": "INDEXED", "body": "Connector index UI fixture"}})
	}))
	defer upstream.Close()
	knowledge := agentmodule.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "synthetic", TeamID: "team", KBID: "attachments", WorkspaceID: "index-ui-workspace", ResponseMapping: &agentmodule.KnowledgeResponseMapping{Search: &agentmodule.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Excerpt: "/body"}, Fetch: &agentmodule.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Excerpt: "/body"}}}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "index-ui.db"), RuntimeID: "index-ui-runtime", WorkspaceID: knowledge.WorkspaceID, ApplicationKey: "index-ui-app", Agent: agentmodule.Options{ConversationProvider: privateAttachmentWebModel{}, AttachmentKnowledge: []agentmodule.KnowledgeConfig{knowledge}, ConversationOptions: agentmodule.ConversationOptions{DocumentPoll: 10 * time.Millisecond}}}
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	var gate sync.RWMutex
	var host *Host
	var handler http.Handler
	var b *browser
	open := func(withSource bool) {
		t.Helper()
		current := options
		if !withSource {
			current.Agent.AttachmentKnowledge = nil
		}
		host, err = Open(t.Context(), current)
		if err != nil {
			t.Fatal(err)
		}
		handler, err = webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Files: os.DirFS(filepath.Join(project, "frontend/dist"))})
		if err != nil {
			t.Fatal(err)
		}
		// The test helper's normal origin remains fixed; use a dedicated handler.
		testHandler, e := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: os.DirFS(filepath.Join(project, "frontend/dist"))})
		if e != nil {
			t.Fatal(e)
		}
		b = &browser{t: t, handler: testHandler, cookies: map[string]*http.Cookie{}}
	}
	open(true)
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grant := func(denied string) {
		mutateTestRolePermissions(t, host, b, func(prior []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, p := range prior {
				if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationActionPrefix+"attachments_") {
					out = append(out, p)
				}
			}
			for _, d := range agentsdk.ConversationHTTPDefinitions() {
				if p := agentsdk.ConversationAttachmentPermission(d.Operation); p != nil && d.Operation != denied {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: p.Key, DataScope: identitysdk.DataScopeOwner})
				}
			}
			return out
		})
	}
	grant("")
	var conversation agentsdk.Conversation
	if err = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"index-ui","title":"附件索引验收"}`, 200).Body.Bytes(), &conversation); err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__acceptance/") {
			gate.Lock()
			defer gate.Unlock()
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			switch r.URL.Path {
			case "/__acceptance/source-off", "/__acceptance/source-on", "/__acceptance/restart":
				if e := host.Close(context.Background()); e != nil {
					t.Error(e)
					w.WriteHeader(500)
					return
				}
				host = nil
				open(r.URL.Path != "/__acceptance/source-off")
				b.login("admin@example.com", changed)
			case "/__acceptance/revoke-check":
				grant("attachments_check_index")
			case "/__acceptance/restore":
				grant("")
			case "/__acceptance/reveal":
				wire.Lock()
				hidden = false
				wire.Unlock()
			case "/__acceptance/state":
				wire.Lock()
				defer wire.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]int{"puts": puts, "deletes": deletes, "hidden_fetches": hiddenFetches})
				return
			default:
				w.WriteHeader(404)
				return
			}
			w.WriteHeader(204)
			return
		}
		gate.RLock()
		defer gate.RUnlock()
		handler.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	node := os.Getenv("AGENT_NODE_BINARY")
	if node == "" {
		t.Fatal("AGENT_NODE_BINARY required")
	}
	script := "attachment-index.browser.mjs"
	if unconfirmed {
		script = "attachment-unconfirmed.browser.mjs"
	}
	command := exec.CommandContext(ctx, node, filepath.Join(project, "frontend/tests", script))
	command.Dir = project
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_CONVERSATION="+conversation.ID)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal("index UI browser failed", err)
	}
	wire.Lock()
	same := bytes.Equal(body, original)
	countPut, countDelete := puts, deletes
	countHidden := hiddenFetches
	wire.Unlock()
	if !same || countPut != 1 || countDelete != 1 {
		t.Fatal("remote writes duplicated or original changed", same, countPut, countDelete)
	}
	if unconfirmed && countHidden < 2 {
		t.Fatal("unconfirmed upload was not independently inspected across restart", countHidden)
	}
	q, args, err := query.NewSelectBuilder(host.registrar.Renderer, "_agent_conversation_attachments").Columns("payload_json").Build()
	if err != nil {
		t.Fatal(err)
	}
	var record persistence.ConversationAttachmentRecord
	deadline := time.Now().Add(5 * time.Second)
	for {
		var raw []byte
		if err = host.db.QueryRowContext(t.Context(), q, args...).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		if record.Attachment.State == "deleted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup unfinished", record.Attachment.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if record.BodyRef != "" || record.Index == nil || record.Index.PutAcknowledged || !record.Index.IndexObserved || !record.Index.DeleteAcknowledged || record.Index.LastCheckRevision < 1 {
		t.Fatal("recovery markers incorrect", record.Index)
	}
	files, err := filepath.Glob(filepath.Join(options.DatabasePath+".attachments", "*", "*.bin"))
	if err != nil || len(files) != 0 {
		t.Fatal("original not removed", files, err)
	}
	if _, err = os.Stat(options.DatabasePath + ".parses"); !os.IsNotExist(err) {
		t.Fatal("source parse cache appeared", err)
	}
	audit, _ := json.MarshalIndent(map[string]any{"complete": true, "remote_puts": countPut, "remote_deletes": countDelete, "hidden_fetches": countHidden, "unconfirmed_upload_scenario": unconfirmed, "original_bytes_preserved": same, "upstream_put_acknowledged": record.Index.PutAcknowledged, "positive_index_observed": record.Index.IndexObserved, "check_revision": record.Index.LastCheckRevision, "original_cleaned": true, "source_parser": false}, "", "  ")
	if err = os.WriteFile(filepath.Join(output, "host-audit.json"), audit, 0600); err != nil {
		t.Fatal(err)
	}
}
