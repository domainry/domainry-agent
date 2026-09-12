# A08 会话独立宿主与公共执行规则验收

日期：2026-09-10。按 TODO 顺序承接 A03，核对旧 Task 可复用部分并补齐会话独立绑定入口。A08 完成：Agent / SDK 全量、专项 race、静态检查与实际 Identity / HTTP / SQLite 的 7 个网页场景通过。

## 代码核对与本次变化

| 能力 | 当前代码与边界 |
| --- | --- |
| 公共调用预算 | `internal/execution/budget.go` 的 `Budget.Reserve` / `AddCost` 已由 Conversation、Task 工具账本与 Interactive 共用。规则没有身份、数据库或业务 Task 字段。Task 在原事务及 fencing 下预留；Conversation 遍历冻结步骤重建逻辑调用数。 |
| 成本口径 | Task 保留原工具权重与 low / normal / high 配置；Conversation 只计调用次数，成本 charge 为 0。这不是模型账单或 token 计费。 |
| 完整 Schema 校验 | `internal/execution/json.go` 由 Task 输出和 Conversation 工具共同使用；校验嵌套类型、枚举、范围、额外字段、格式，禁止执行期间下载远程 Schema。 |
| 实时授权 | `task_authorization.go` 的同一解析规则用于 Task 启动与工具回调；回调检查当前主体、Task 版本与允许工具后才预留预算。Conversation 使用独立 `ConversationToolHost`，恢复冻结输入、确认与实际调用继续走当前授权。 |
| 待补缺口 | SDK 已有独立 `ConversationApplicationHost`，但 Module 延迟装配只能通过完整 `ApplicationHostBinder` 进入，仍迫使只需要聊天的宿主实现 Interactive / Task / Proposal / Audit / Analysis 端口。 |
| 本次新增 | SDK `modulehost.ConversationApplicationHostBinder.BindConversationHost` 与 Agent `module/conversation_assembly.go` 实现；只装配会话服务和 Adapter，开始处理持久队列，不装配旧应用端口。 |
| 真实产品接入 | `internal/assembly/web/host.go` 先完成 Identity 装配，再调用会话独立绑定入口。普通与外部 Identity 的 Web 模式共用该入口。 |
| 延迟默认值修正 | `module/factory.go` 在延迟模式下不再提前捕获持久层宿主的授权器 / 工具宿主 / 连接状态；默认值来自后绑定的会话宿主，显式选项仍优先。 |

不合并两类状态机。旧 Workflow Task 的 ProcessID、NodeInstanceID、TaskDefinition、凭证和回调仍属于旧业务任务；会话只使用自己的 runtime / workspace / user、conversation / run / step / call 身份。无需为每轮聊天创建旧 Task 记录。

## 装配契约

1. 持久层宿主实现 `DeferredConversationHost` 并返回 true，先迁移和打开存储。
2. 此时 `Conversations()` 为 nil，未发布会话 HTTP Adapter，已入队运行的 Attempt 保持 0。
3. 通过可选 `ConversationApplicationHostBinder` 绑定授权器和可选业务源。无业务源时仍可使用个人工具。
4. 成功后发布会话服务 / Adapter 并恢复队列。必须在读取服务、Descriptor、Adapter 或开始 HTTP 服务之前完成绑定。
5. nil / 缺少授权器、非延迟启动、重复绑定和关闭后绑定均拒绝；不能用此接口替换运行中的服务。旧完整 `BindApplicationHost` 保留兼容入口。

## 后端专项结果

新增及调整的集成测试在 [conversation_module_binding_integration_test.go](../integration/conversation_module_binding_integration_test.go)：

- 仅包含授权器与可选业务源的宿主即可绑定；断言它不实现完整旧 `ApplicationHost`。
- 绑定前真实 SQLite 中的排队运行不被领取，失败绑定不发布服务，成功绑定只增加一个会话 Adapter。
- 第一笔效果完成后模型失败；关闭整个 Module、重新打开并绑定，撤权后恢复在模型调用前拒绝，恢复权限后仍沿用同一 Run。
- 预算为 2：两笔工具效果可完成；第三笔返回 `execution_limit`。两种场景均只发生 2 次效果、0 次 reconcile、4 次模型请求，Run Attempt 为 3；旧 Task / TaskDefinition / Interactive 表均为 0 行。
- 持久层宿主故意提供全部拒绝的过早授权和状态策略，验证自动装配使用后绑定的当前宿主；显式指定授权器或状态策略则保留指定行为。
- 无模型部署仍可创建和管理会话，但不宣称模型就绪；旧完整宿主的业务源绑定、重复绑定和历史撤权测试仍通过。

共享机制回归使用已有用例，重新执行：

- `TestReservationsRejectOverflowAndLeaveUsageUnchanged`：预算溢出、负数与失败后使用量不变。
- `TestTaskCompletionValidatesTheFullOutputContract`：Task 与 Conversation 的 7 组 Schema 结果一致，远程 `$ref` 被拒绝且没有 HTTP 请求。
- `TestTaskCallbackHTTPReauthorizesBeforeAtomicBudgetReservation`：真实 Module / SaaS HTTP 回调，撤权与取消在预算预留前拒绝，恢复后沿用原预算，超限以 429 和不可重试错误传递。
- `TestPublicAgentBindingPersistsRecoversAndExecutesWithHostAuthority`：旧 Module / SaaS Task 持久化、恢复、宿主授权与回调未回归。

命令：

```sh
go test -race -p 2 ./internal/execution ./internal/application ./integration -run 'TestReservations|TestTaskCompletionValidatesTheFullOutputContract|TestTaskCallbackHTTPReauthorizesBeforeAtomicBudgetReservation|TestPublicAgentBindingPersistsRecoversAndExecutesWithHostAuthority|TestConversationOnlyBinding|TestDeferredConversationModule|TestConversationCatalogConnectionChangesAndFrozenResume' -count=1 -v
```

结果：通过，集成包 9.235 秒，无 race。日志 `/tmp/domainry-A08-race.log`。先前绑定专项 `/tmp/domainry-A08-binding.log` 也通过，含实际 Identity / HTTP / SQLite 的目录、撤权和恢复回归。

## 网页与全量验证

复用 [工具目录网页脚本](../frontend/tests/tool-catalog.browser.mjs) 和 [Identity / HTTP 宿主](../internal/assembly/web/conversation_tool_catalog_test.go)，产品宿主已改走新增绑定入口。独立无头 Chrome 使用全新测试 Profile，操作临时 `catalog-workspace`，不使用用户的浏览器身份。

本次 7 个网页场景全部通过：

1. 登录后模型只收到已挂载且经 Identity 授权的工具。
2. 知识连接停用时不提供检索，本地计算继续执行。
3. 工具开关与 Identity 撤权独立生效。
4. 模型选中计算后停用工具，实际调用被阻止；完整宿主重启、网页刷新、恢复后继续原 Run，只有一条完成的计算记录。
5. 连接检查错误经过脱敏，网页恢复原失败运行。
6. 恢复知识连接后实际调用 HTTP Connector，宿主重启后仍可读取保存的回复。
7. 再次断开连接，历史来源复核阻止复用并隐藏回复；恢复连接后可见。

脚本检查网页“已保存”状态，并通过同一登录会话的 HTTP 接口核对最终 `completed` Run 和已落库 assistant 消息。报告记录 8 个不同的已完成 Run，其中 2 个 Attempt 为 2；浏览器 JavaScript 错误 0。已查看重启恢复截图，计算值为 `0.30 CNY`，回复和“已保存”状态均可见。宿主最终检查旧 Task、TaskDefinition 和 Interactive 三表均为 0 行。

| 验证 | 结果及证据 |
| --- | --- |
| Agent `go test -p 2 ./...` | 通过，`/tmp/domainry-A08-agent-full.log`；Web 包 22.483 秒，集成包 13.950 秒 |
| SDK `go test -p 2 ./...` | 通过，`/tmp/domainry-A08-sdk-full.log` |
| 受影响包 `go vet` | 通过，`/tmp/domainry-A08-vet.log` |
| 网页脚本 | 通过，`/tmp/domainry-A08-browser.log` |
| 网页宿主及 race | 通过，94.45 秒；`/tmp/domainry-A08-browser-host.log`，模型 HTTP 20 次、知识 HTTP 41 次（含来源复核） |
| 网页报告与截图 | `/tmp/domainry-A08-browser/report.json`；同目录包含目录、调用前停用、重启恢复、错误、知识恢复和历史隐藏的 6 张截图 |

启动命令：`AGENT_TOOL_UI_ACCEPTANCE=1 go test -race ./internal/assembly/web -run '^TestConversationCatalogIdentityHTTPConnectionsAndBrowser$' -count=1 -timeout 18m -v`。宿主 ready 后设置 `AGENT_PLAYWRIGHT_MODULE` 为已安装 Playwright 的模块路径、`AGENT_UI_TEST_OUTPUT=/tmp/domainry-A08-browser`，运行 `node frontend/tests/tool-catalog.browser.mjs`。

浏览器完成后自动关闭临时 Chrome 并通知宿主退出；已确认 8092 无监听器。前端源码未改动，本次沿用现有构建并完成真实网页回归。

## 失败与修正

初次专项测试导入了不存在的 SDK `modulehttp` 包，集成测试编译失败；改为现有 Foundation `modulehttp` 契约后通过。日志 `/tmp/domainry-A08-initial.log` 保留失败。没有通过新增依赖绕过编译问题。

## 范围

本批是会话宿主解耦和执行机制复用验收；模型与知识上游使用协议夹具。没有新增真实 Verdent 或真实私有多库验收，没有发布新的 SDK 版本；脱离本地工作区的依赖发布仍在 H04。取消外部在途操作、后台任务和计划触发分别继续在 B06 / L / G，不能由 A08 通过代替。
