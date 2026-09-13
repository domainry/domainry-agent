# C05 成果阅读与交付原回执：命令和证据

在 `/Users/tiger/Projects/domainry-agent` 使用既有 `go.work`，不创建分支／worktree、不提交或部署。

```sh
go test ./... ../domainry-agent-sdk/... ../domainry-knowledge/...
go test -race ./internal/application ./internal/assembly/web ./remote -run 'Test(DeliveryArtifacts|ReleasedResult|DeliveryToolRead|PeerMessagesAndDeliveries|PeerArtifactDelivery|SaaSDeliveryReader)' -count=1
go test ./internal/application -run 'TestDeliveryArtifacts|TestReleasedResult' -count=1
```

前端在 `frontend` 执行 `npm run check`、`npm test`、`npm run build`，随后运行真实浏览器：

```sh
AGENT_ARTIFACT_DELIVERY_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/tmp/c05-artifact-browser \
go test ./internal/assembly/web -run '^TestPeerArtifactDeliveryReadsKnowledgeVersionsWithoutMutationRights$' -count=1 -v
```

- `full-regression.log`：Agent、Agent SDK、Knowledge 全包最终回归。
- `race.log`：应用层、真实 Web 交付链路及 SaaS 转发的 race 检查。
- `owner-final.log`：六种成果工具、独立枚举权限、精确回执及逐页撤权的最终检查。
- `frontend-tests.log`、`frontend-build.log`：70 项现有前端测试及生产构建；保留既有大 chunk 构建提示。
- `browser.log`、`delivery-result-report.json`、两张截图：真实 Chrome 当前／历史回执阅读、撤权、恢复、关闭、刷新和窄屏，无页面异常或普通执行结果请求。
- `transport.log`：SaaS／Remote 和公开 capability 清单修正后的检查；Module 与 SDK 同时由全包回归覆盖。
- `source-sha256.json`：本批相关源码与仓库 HEAD，收尾复核。

失败记录保留于 `before-*.log`。新增只读路由后，原有 HTTP／SaaS／capability 数量断言分别需要更新；SaaS 夹具首次遗漏 runtime 身份，被现有边界正确拒绝。浏览器夹具首次未展开原始 JSON、误把来源撤权等同于隐藏整个权限区域、窄屏刷新后查找桌面菜单，均按实际页面入口修正。成果单测首次错误要求未出现在导出回执中的标题也影响该回执，改为核对真实导出文件名；没有放宽生产授权。

完整边界及未完成条目见[验收记录](../../testing-2026-09-13-c05-artifact-and-delivery-reading.md)。
