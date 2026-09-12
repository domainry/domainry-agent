# B06 取消边界、在途请求与回执验收

日期：2026-09-10。按 TODO 顺序承接 A08。B06 完成：取消保留已完成效果，标明未知结果和未执行步骤，支持原调用受限补存迟到回执，沿已接 Connector 的 Context 停止在途请求。

## 发现与修正

原 `Cancel` 立即增加运行 fence 并取消本地 Context。随后工具即使返回外部成功回执，`FinishExecutionTool` 也会因旧 fence / 已取消 Context 拒绝写入。没有回执的写调用仍是 started；恢复时可能再次进入 Invoke。另有一个竞态：取消事务已完成但 API 尚未返回，用户恢复同一 Run 后，旧取消响应可能按 Run ID 停掉新的 attempt。

本次变化：

| 文件 | 责任 |
| --- | --- |
| SDK `persistence/conversation_execution.go` | 在内部执行账本增加最近调用 / 核查的 `LeaseOwner` / `Fence`，说明收尾回执例外；不加入浏览器和模型契约 |
| `conversation_cancellation_store.go` | 取消事务将无回执的在途写调用标为 uncertain；核对补存回执的原执行者与取消 fence |
| `conversation_execution_store.go` | 回执更新及公开事件原子提交，保留 completed / pending；补存不恢复运行、不允许覆盖完成结果。内部事务效果入口保持有效租约要求 |
| `conversation_run_store.go` | 停止后续执行并原子保存取消与工具状态；重复取消保持幂等 |
| `conversation_execution.go` | 调用前检查停止信号；工具返回后使用独立且限时 5 秒的收尾 Context 保存回执，再停止执行循环。未知写结果恢复时核查，不能盲目重放 |
| `conversation_service.go` | 本地停止信号只作用于取消事务对应的 attempt，不误停并发恢复的新 attempt |
| 服务端 / 前端执行投影、`ExecutionActivity.tsx` | 区分未执行、请求停止、待核查和实际完成；显示取消不等于撤销业务效果，并提供现有处理记录刷新入口 |

调用在持久账本 Begin 成功后属于在途范围，取消可能与外部提交同时发生；不承诺取消能回滚这个窗口内的外部动作。已存结果及其资源 ID 保留。旧账本没有新增 guard 时，不接受旧执行者绕过取消 fence 补写，仍可按恢复核查流程处理。

## 架构边界

- Agent 应用层只编排取消和结果保存，经 Agent SDK 的持久化端口访问账本；没有直接导入数据库实现、HTTP 适配器、Identity / Runtime 实现或 Connector Provider。
- 数据库实现负责事务、租约、回执和事件；不访问外部服务。新增 guard 存在原执行记录 JSON 中，不另建跨服务共享表。
- 停止 I/O 使用原有宿主端口的 Context。当前官方知识 Connector 使用 `Call(ctx)` 和 Connector SDK `Transport.RoundTripHTTP(ctx)`，Agent 宿主的 HTTP Transport 通过 `http.NewRequestWithContext` 发出请求；凭证和实际客户端留在宿主 / Transport 边界。
- 核对当前 Connector SDK `ReliabilityContract`：已有幂等、核查和补偿，没有通用远端任务取消能力。取消 Run 不调用删除业务对象、撤销订单等补偿，也不自动取消已受理的 Workflow。没有为未知的 Provider API 编造取消端点。
- 新增 `TestApplicationDoesNotImportAdaptersOrOtherServiceImplementations`，通过 Go AST 检查生产应用代码的 imports，禁止直接导入具体存储 / Transport / Assembly 和其他服务实现，允许公开 SDK 契约。此测试与现有存储内部化、模块版本依赖测试均通过；`/tmp/domainry-B06-architecture.log`。

## 存储与执行验证

[存储测试](../internal/infrastructure/persistence/database/agent/conversation_cancellation_store_test.go) 验证：

1. 三个调用分别处于已完成、在途和未开始时取消，保留第一个结果、第二个写调用标为 uncertain、第三个不能开始。
2. 取消后不能续租、生成正式回复、增加草稿或通过内部事务效果入口写入。
3. 不同执行者 / fence / 用户 / 工作区的回执被拒绝；重开存储后原调用的真实回执可补存，Run 仍是 cancelled。
4. 重复回执不增加事件，冲突结果不能覆盖，不能为未开始的调用补造结果；resume 和删除之后旧回执失效。
5. 已完成回执在恢复时读取，不重复效果；旧冻结步骤测试同时覆盖第二次调用的核查与新租约。

[执行集成测试](../integration/conversation_cancellation_integration_test.go) 验证：

- 取消发生在第二个外部效果提交、回执返回之前：确定回执保存为 completed，不确定结果保存为 uncertain，第三个调用不执行，模型没有继续，也没有正式 assistant 消息。
- 关闭并重开服务后，明确恢复原 Run：已有两个效果不重复，仅执行第三项；确定结果无需核查，不确定结果只核查一次。两种场景均为 3 次实际 Invoke、2 次模型请求，Reconcile 分别为 0 / 1。
- 通过官方知识 Connector 向真实 `httptest` HTTP 服务发送查询，取消后服务端 Request Context 被关闭；原调用收尾写入失败结果，没有下一次模型请求。
- 在取消数据库事务已提交、API 尚未返回的间隙恢复同一 Run，新 attempt 完成；旧取消响应不能将它误停。

最终专项：

```sh
go test -race -p 2 ./integration ./internal/infrastructure/persistence/database/agent -run 'TestCancellation|TestConversationCancellation|TestConversationExecutionFreezesEachStepAndNeverReplaysCompletedWrites' -count=1 -v
```

通过，集成包 2.538 秒、存储包 1.700 秒，无 race；日志 `/tmp/domainry-B06-race-final.log`。此前包含 A08 回归的专项也通过，`/tmp/domainry-B06-cancellation-race.log`。

## 网页端到端验证

使用 [cancellation.browser.mjs](../frontend/tests/cancellation.browser.mjs)，实际产品页面 + Identity + HTTP + SQLite + 官方知识 Connector。模型及知识内容为受控协议夹具，独立无头 Chrome 使用全新测试 Profile，不读取用户浏览器身份。

本次 4 个场景全部通过：

1. 网页发送请求，第一项计算已完成，第二项真实知识 HTTP 请求在途，第三项未开始。
2. 点击“停止生成”，HTTP 请求关闭；处理记录保留 `0.30 CNY`，第三项显示“未执行”；只有用户消息，没有取消后的正式 assistant 回复。
3. 刷新后结果一致，重复 Cancel 返回相同事件序号。
4. 完整宿主关闭重开后仍保持 cancelled；点击“继续处理”后仅推进剩余工作，在同一 Run 的第 2 次 attempt 完成并保存唯一正式回复。

Run 为 `crun_258ea7ab51bc2e99582f04d8181eb56d`：取消前 seq 14；取消后 / 刷新 / 重启为 seq 16，状态依次为 completed、failed、not_started；明确继续后 seq 26，状态为 completed、failed、completed，消息数由 1 变成 2。脚本核对网页“已保存”、实际 Run 终态及最后一条 assistant 消息的 Run ID。JavaScript 错误 0。

- 浏览器日志：`/tmp/domainry-B06-browser.log`。
- 宿主及 race：`/tmp/domainry-B06-browser-host.log`，77.59 秒通过；本次确实关闭 1 个官方知识 HTTP 请求。含前置目录验证，共模型 HTTP 11 次、知识 HTTP 23 次。
- 报告：`/tmp/domainry-B06-browser/report.json`，记录取消前、刷新、重启和恢复后的实际公开快照。
- 截图：同目录的 `before-cancel.png`、`cancelled-receipts.png`、`restart-cancelled.png`、`explicit-resume.png`。已查看处理记录截图，完成 / 失败 / 未执行三个状态及保留的金额清晰可见。
- 结束后浏览器和临时宿主已退出，8092 无监听器。

启动命令：`AGENT_TOOL_UI_ACCEPTANCE=1 go test -race ./internal/assembly/web -run '^TestConversationCatalogIdentityHTTPConnectionsAndBrowser$' -count=1 -timeout 18m -v`。ready 后设置 `AGENT_PLAYWRIGHT_MODULE` 为已安装 Playwright 路径、`AGENT_UI_TEST_OUTPUT=/tmp/domainry-B06-browser`，执行 `node frontend/tests/cancellation.browser.mjs`。

## 全量检查与已知范围

| 验证 | 结果 / 证据 |
| --- | --- |
| Agent `go test -p 2 ./...` | 通过，`/tmp/domainry-B06-agent-full.log`；集成包 21.873 秒，Web 包 36.411 秒 |
| SDK `go test -p 2 ./...` | 通过，`/tmp/domainry-B06-sdk-full.log` |
| 新架构约束专项 | 通过，`/tmp/domainry-B06-architecture.log` |
| 受影响包 `go vet` | 通过，`/tmp/domainry-B06-vet.log` |
| 前端状态测试 | 25 项通过，`/tmp/domainry-B06-frontend-tests.log`；含取消投影保留 pending / completed、未知结果和迟到回执 |
| 前端 TypeScript / 构建 | 通过，`/tmp/domainry-B06-frontend-build.log`；保留已有较大分块提示 |

初次 `/tmp/domainry-B06-initial.log` 的一条旧断言要求取消后一律丢弃回执，和本批需求冲突；已改为验证不相关执行者与已恢复运行的旧回执仍被拒绝，并增加原调用受限补存的测试。后续存储、集成和 race 均通过，没有删除原冻结 / 核查验收。

本次未调用真实 Verdent，未验证新 Provider 的远端任务停止 API；并未声称关闭 HTTP 就能撤销外部任务。实际外部故障覆盖和部署扩展继续在 H06 / H08，SDK 版本发布仍在 H04。B06 不取代业务补偿、后台任务取消或计划任务权限的后续实现。
