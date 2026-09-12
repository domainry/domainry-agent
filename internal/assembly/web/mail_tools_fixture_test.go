package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
	mail "github.com/domainry/domainry-connector-sdk/mail"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

const mailReadScope = "https://www.googleapis.com/auth/gmail.readonly"
const mailBasicScope = "https://www.googleapis.com/auth/gmail.metadata"
const mailFixtureBody = "MAIL-BODY：请李明在 2026-09-14 前核对预算 3200 元并回复。负责人未确认，请勿宣称已承诺。"

type mailProductFixture struct {
	*accountFixture
	partial                 atomic.Bool
	vendorCalls, modelCalls atomic.Int32
}

func newMailProductFixture(t *testing.T) *mailProductFixture {
	t.Helper()
	f := &mailProductFixture{accountFixture: newAccountFixture(t)}
	f.close()
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.vendorCalls.Add(1)
		f.mu.Lock()
		grant := f.tokenGrants[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		full := strings.Contains(grant, mailReadScope)
		if !full && (!strings.Contains(grant, mailBasicScope) || r.URL.Query().Get("q") != "" || r.URL.Query().Get("format") == "full") {
			http.Error(w, "denied", 403)
			return
		}
		if r.Method != "GET" {
			t.Error("mail read attempted vendor mutation")
			http.Error(w, "read only", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		switch r.URL.Path {
		case "/gmail/v1/users/me/messages":
			if q.Get("includeSpamTrash") != "false" {
				t.Error("mailbox scope changed")
			}
			if q.Get("q") != "" && q.Get("q") != "subject:预算" {
				t.Error("native query changed")
			}
			out := map[string]any{"messages": []any{map[string]string{"id": "mail-1", "threadId": "thread-budget"}}, "resultSizeEstimate": 1}
			if q.Get("q") != "" {
				if q.Get("pageToken") == "mail-page-2" {
					out["messages"] = []any{map[string]string{"id": "mail-2", "threadId": "thread-budget"}}
				} else {
					out["nextPageToken"] = "mail-page-2"
				}
			}
			json.NewEncoder(w).Encode(out)
		case "/gmail/v1/users/me/messages/mail-1", "/gmail/v1/users/me/messages/mail-2":
			id := strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me/messages/")
			headers := []any{}
			for _, v := range [][2]string{{"Subject", "预算核对"}, {"From", "王芳 <wang@example.com>"}, {"To", "李明 <li@example.com>"}, {"Reply-To", "budget@example.com"}, {"Message-ID", "<budget-" + id + "@example.com>"}, {"Date", "Fri, 11 Sep 2026 10:00:00 +0800"}} {
				headers = append(headers, map[string]string{"name": v[0], "value": v[1]})
			}
			payload := map[string]any{"mimeType": "text/plain", "headers": headers}
			if q.Get("format") == "full" && !f.partial.Load() {
				payload["body"] = map[string]any{"size": len(mailFixtureBody), "data": base64.RawURLEncoding.EncodeToString([]byte(mailFixtureBody))}
			}
			json.NewEncoder(w).Encode(map[string]any{"id": id, "threadId": "thread-budget", "internalDate": "1789092000000", "labelIds": []string{"INBOX", "UNREAD"}, "payload": payload})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(vendor.Close)
	f.providerTransport = accountProviderHTTPTransport{vendor, "gmail.googleapis.com", "/gmail/v1/"}
	model := httptest.NewServer(http.HandlerFunc(f.model))
	t.Cleanup(model.Close)
	f.options.MailTools = true
	f.options.Agent = agentmodule.Options{ConversationURL: model.URL, ConversationModel: "mail-protocol-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond, MaxSteps: 16, MaxToolCalls: 16}}
	f.open()
	return f
}

func (f *mailProductFixture) connect(b *browser, basic bool) integration.ConnectionAccount {
	f.t.Helper()
	scope, applicationKey := mailReadScope, "mail-google"
	if basic {
		scope, applicationKey = mailBasicScope, "mail-google-basic"
	}
	b.call("PUT", "/integration/oauth-applications/"+applicationKey, accountJSON(integration.OAuthApplicationInput{ConnectorKey: "google_workspace", ProviderKey: "google", Name: "Google Mail", ClientID: "fixture-client", ClientSecret: "fixture-client-secret", RedirectURI: "http://127.0.0.1:8091/oauth/callback", Scopes: []string{mailReadScope, mailBasicScope}, Enabled: true}), 200)
	s := accountDecode[integration.OAuthAuthorizationSession](f.t, b.call("POST", "/integration/oauth-authorizations", accountJSON(integration.OAuthAuthorizationInput{ApplicationKey: applicationKey, Scope: integration.ConnectionAccountScopePersonal, Name: "个人邮件", Scopes: []string{scope}}), 200))
	u, _ := url.Parse(f.callback(s.AuthorizationURL, "connect"))
	done := accountDecode[integration.OAuthAuthorizationSession](f.t, b.call("POST", "/integration/oauth-authorizations/callback", accountJSON(integration.OAuthAuthorizationCallback{State: u.Query().Get("state"), Code: u.Query().Get("code")}), 200))
	if done.Account == nil {
		f.t.Fatal("mail account missing")
	}
	return *done.Account
}

func (f *mailProductFixture) grantReader(admin *browser, user string) {
	f.t.Helper()
	mutateTestRolePermissions(f.t, f.host, admin, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		keys := []string{integration.ActionIntegrationConnectionAccountsList, integration.ActionIntegrationConnectionAccountsRead}
		for _, d := range append(toolmodule.MailDefinitions(), sdk.ArtifactConversationTools()...) {
			keys = append(keys, d.ActionKey)
		}
		for _, r := range tools.ToolSettingsRoutes() {
			keys = append(keys, r.Action.Permission.Key)
		}
		known := map[string]bool{}
		for _, p := range prior {
			known[p.PermissionKey] = true
		}
		for _, key := range keys {
			if !known[key] {
				prior = append(prior, identity.ProjectRolePermission{PermissionKey: key, DataScope: identity.DataScopeOwner})
			}
		}
		return prior
	}, user)
}

// The model fixture reads actual HTTP tool feedback before choosing the next
// call and composing the answer. It is not a language-model quality evaluation.
func (f *mailProductFixture) model(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tools    []struct{ Function struct{ Name string } }
		Messages []struct {
			Role, Content string
			CallID        string `json:"tool_call_id"`
		}
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Messages) == 0 {
		http.Error(w, "bad model request", 400)
		return
	}
	f.modelCalls.Add(1)
	w.Header().Set("Content-Type", "text/event-stream")
	write := func(delta any, finish string) {
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
		w.(http.Flusher).Flush()
	}
	answer := func(s string) { write(map[string]any{"content": s}, "stop") }
	defer fmt.Fprint(w, "data: [DONE]\n\n")
	available := map[string]bool{}
	for _, v := range in.Tools {
		available[v.Function.Name] = true
	}
	call := func(id, key string, args any) {
		if !available[key] {
			answer("当前没有可用的邮件读取权限或连接。")
			return
		}
		write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": key, "arguments": accountJSON(args)}}}}, "tool_calls")
	}
	data := map[string]json.RawMessage{}
	for _, m := range in.Messages {
		if m.Role == "tool" {
			var result sdk.ConversationToolResult
			if json.Unmarshal([]byte(m.Content), &result) != nil || result.Status != "completed" {
				answer("邮件处理未完成，未宣称保存或发送成功。")
				return
			}
			if strings.HasPrefix(m.CallID, "mail-") {
				var env struct{ Data json.RawMessage }
				json.Unmarshal(result.Content, &env)
				data[m.CallID] = env.Data
			} else {
				data[m.CallID] = result.Content
			}
		}
	}
	last := in.Messages[len(in.Messages)-1]
	if last.Role == "user" {
		call("mail-accounts", "mail_accounts", map[string]any{"operation": mail.ListOperationKey})
		return
	}
	var accounts struct{ Items []struct{ Key string } }
	json.Unmarshal(data["mail-accounts"], &accounts)
	if len(accounts.Items) != 1 {
		answer("没有唯一的可用邮件账号，需要明确选择。")
		return
	}
	account := accounts.Items[0].Key
	var first, second mail.MessagesPage
	json.Unmarshal(data["mail-search"], &first)
	json.Unmarshal(data["mail-page"], &second)
	var message mail.Message
	json.Unmarshal(data["mail-read"], &message)
	var artifact sdk.ConversationArtifactVersion
	for _, key := range []string{"draft-create", "draft-read", "draft-edit"} {
		if raw, ok := data[key]; ok {
			json.Unmarshal(raw, &artifact)
		}
	}
	switch last.CallID {
	case "mail-accounts":
		call("mail-list", "mail_list", map[string]any{"account_key": account, "limit": 2})
	case "mail-list":
		if !available["mail_read"] || !available["mail_search"] {
			answer("当前仅有邮件头权限，无法总结正文或创建回复草稿。")
			return
		}
		call("mail-search", "mail_search", map[string]any{"account_key": account, "query": "subject:预算", "query_syntax": "gmail", "limit": 1})
	case "mail-search":
		if first.Complete || first.NextCursor == "" || len(first.Items) != 1 {
			answer("搜索来源未完成。")
			return
		}
		call("mail-page", "mail_search", map[string]any{"account_key": account, "query": "subject:预算", "query_syntax": "gmail", "limit": 1, "cursor": first.NextCursor})
	case "mail-page":
		if !second.Complete || len(second.Items) != 1 {
			answer("搜索来源未完成。")
			return
		}
		call("mail-read", "mail_read", map[string]any{"account_key": account, "message_id": first.Items[0].ID})
	case "mail-read":
		if !message.Body.Complete {
			answer("邮件正文不完整，无法生成完整摘要或回复草稿。未发送。")
			return
		}
		text := fmt.Sprintf("# 预算回复草稿（未发送）\n\n收件人：%s\n\n已收到预算核对请求，负责人和承诺仍待确认。\n\n原邮件内容：%s\n\n来源账号：%s\n邮件 ID：%s\nthread_id：%s\ninternet_message_id：%s\n来源时间：%s\n", message.Summary.ReplyTo[0].Address, message.Body.Text, account, message.Summary.ID, message.Summary.ThreadID, message.Summary.InternetMessageID, message.Summary.ReceivedAt)
		call("draft-create", "artifact_create", map[string]any{"title": "预算回复草稿（未发送）", "content": sdk.ConversationArtifactContent{Kind: "markdown", Markdown: text}})
	case "draft-create":
		call("draft-read", "artifact_read", map[string]any{"id": artifact.Artifact.ID, "version": artifact.Artifact.Version})
	case "draft-read":
		call("draft-edit", "artifact_edit", map[string]any{"id": artifact.Artifact.ID, "expected_version": artifact.Artifact.Version, "patch": map[string]any{"text": []any{map[string]string{"find": "负责人和承诺仍待确认。", "replace": "负责人和承诺仍待确认；核对后再回复。"}}}})
	case "draft-edit":
		call("draft-export", "artifact_export", map[string]any{"id": artifact.Artifact.ID, "version": artifact.Artifact.Version, "format": "markdown"})
	case "draft-export":
		var receipt struct {
			Export sdk.ConversationArtifactExport `json:"export"`
		}
		json.Unmarshal(data["draft-export"], &receipt)
		if receipt.Export.ID == "" || artifact.Artifact.ID == "" {
			answer("草稿回执缺失。")
			return
		}
		answer(fmt.Sprintf("已搜索 %d 封邮件。摘要：%s\n\n待处理事项：预算核对；来源请求李明于 2026-09-14 前处理，负责人和承诺待确认。\n\n已保存本地回复草稿（未发送），版本 %d，并导出 Markdown。来源邮件 %s / %s / %s，草稿 ID：%s。", len(first.Items)+len(second.Items), message.Body.Text, artifact.Artifact.Version, account, message.Summary.ID, message.Summary.InternetMessageID, artifact.Artifact.ID))
	default:
		answer("未完成邮件处理。")
	}
}
