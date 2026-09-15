# E01 验收命令

日期：2026-09-15。以下最终命令在所列目录运行，退出码均为 0。日志、浏览器报告、截图与对应源码快照保存在本目录。

## SDK

工作目录：`/Users/tiger/Projects/domainry-agent-sdk`

```bash
go test ./...
git diff --check
```

## Agent 全产品回归

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
go test ./internal/... ./module ./remote ./server ./integration
```

## 核心并发与权限回归

```bash
go test -race ./internal/application \
  -run '^(TestTaskDeliveryReceiptsSeparateControlsFromCurrentExecutionAndSources|TestPrepareConversationTaskKeepsExplicitAndInferredAgreementFields)$' -count=1

go test -race ./internal/infrastructure/persistence/database/agent \
  -run '^TestConversationTaskAgreementPersistsCurrentGoalAndStartsANewRun$' -count=1

go test ./internal/assembly/web \
  -run '^(TestTaskStartThroughIdentityHTTPWorkerRecoveryAndSQLite|TestPeerCollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery|TestPeerTaskDeliveryReadsOriginalReceiptsAfterControlRevocation)$' -count=1

go test ./remote ./server ./module ./internal/capability -count=1
git diff --check
```

## 前端

工作目录：`/Users/tiger/Projects/domainry-agent/frontend`

```bash
npm test -- --runInBand
npm run build
```

## 真实 Identity／HTTP／SQLite／Chrome

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
AGENT_TOOL_UI_ACCEPTANCE=1 \
  go test ./internal/assembly/web \
  -run '^TestTaskStartThroughIdentityHTTPWorkerRecoveryAndSQLite$' -count=1 -v

AGENT_PLAYWRIGHT_MODULE=/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules/playwright \
AGENT_UI_ORIGIN=http://127.0.0.1:8092 \
AGENT_UI_TEST_OUTPUT=<output-directory> \
  node frontend/tests/task-control.browser.mjs
```

Go 根场景建立真实 Identity 主体、HTTP 适配、SQLite 存储和后台 worker，并等待浏览器验收完成。浏览器从页面更新完整任务约定、继续失败任务、刷新读取完成状态，再取消等待任务并检查 390 px 页面。
