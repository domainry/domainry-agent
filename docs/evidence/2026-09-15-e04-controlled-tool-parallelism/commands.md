# E04 验收命令

日期：2026-09-15。以下正式验收命令退出码均为 0。完整 Web、Integration、针对性 race、真实 Chrome、结构化报告和相关源码快照保存在本目录。

## Tools SDK

工作目录：`/Users/tiger/Projects/domainry-tools-sdk`

```bash
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./... -count=1
git diff --check
```

## Tools

工作目录：`/Users/tiger/Projects/domainry-tools`

```bash
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./... -count=1
git diff --check
```

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
```

完整 Web 包结果为 `ok github.com/domainry/domainry-agent/internal/assembly/web 468.714s`，Integration 为 `94.918s`。

## 受控并行与持久顺序 race

```bash
go test -race ./internal/assembly/web \
  -run '^TestIndependentReadToolsRunConcurrentlyInOrderAndCancelTogether$' -count=1

go test -race ./internal/infrastructure/persistence/database/agent \
  -run '^(TestConversationExecutionAllowsOnlyExplicitParallelReadsToBeginOutOfOrder|TestConversationExecutionProjectionClearsParallelModeOnRetry)$' -count=1

git diff --check
```

## 前端

工作目录：`/Users/tiger/Projects/domainry-agent/frontend`

```bash
npm test
npm run build
```

前端共 88 项测试通过；生产构建通过。

## 真实 Identity／HTTP／SQLite／Chrome

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
AGENT_E04_BROWSER=1 \
AGENT_NODE_BINARY=<bundled-node> \
AGENT_PLAYWRIGHT_MODULE=/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/Users/tiger/Projects/domainry-agent/docs/evidence/2026-09-15-e04-controlled-tool-parallelism/browser \
  go test ./internal/assembly/web \
  -run '^TestControlledToolParallelismBuiltBrowser$' -count=1 -v
```

浏览器报告覆盖两个明确声明的独立读取、执行前独立授权、实际峰值 2、调用结果原顺序投影、运行级并行审计和 390 px 页面。打开结果详情产生两次额外的当前授权读取，因此宿主总授权次数为 4，执行指标仍准确记录本轮两个工具调用及两次授权。登录后的 JavaScript 错误、控制台错误和警告均为 0；登录前 `/app/session` 与 `/auth/refresh` 的 401 是匿名会话探测。
