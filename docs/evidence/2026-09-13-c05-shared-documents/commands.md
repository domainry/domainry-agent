# C05 明确资料共享：命令与证据

从 `/Users/tiger/Projects/domainry-agent` 使用既有 `go.work`。下列检查均已完成；未创建分支／worktree、提交或部署。`source-sha256.json` 保存三个仓库的 HEAD 及本阶段 29 个代码／测试文件摘要。

```sh
go test ./... ../domainry-agent-sdk/...
go test ./internal/application ./internal/infrastructure/persistence/database/agent ../domainry-agent-sdk/...
go test ./internal/assembly/web -run '^TestPeerSharedDocumentsUseExplicitKnowledgeCopyAndCurrentReadPermission$' -count=1 -v
go test ./remote ../domainry-agent-sdk -run 'TestSaaSSharedDocuments|TestSharedDocumentReferences' -count=1
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/assembly/web -run 'TestSharedDocuments|TestPeerDocumentReferences|TestPeerSharedDocuments' -count=1
go test -race ./internal/assembly/web -run '^TestPeerSharedDocumentsUseExplicitKnowledgeCopyAndCurrentReadPermission$' -count=1
go test -race ./remote -run '^TestSaaSSharedDocumentsPreserveActorAndExactReference$' -count=1
```

`full-regression.log` 覆盖 Agent／SDK 全包（Web 292.850s）；其后只扩展了 HTTP 模型夹具，让接收方通过真实 `agent_message` 发回同一文件引用。扩展后的夹具由 `http-agent-send.log`、`http-agent-send-race.log`（44.676s）及最终 Chrome 再次通过；最终生产 Go 代码仍是全包检查对应代码。

前端在 `frontend` 执行 `npm test`（70 pass）和 `npm run build`（含 TypeScript 检查；保留既有大 chunk 提示）。最终 Chrome 覆盖实际构建页面、Identity 角色变更、Knowledge 资料读取／下载及消息投递：

```sh
AGENT_SHARED_DOCUMENT_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/tmp/c05-shared-documents-agent-browser \
go test ./internal/assembly/web -run '^TestPeerSharedDocumentsUseExplicitKnowledgeCopyAndCurrentReadPermission$' -count=1 -v
```

`browser.log` 为最终通过记录（14.510s），`shared-documents-report.json` 记录实际页面提交的准确版本引用、测试场景和零页面异常；两张截图分别是桌面与 390px 页面。`saas-race.log` 覆盖实际 SaaS 签名转发。

保留两个夹具修正前的失败记录：缺少 datasource 必需的 search mapping，及工具夹具误提交由服务端分配的 `client_id`。两者均修正夹具后通过；没有放宽生产输入契约或授权。Agent／SDK `git diff --check` 通过。

本阶段完成明确共享流程，C05 仍包含跨用户／角色主体和其余 owner 的独立交付阅读，整体仍为 4／25，见 `acceptance.json`。
