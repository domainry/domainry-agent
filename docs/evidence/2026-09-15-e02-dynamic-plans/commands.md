# E02 验收命令

日期：2026-09-15。以下正式验收命令退出码均为 0。大型 Web 完整包、真实 Chrome 根场景的结构化报告和对应源码快照一并保存在本目录。

## SDK

工作目录：`/Users/tiger/Projects/domainry-agent-sdk`

```bash
go test ./...
git diff --check
```

## Agent 全产品回归

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
go test ./internal/... ./module ./remote ./server ./integration ./cmd/... ./definition
go test ./internal/assembly/web
```

本轮完整 Web 包结果为 `ok github.com/domainry/domainry-agent/internal/assembly/web 477.288s`。其余产品包在同一源码版本通过；最终新增权限修复后再次完整执行 Web 包。

## 计划并发、来源和跨主体回归

```bash
go test -race ./internal/application \
  -run '^(TestPlanSourceReadsExactHistoricalVersionAndRejectsForgedReceipt|TestConversationPlanToolIsOnlyVisibleToBackgroundTaskRuns)$' -count=1

go test -race ./internal/infrastructure/persistence/database/agent \
  -run '^TestConversationPlan' -count=1

go test ./internal/assembly/web \
  -run '^TestSourceReleaseHTTPAgentDispatchMessageAndAutomaticDeliveryAcrossSubjects$' -count=1

git diff --check
rg -n 'DEBUG' internal/application internal/assembly/web internal/transport
```

最后一条 `rg` 无匹配。大型跨主体 E2E 在 race 插桩下受测试任务预算和各控制工具观察窗影响，因此正式 E2E 使用正常速度；应用与持久化边界分别执行 race。

## 前端

工作目录：`/Users/tiger/Projects/domainry-agent/frontend`

```bash
npm test -- --runInBand
npm run build
```

## 真实 Identity／HTTP／SQLite／Chrome

```bash
AGENT_TOOL_UI_ACCEPTANCE=1 \
  go test ./internal/assembly/web \
  -run '^TestBackgroundTaskPersistsVersionedPlanThroughIdentityHTTPAndSQLite$' -count=1 -v

AGENT_PLAYWRIGHT_MODULE=/Applications/ChatGPT.app/Contents/Resources/cua_node/lib/node_modules/playwright \
AGENT_UI_ORIGIN=http://127.0.0.1:8092 \
AGENT_UI_TEST_OUTPUT=<output-directory> \
  node frontend/tests/task-plan.browser.mjs
```

另以同样的真实宿主运行 `frontend/tests/collaboration.browser.mjs` 和 `frontend/tests/task-control.browser.mjs`。报告记录当前／历史计划、准确证据跳转、要求变化后的恢复计划、桌面和窄屏布局以及页面错误。
