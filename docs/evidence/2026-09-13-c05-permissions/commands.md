# C05 权限阶段：命令与证据

Go 命令从 `/Users/tiger/Projects/domainry-agent` 执行，通过现有 `go.work` 读取相邻 Agent SDK；没有新分支、worktree、提交或部署。

```sh
go test ./... ../domainry-agent-sdk/... -count=1
go test -race ./internal/application ./internal/assembly/web ./integration -run 'Test(Collaboration|PeerCollaborationPermissions|ExecutionReferencesSurviveTurnsSummariesAndServiceRestart)' -count=1
```

前端从 `frontend` 执行：

```sh
npm test
npm run build
```

构建后，在临时 SQLite、真实 Identity 和隔离 HTTP 效果端点上运行：

```sh
AGENT_PEER_BROWSER=1 \
AGENT_PEER_PERMISSIONS_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/tmp/c05-browser-verified \
go test ./internal/assembly/web -run '^TestPeer(CollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery|CollaborationPermissionsRecheckCurrentRoleAcrossEntrypoints|OutcomeInspectionOnlyReadsOriginalHTTPReceipt|TransferOnlyContinuesRemainingHTTPWork|TransferAgentToolContinuesRemainingHTTPWork)$' -count=1 -v
```

- `full-regression.log`：Agent 全包、架构门禁、集成测试、持久化、全部 Web 场景、Module、Remote、Server 及 Agent SDK 全部通过。Web 全包约 214 秒，集成包约 93 秒。
- `frontend-tests.log`：70 项前端测试通过；`frontend-build.log` 包含 TypeScript 检查与生产构建，保留既有 Vite 大 chunk 提示。
- `browser-http.log`：权限矩阵、真实 Chrome 权限撤回和既有交付／分歧／回执核查／转交五个场景通过，约 81 秒。`permissions-report.json` 与两张权限截图来自该次 Chrome。
- `race.log`：权限、消息、交付来源用途、运行中撤权以及原始执行引用恢复场景全部通过，Web 约 80 秒。
- `context-recovery.log`：原 20KB 限额下的历史执行引用恢复、结果分页、撤权、工具结果压缩及 UTF-8 分页回归通过。
- `source-sha256.json`：55 个当前相关源码摘要，收尾时逐一复核一致。`acceptance.json` 明确本批只是 C05 阶段交付，完整条目仍为 4／25。

失败证据保留：`before-final-regression-fixes.log` 包含旧清单数量断言和上下文恢复失败；`before-context-reserve-fix.log` 记录失败前的请求大小，恢复链在第四步已经达到 19,416 字节；`before-independent-fixture-deadlines.log` 记录 race 夹具把接收方执行与来源通知串用同一期限造成的超时。后者仅为独立阶段设置有界等待，没有修改生产运行超时。

完整范围和下一阶段边界见 [C05 阶段记录](../../testing-2026-09-13-c05-permissions.md)。未实现的专业工具结果策略、跨用户主体协作及明确资料共享保持待办。
