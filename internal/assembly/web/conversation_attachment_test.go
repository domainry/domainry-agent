package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func TestAttachmentsThroughIdentityHTTPAndRestart(t *testing.T) {
	const initial, changed = "Initial-Attachment-Test!2", "Changed-Attachment-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "attachment-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "attachment-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "attachments.db"), RuntimeID: "attachment-runtime", WorkspaceID: "attachment-workspace", ApplicationKey: "attachment-app", Agent: agentmodule.Options{ConversationProvider: testModel{}}}
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
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
		return &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	b := newBrowser()
	b.call("GET", "/agent/conversations/missing/attachments", "", 401)
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	var conversation agentsdk.Conversation
	if err := json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"attachment-http","title":"私有附件测试"}`, 200).Body.Bytes(), &conversation); err != nil {
		t.Fatal(err)
	}
	path := "/agent/conversations/" + conversation.ID + "/attachments"
	b.call("GET", path, "", 403)
	grant := func(denied string) {
		mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, permission := range previous {
				if !strings.HasPrefix(permission.PermissionKey, agentsdk.ConversationActionPrefix+"attachments_") {
					out = append(out, permission)
				}
			}
			for _, operation := range agentsdk.ConversationHTTPDefinitions() {
				if permission := agentsdk.ConversationAttachmentPermission(operation.Operation); permission != nil && operation.Operation != denied {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: permission.Key, DataScope: identitysdk.DataScopeOwner})
				}
			}
			return out
		})
	}
	grant("")
	upload := func(query string, raw []byte, scope, origin string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "http://127.0.0.1:8091"+path+"?"+query, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-Agent-Scope", scope)
		for _, cookie := range b.cookies {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		b.handler.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("upload status %d, expected %d: %s", response.Code, want, response.Body.String())
		}
		return response
	}
	raw := []byte("# 私有附件\n\n项目金额：123.45\n<script>not executed</script>\n")
	query := url.Values{"client_id": {"browser-upload"}, "filename": {"资料.md"}}.Encode()
	upload(query, raw, b.scope, "http://untrusted.example", 403)
	upload(query, raw, "other-scope", "http://127.0.0.1:8091", 409)
	upload(query+"&permission_ids=all", raw, b.scope, "http://127.0.0.1:8091", 400)
	upload(query, make([]byte, agentsdk.ConversationAttachmentMaxBytes+1), b.scope, "http://127.0.0.1:8091", 413)
	var attachment agentsdk.ConversationAttachment
	if err := json.Unmarshal(upload(query, raw, b.scope, "http://127.0.0.1:8091", 200).Body.Bytes(), &attachment); err != nil || attachment.State != "stored" {
		t.Fatal("HTTP upload incorrectly became indexed", attachment, err)
	}
	var replay agentsdk.ConversationAttachment
	if err := json.Unmarshal(upload(query, raw, b.scope, "http://127.0.0.1:8091", 200).Body.Bytes(), &replay); err != nil || replay.ID != attachment.ID {
		t.Fatal("retry duplicated attachment", err)
	}
	downloadPath := path + "/" + attachment.ID + "/content"
	download := b.call("GET", downloadPath, "", 200)
	if !bytes.Equal(download.Body.Bytes(), raw) || download.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment;") || download.Header().Get("Cache-Control") != "private, no-store" || download.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("unsafe download or changed original bytes")
	}
	grant("attachments_download")
	b.call("GET", downloadPath, "", 403)
	grant("attachments_upload")
	upload(query, raw, b.scope, "http://127.0.0.1:8091", 403)
	grant("")
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	host = nil
	host, err = Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	b = newBrowser()
	b.login("admin@example.com", changed)
	if download := b.call("GET", downloadPath, "", 200); !bytes.Equal(download.Body.Bytes(), raw) {
		t.Fatal("host restart changed private original file")
	}
	b.call("DELETE", "/agent/conversations/"+conversation.ID+"?expected_revision=1", "", 200)
	b.call("GET", downloadPath, "", 404)
	deadline := time.Now().Add(3 * time.Second)
	for {
		paths, err := filepath.Glob(filepath.Join(options.DatabasePath+".attachments", "*", attachment.ID+".bin"))
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("conversation deletion did not physically clean original file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.call("POST", "/agent/conversations", `{"client_id":"attachment-ui","title":"附件网页验收"}`, 200)
	servePersonalToolAcceptance(t, host, options, map[string]func(){"revoke_attachment_download": func() { grant("attachments_download") }, "restore_attachments": func() { grant("") }})
}
