# E06 验收命令

日期：2026-09-15。以下正式验收命令退出码均为 0。完整 Web、Integration、上下文竞态、真实 Chrome、结构化报告和相关源码快照保存在本目录。

## Agent SDK

工作目录：`/Users/tiger/Projects/domainry-agent-sdk`

```bash
GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test ./...
git diff --check
```

## Agent 产品回归

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
go test ./internal/execution ./internal/application \
  ./internal/infrastructure/persistence/database/agent \
  ./internal/infrastructure/provider ./internal/transport/http/module \
  ./module ./remote ./server -count=1
go test ./integration -count=1
go test ./internal/assembly/web -count=1
git diff --check
```

完整 Web 包结果为 `ok github.com/domainry/domainry-agent/internal/assembly/web 558.744s`，Integration 为 `99.274s`。

## 上下文竞态与前端

```bash
go test -race ./internal/application \
  ./internal/infrastructure/persistence/database/agent \
  -run 'Context' -count=1

cd frontend
npm test -- --run
npm run build
```

前端共 90 项测试通过；TypeScript 与 Vite 生产构建通过。

## 真实 Identity／HTTP／SQLite／Chrome

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
AGENT_E06_BROWSER=1 \
AGENT_NODE_BINARY=<node-22> \
AGENT_PLAYWRIGHT_MODULE=<playwright-module> \
AGENT_UI_TEST_OUTPUT=/Users/tiger/Projects/domainry-agent/docs/evidence/2026-09-15-e06-long-context-management/browser \
  go test ./internal/assembly/web \
  -run '^TestLongContextManagementBuiltBrowser$' -count=1 -v
```

浏览器使用编译后的前端和真实 Identity／HTTP／SQLite 服务。首个运行执行三次模型请求、两次工具读取，按实际提供者协议记录 16,169／20,480 字节峰值，压缩 2 个结果和 1 个执行区间，并累计 180 个缓存命中 token 与 100 个缓存创建 token。逐步业务来源版本为 `record-v1`、`record-v2`、`record-v3`。撤销旧来源后，第二个运行只读取 `record-v4`，旧回复正文没有进入模型或公开运行 JSON。桌面和 390 px 页面通过；登录后的 JavaScript 错误、控制台错误和警告均为 0。
