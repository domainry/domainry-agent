# C04 分歧处理：命令与证据范围

Go 从 `/Users/tiger/Projects/domainry-agent` 执行，SDK 通过现有 `go.work` 读取相邻源码；前端使用 Node 22.22.0。

```sh
go test ./internal/execution ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/transport/http/module ./module ./remote ../domainry-agent-sdk/... -count=1
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/assembly/web -run 'Test(Disagreement|PeerDisagreement|ConversationStepSources|PeerCollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery)' -count=1
npm test
npm run build
npm run check

AGENT_PEER_BROWSER=1 \
AGENT_NODE_BINARY=/Users/tiger/.nvm/versions/node/v22.22.0/bin/node \
AGENT_PLAYWRIGHT_MODULE=/Users/tiger/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright \
AGENT_UI_TEST_OUTPUT=/tmp/peer-c04-disagreements-browser-final \
go test ./internal/assembly/web -run '^TestPeer(CollaborationHTTPExecutesIsolatedAgentAndAcceptsDelivery|OutcomeInspectionOnlyReadsOriginalHTTPReceipt|TransferOnlyContinuesRemainingHTTPWork|TransferAgentToolContinuesRemainingHTTPWork)$' -count=1 -v
```

`core-sdk-http-final.log` 为核心、迁移、SDK、Module、Remote 与 HTTP 契约全套结果；`frontend-tests.log` 为 69 项通过；`frontend-build.log` 是当前前端产品源码的生产构建，保留既有 Vite 大 chunk 提示。构建后只增加前端状态测试，最终 `frontend-types.log` 再次检查全部 TypeScript。

`browser-http-final.log` 包含真实 Chrome 与其后的宿主重启、当前来源撤回检查，以及原回执核查／用户转交／Agent 转交三个既有场景。`report.json` 和两张分歧截图来自该次 Chrome，桌面及窄屏均已人工查看。`race-final.log` 的应用、持久化和完整协作 HTTP 场景全部通过，HTTP 约 177 秒。

`source-lifecycle-tests.log` 保留针对分歧生命周期和真实 SQLite 关闭重开后的步骤来源测试；随后增加的摘要容量与当前验收阻塞投影测试也包含在完整核心和 race 回归中。

保留以下失败证据，不将失败运行冒充验收通过：

- `before-fixture-schema-fix.log`：测试模型直接序列化 HTTP 更新结构，带入工具不允许的 `client_id`；生产 Schema 正确拒绝。测试改用实际工具字段，未放宽服务端校验。
- `before-fixture-result-read-fix.log`：测试模型把压缩结果预览当成完整交付详情，下一步找不到刚保存的分歧。夹具补充 `tool_result_read`，只从实际消息正文获取引用并按字节拼接完整分页；不使用模型输入内部引用字段、直接数据库访问或放大上下文限制。
- `before-fixture-revision-refresh.log`：race 下后台任务恰好在详情投影期间完成，测试随后沿用旧修订号提交新交付，服务返回 409。测试在新的用户写入前重新读取当前快照；生产并发保护保留。

`source-sha256.json` 包含 58 个相关产品、契约与测试源码摘要。最终 HTTP／Chrome 后仅补充文档和证据，未改变产品行为。本批没有提交、推送或部署。
