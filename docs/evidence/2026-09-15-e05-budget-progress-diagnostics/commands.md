# E05 验收命令

日期：2026-09-15。以下正式验收命令退出码均为 0。完整 Web、Integration、预算并发 race、真实 Chrome、结构化报告和相关源码快照保存在本目录。

## Agent SDK

工作目录：`/Users/tiger/Projects/domainry-agent-sdk`

```bash
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./... -count=1
git diff --check
```

## Agent 产品回归

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
go test ./internal/execution ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/transport/http/module ./module ./remote ./server -count=1
go test ./integration -count=1
go test ./internal/assembly/web -count=1
git diff --check
```

完整 Web 包结果为 `ok github.com/domainry/domainry-agent/internal/assembly/web 506.330s`，Integration 为 `93.529s`。

## 跨 Agent 预算并发 race

```bash
go test -race ./internal/infrastructure/persistence/database/agent \
  -run '^(TestConversationWorkBudgetIsInheritedReservedAndSettledAcrossAgents|TestConversationWorkBudgetSerializesConcurrentReservationsAcrossAgents|TestConversationWorkAllocationCountsCallsAndRepeatedArgumentsWithoutReset|TestPeerDiscoveryLoadFollowsDurableTaskAndRunTransitions)$' -count=1
```

同一根任务的两个 Agent 同时预留时仅一个成功；另一个得到 `work_budget_exhausted`。结算、稳定委派分配、重复参数指纹、转交保持和准确配置历史同时通过竞态检查。

## 前端

工作目录：`/Users/tiger/Projects/domainry-agent/frontend`

```bash
npm test
npm run build
```

前端共 89 项测试通过；TypeScript 与 Vite 生产构建通过。

## 真实 Identity／HTTP／SQLite／Chrome

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
AGENT_E05_BROWSER=1 \
AGENT_NODE_BINARY=<bundled-node> \
AGENT_PLAYWRIGHT_MODULE=/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/Users/tiger/Projects/domainry-agent/docs/evidence/2026-09-15-e05-budget-progress-diagnostics/browser \
  go test ./internal/assembly/web \
  -run '^TestPeerCollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery$' -count=1 -v
```

浏览器报告覆盖执行活动和业务进度分区、本项委派与整个工作累计、整个工作上限、七次模型、六次工具、700／140 token、`0.001960 CNY` 模型费用、接受前业务阻塞以及接受后的完成状态。桌面与 390 px 通过；登录后的 JavaScript 错误、控制台错误和警告均为 0。登录前匿名会话探测产生一次 404 和两次 401，单独记录为 `preLoginConsoleErrors`。
