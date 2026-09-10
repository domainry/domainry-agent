package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func TestKnowledgeLibrariesIdentityHTTPAndRestart(t *testing.T) {
	const initial, changed = "Initial-Library-Test!2", "Changed-Library-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "library-test-signing-secret-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "library-test-encryption-secret-long-enough")
	t.Setenv("APP_ENV", "development")
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "libraries.db"), RuntimeID: "library-runtime", WorkspaceID: "library-workspace", ApplicationKey: "library-app", Agent: agentmodule.Options{ConversationProvider: testModel{}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	newBrowser := func() *browser {
		handler, e := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if e != nil {
			t.Fatal(e)
		}
		return &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	a := newBrowser()
	a.call("GET", "/agent/knowledge-libraries", "", 401)
	a.login("admin@example.com", initial)
	a.changePassword(initial, changed)
	a.call("POST", "/agent/knowledge-libraries", `{"client_id":"shared","kind":"shared","name":"项目资料"}`, 403)
	grant := func(user, denied string) {
		mutateTestRolePermissions(t, host, a, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, p := range previous {
				if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationActionPrefix+"libraries_") {
					out = append(out, p)
				}
			}
			for _, d := range agentsdk.ConversationHTTPDefinitions() {
				if p := agentsdk.KnowledgeLibraryPermission(d.Operation); p != nil && d.Operation != denied {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: p.Key, DataScope: identitysdk.DataScopeAll})
				}
			}
			return out
		}, user)
	}
	grant("admin", "")
	decode := func(raw []byte) agentsdk.KnowledgeLibrary {
		var lib agentsdk.KnowledgeLibrary
		if e := json.Unmarshal(raw, &lib); e != nil {
			t.Fatal(e)
		}
		return lib
	}
	lib := decode(a.call("POST", "/agent/knowledge-libraries", `{"client_id":"shared","kind":"shared","name":"项目资料","description":"跨部门协作"}`, 200).Body.Bytes())
	path := "/agent/knowledge-libraries/" + lib.ID
	a.call("POST", "/agent/knowledge-libraries", `{"client_id":"forged","kind":"shared","name":"forged","owner_user_id":"second"}`, 400)
	b := newBrowser()
	b.login("system_administrator@example.com", initial)
	b.changePassword(initial, changed)
	second := b.readSession()["user_id"].(string)
	grant(second, "")
	b.call("GET", path, "", 404)
	if page := b.call("GET", "/agent/knowledge-libraries", "", 200).Body.String(); strings.Contains(page, lib.ID) {
		t.Fatal("non-member library listed")
	}
	a.call("PUT", path+"/members/unknown-user", `{"role":"reader","expected_revision":1}`, 400)
	lib = decode(a.call("PUT", path+"/members/"+second, `{"role":"reader","expected_revision":1}`, 200).Body.Bytes())
	if read := decode(b.call("GET", path, "", 200).Body.Bytes()); read.Role != "reader" {
		t.Fatal("wrong role", read)
	}
	b.call("PATCH", path, fmt.Sprintf(`{"name":"denied","expected_revision":%d}`, lib.Revision), 403)
	b.call("GET", path+"/members", "", 403)
	lib = decode(a.call("PUT", path+"/members/"+second, fmt.Sprintf(`{"role":"editor","expected_revision":%d}`, lib.Revision), 200).Body.Bytes())
	if read := decode(b.call("GET", path, "", 200).Body.Bytes()); read.Role != "editor" {
		t.Fatal("role update stale")
	}
	b.call("PUT", path+"/members/admin", fmt.Sprintf(`{"role":"reader","expected_revision":%d}`, lib.Revision), 403)
	grant(second, "libraries_get")
	b.call("GET", path, "", 403)
	grant(second, "")
	b.call("GET", path, "", 200)
	personal := decode(a.call("POST", "/agent/knowledge-libraries", `{"client_id":"personal","kind":"personal","name":"个人资料"}`, 200).Body.Bytes())
	a.call("PUT", "/agent/knowledge-libraries/"+personal.ID+"/members/"+second, `{"role":"reader","expected_revision":1}`, 403)
	b.call("GET", "/agent/knowledge-libraries/"+personal.ID, "", 404)
	if e := host.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	host = nil
	host, err = Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	a = newBrowser()
	a.login("admin@example.com", changed)
	b = newBrowser()
	b.login("system_administrator@example.com", changed)
	if read := decode(b.call("GET", path, "", 200).Body.Bytes()); read.Role != "editor" {
		t.Fatal("restart lost membership")
	}
	a.call("DELETE", path+"/members/admin?expected_revision="+fmt.Sprint(lib.Revision), "", 409)
	lib = decode(a.call("DELETE", path+"/members/"+second+"?expected_revision="+fmt.Sprint(lib.Revision), "", 200).Body.Bytes())
	b.call("GET", path, "", 404)
	b.call("GET", path+"/members", "", 404)
	lib = decode(a.call("PATCH", path, fmt.Sprintf(`{"name":"项目资料（归档）","archived":true,"expected_revision":%d}`, lib.Revision), 200).Body.Bytes())
	if !lib.Archived {
		t.Fatal("archive not persisted")
	}
	// Restored active state is used for optional manual product-page acceptance.
	a.call("PATCH", path, fmt.Sprintf(`{"name":"项目资料","archived":false,"expected_revision":%d}`, lib.Revision), 200)
	servePersonalToolAcceptance(t, host, options, map[string]func(){"revoke_library_read": func() { grant("admin", "libraries_get") }, "restore_library_read": func() { grant("admin", "") }})
}
