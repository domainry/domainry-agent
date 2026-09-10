package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func TestArtifactsThroughIdentityHTTPAndPrivateStorageRestart(t *testing.T) {
	const initial, changed = "Initial-Artifact-Test!2", "Changed-Artifact-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "artifact-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "artifact-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "artifacts.db"), RuntimeID: "artifact-runtime", WorkspaceID: "artifact-workspace", ApplicationKey: "artifact-app"}
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
	b.call("GET", "/agent/artifacts", "", 401)
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	b.call("GET", "/agent/artifacts", "", 403)
	grant := func(denied string) {
		mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, p := range previous {
				if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationToolActionPrefix+"artifact_") {
					out = append(out, p)
				}
			}
			for _, tool := range agentsdk.ArtifactConversationTools() {
				if tool.Key != denied {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identitysdk.DataScopeOwner})
				}
			}
			return out
		})
	}
	grant("")
	input := agentsdk.ConversationArtifactCreate{ClientID: "http-report", Title: "网页周报", Content: agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: "# 周报\n\n## 第二节\n待核对。\n" + strings.Repeat("附录资料。\n", 7000)}}
	raw, _ := json.Marshal(input)
	var created agentsdk.ConversationArtifactVersion
	if err = json.Unmarshal(b.call("POST", "/agent/artifacts", string(raw), 200).Body.Bytes(), &created); err != nil || created.Artifact.Version != 1 {
		t.Fatal("large HTTP create failed", err)
	}
	path := "/agent/artifacts/" + created.Artifact.ID
	b.call("POST", "/agent/artifacts", `{"client_id":"forged","title":"x","content":{"kind":"markdown","markdown":"x"},"sources":{"version":1,"runs":[]}}`, 400)
	b.call("GET", path+"?version=-1", "", 400)
	b.call("PATCH", path, `{"client_id":"missing-replacement","expected_version":1,"patch":{"text":[{"find":"待核对。"}]}}`, 400)
	b.call("PATCH", path, `{"client_id":"unknown-patch-field","expected_version":1,"patch":{"text":[{"find":"待核对。","replace":"x","owner":"other"}]}}`, 400)
	change := agentsdk.ConversationArtifactEdit{ClientID: "http-edit", ExpectedVersion: 1, Patch: agentsdk.ConversationArtifactPatch{Text: []agentsdk.ConversationArtifactTextEdit{{Find: "待核对。", Replace: "已核对。"}}}}
	raw, _ = json.Marshal(change)
	var updated agentsdk.ConversationArtifactVersion
	if err = json.Unmarshal(b.call("PATCH", path, string(raw), 200).Body.Bytes(), &updated); err != nil || updated.Artifact.Version != 2 || !strings.Contains(updated.Content.Markdown, "已核对。") {
		t.Fatal("HTTP section edit failed", err)
	}
	var exported agentsdk.ConversationArtifactExport
	if err = json.Unmarshal(b.call("POST", path+"/exports", `{"client_id":"http-export","version":1,"format":"markdown"}`, 200).Body.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	downloadPath := "/agent/artifact-exports/" + exported.ID + "/download"
	download := b.call("GET", downloadPath, "", 200)
	if download.Body.String() != input.Content.Markdown || download.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || !strings.Contains(download.Header().Get("Content-Disposition"), "attachment;") || !strings.Contains(download.Header().Get("Content-Disposition"), exported.Filename) || download.Header().Get("Cache-Control") != "private, no-store" || download.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("HTTP download lost exact bytes or attachment headers")
	}
	grant("artifact_export")
	b.call("GET", path, "", 200)
	b.call("GET", downloadPath, "", 403)
	grant("artifact_read")
	b.call("GET", path, "", 403)
	b.call("GET", downloadPath, "", 403)
	grant("")
	// Close the actual SQLite host and its owned blob store, then reopen both.
	if err = host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	host, err = Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	b = newBrowser()
	b.login("admin@example.com", changed)
	if download = b.call("GET", downloadPath, "", 200); download.Body.String() != input.Content.Markdown {
		t.Fatal("restart changed exported old version")
	}
	var latest agentsdk.ConversationArtifactVersion
	if err = json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &latest); err != nil || latest.Artifact.Version != 2 || latest.Content.Markdown != updated.Content.Markdown {
		t.Fatal("restart lost latest edit", err)
	}
	// CSV uses typed decimal strings and guards text without rounding amounts.
	b.call("POST", "/agent/artifacts", `{"client_id":"bad-table","title":"费用表","content":{"kind":"table","table":{"columns":[{"key":"amount","label":"金额","type":"number"}],"rows":[[1.25]]}}}`, 400)
	var table agentsdk.ConversationArtifactVersion
	if err = json.Unmarshal(b.call("POST", "/agent/artifacts", `{"client_id":"table","title":"费用表","content":{"kind":"table","table":{"columns":[{"key":"name","label":"项目","type":"text"},{"key":"amount","label":"金额","type":"number"}],"rows":[["=SUM(A1:A2)","9007199254740993.01"]]}}}`, 200).Body.Bytes(), &table); err != nil {
		t.Fatal(err)
	}
	tablePath := "/agent/artifacts/" + table.Artifact.ID
	b.call("PATCH", tablePath, `{"client_id":"missing-cell-value","expected_version":1,"patch":{"cells":[{"row":0,"column":"amount"}]}}`, 400)
	b.call("PATCH", tablePath, `{"client_id":"missing-cell-row","expected_version":1,"patch":{"cells":[{"column":"amount","value":null}]}}`, 400)
	if err = json.Unmarshal(b.call("POST", "/agent/artifacts/"+table.Artifact.ID+"/exports", `{"client_id":"csv-export","version":1,"format":"csv"}`, 200).Body.Bytes(), &exported); err != nil || !exported.FormulaGuarded {
		t.Fatal("CSV export metadata missing", err)
	}
	download = b.call("GET", "/agent/artifact-exports/"+exported.ID+"/download", "", 200)
	if download.Body.String() != "项目,金额\r\n'=SUM(A1:A2),9007199254740993.01\r\n" {
		t.Fatal("CSV output changed exact decimal or formula guard")
	}
	var cleared agentsdk.ConversationArtifactVersion
	if err = json.Unmarshal(b.call("PATCH", tablePath, `{"client_id":"explicit-cell-clear","expected_version":1,"patch":{"cells":[{"row":0,"column":"amount","value":null}]}}`, 200).Body.Bytes(), &cleared); err != nil || cleared.Artifact.Version != 2 || cleared.Content.Table.Rows[0][1] != nil {
		t.Fatal("explicit null did not clear the exact selected cell", err)
	}
}
