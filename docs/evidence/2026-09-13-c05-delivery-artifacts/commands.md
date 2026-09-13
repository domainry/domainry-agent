# C05 完整成果阅读：命令与证据

从 `/Users/tiger/Projects/domainry-agent` 使用既有 `go.work`，所有命令已完成；没有创建分支／worktree、提交或部署。

```sh
go test ./... ../domainry-agent-sdk/... ../domainry-knowledge/...
go test -race ../domainry-knowledge/internal/application/knowledge ./internal/application ./remote -run 'Test(ExistingArtifactExportRead|DeliveryArtifacts|ReleasedResult|SaaSDeliveryReader)' -count=1
go test -race ./internal/assembly/web -run '^TestPeerArtifactDeliveryReadsKnowledgeVersionsWithoutMutationRights$' -count=1
go test ../domainry-agent-sdk/... ./internal/transport/http/module ./internal/capability ./server ./remote
```

`full-regression.log` 覆盖 Agent、Agent SDK、Knowledge 全包；`unit-race.log`、`web-race.log` 分别覆盖 owner／应用／SaaS 和真实 HTTP 链路。全包检查启动后补齐下载 OpenAPI 的二进制媒体声明，由 `contract-final.log` 针对最终 SDK／Module／capability／Server／Remote 再次验证，未改动业务执行行为。

前端在 `frontend` 执行 `npm run check`、`npm test`、`npm run build`。`frontend-tests.log` 为 70 项测试，`frontend-build.log` 包含类型检查、构建及既有大 chunk 提示。

```sh
AGENT_ARTIFACT_DELIVERY_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/tmp/c05-full-artifact-browser \
go test ./internal/assembly/web -run '^TestPeerArtifactDeliveryReadsKnowledgeVersionsWithoutMutationRights$' -count=1 -v

AGENT_PEER_PERMISSIONS_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/tmp/c05-full-artifact-peer-permissions \
go test ./internal/assembly/web -run '^TestPeerCollaborationPermissionsRecheckCurrentRoleAcrossEntrypoints$' -count=1 -v
```

`browser.log`、`delivery-result-report.json` 与两张截图来自真实 Chrome；`peer-permissions.log` 为既有权限页面回归。原“禁止原回执按钮”断言已改为“禁止执行过程按钮”，配合新场景验证交付阅读权确实可以读取已释放回执，而原始执行仍不可读。

`owner.log`、`membership.log`、`http.log` 和 `saas.log` 记录精确来源边界。`before-*.log` 保留超出模型参数预算的初始夹具，以及补充终态快速失败诊断后的结果。夹具改为预算内的正文和明确 256 字节正文页，仍验证完整正文超出已保存页面；生产参数上限没有调整。HTTP 哈希回归一般使用 8192 字节页，仅一份元数据回执专门使用 256 字节页，避免对同一授权链重复大量读取。

`source-sha256.json` 保存三个仓库 HEAD 和 38 个相关源码摘要，收尾已逐一复核。`acceptance.json` 记录本阶段完成范围，C05 未标记完整验收，清单仍为 4／25。
