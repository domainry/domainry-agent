# C07 验收命令

日期：2026-09-14。以下命令在所列目录运行，退出码均为 0。日志、真实浏览器报告、截图与对应源码快照保存在本目录。

## SDK

工作目录：`/Users/tiger/Projects/domainry-agent-sdk`

```bash
go test ./...
git diff --check
```

## Agent 存储与并发边界

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
go test -race ./internal/infrastructure/persistence/database/agent -run 'TestConversationWork|TestConversationAgentMessageRate|TestPeerReplyClosesOnlyEquivalent|TestPeerSameAgentRecovery' -count=1
```

## Agent 产品代码包

```bash
go test $(go list -e -f '{{if .GoFiles}}{{.ImportPath}}{{end}}' ./... 2>/dev/null | rg -v '^$|/docs/evidence/')
git diff --check
```

仓库根的 `go test ./...` 会继续扫描 `docs/evidence` 中为旧阶段封存的局部 Go 源码快照；这些目录不是产品包，部分快照按设计不能独立编译。本次没有修改既有证据目录，而是排除证据归档后运行全部实际产品包。

## 前端

工作目录：`/Users/tiger/Projects/domainry-agent/frontend`

```bash
npm test -- --runInBand
npm run build
```

## 真实 Identity／HTTP／SQLite／Chrome 恢复

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
AGENT_C07_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=<output-directory> \
go test ./internal/assembly/web -run '^TestPeerSameAgentRecoveryHTTPAndBrowser$' -count=1 -v
```

该根场景先让旧任务记录 `agent_changed`，通过真实 Agent HTTP 更新接口发布新配置，再由页面提交同一 Agent 的恢复操作。后端断言新会话、新任务、新接单记录及共享工作预算；浏览器断言桌面预算、恢复表单和 390 px 恢复后详情。
