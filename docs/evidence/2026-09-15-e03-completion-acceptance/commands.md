# E03 验收命令

日期：2026-09-15。以下正式验收命令退出码均为 0。完整 Web 包、Identity／HTTP／SQLite 对照、真实 Chrome 报告和相关源码快照保存在本目录。

## SDK

工作目录：`/Users/tiger/Projects/domainry-agent-sdk`

```bash
go test ./... -count=1
git diff --check
```

## Agent 产品回归

工作目录：`/Users/tiger/Projects/domainry-agent`

```bash
go test ./internal/execution ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/transport/http/module ./module ./remote ./server -count=1
go test ./integration -count=1
go test ./internal/assembly/web -count=1
```

完整 Web 包结果为 `ok github.com/domainry/domainry-agent/internal/assembly/web 471.280s`，Integration 为 `97.967s`。恢复控制的针对性 Identity／HTTP／SQLite 用例为 `10.661s`。

## 完成核对与持久化 race

```bash
go test -race ./internal/execution \
  -run '^(TestCompletionChecksUseDataAndActualReceiptSemantics|TestCompletionSeparatesRecipientClaimsFromReviewAndUnresolvedWork|TestCompletionRejectsInvalidRulesAndDoesNotHideEvidenceErrors)$' -count=1

go test -race ./internal/application \
  -run '^(TestAssessedTaskRequiresSubmissionAndCanResumeWithImmutableCompletionHistory|TestProgramCompletionCannotBeOverriddenByAgentOrUserReview|TestConversationTaskCompletionToolVisibilityAndScope)$' -count=1

go test -race ./internal/infrastructure/persistence/database/agent \
  -run '^TestConversationTaskCompletion' -count=1

git diff --check
```

## 前端

工作目录：`/Users/tiger/Projects/domainry-agent/frontend`

```bash
npm test
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
  node frontend/tests/task-completion.browser.mjs
```

浏览器报告覆盖 Agent 未确定提交、用户绑定当前 digest/revision 的逐项复核、两版不可变历史和 390 px 页面。登录后的 JavaScript 错误、控制台错误和警告均为 0。
