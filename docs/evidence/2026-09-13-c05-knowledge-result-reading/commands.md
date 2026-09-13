# C05 Knowledge 原回执阅读：命令与证据

在 `/Users/tiger/Projects/domainry-agent` 使用既有 `go.work`，没有创建分支／worktree、提交或部署。

```sh
go test ./... ../domainry-agent-sdk/... ../domainry-knowledge/...
go test ./internal/application ../domainry-agent-sdk/... ../domainry-knowledge/...
go test ./internal/assembly/web -run '^TestPeerKnowledgeDeliveryReadsWithoutSearchReadOrExtractExecution$' -count=1 -v
go test -race ./internal/application ../domainry-knowledge/internal/application/knowledge ../domainry-knowledge/internal/infrastructure/provider ./internal/assembly/web -run 'TestDeliveryKnowledge|TestManagedKnowledgeResult|TestKnowledgeResultReader|TestKnowledgeReceiptsRecheck|TestPeerKnowledgeDelivery' -count=1
```

`full-regression.log` 全部通过（Web 252.075s，包含 Module／SaaS 与架构契约）；`race.log` 全部通过（真实 Web 场景 150.911s）。`http.log` 为真实四种工具交付与权限变更、来源改变和重启检查（7.268s）。`application.log`、`owner-regression.log` 保留应用及 SDK／Knowledge 检查。

真实 Chrome 使用既有生产前端构建，本阶段没有修改生产前端：

```sh
AGENT_KNOWLEDGE_DELIVERY_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/tmp/c05-knowledge-delivery-browser \
go test ./internal/assembly/web -run '^TestPeerKnowledgeDeliveryReadsWithoutSearchReadOrExtractExecution$' -count=1 -v
```

`browser.log` 通过（16.414s）；`knowledge-delivery-report.json` 记录四类回执、原抽取值和证据、历史、权限撤回与恢复、刷新、关闭清理及窄屏，原始执行结果请求为 0。桌面与 390px 截图已检查。

Agent／SDK／Knowledge 的 `git diff --check` 通过。`source-sha256.json` 保存三个仓库 HEAD 及本阶段 19 个代码／测试文件摘要。本阶段没有新增数据库迁移或路由／Action。

该来源阅读阶段完成；C05 仍未整项验收，整体 4／25。明确不支持独立读取的自定义来源仍要求原执行授权。
