# L03 后台任务取消与恢复验收

L03 已完成。`task_cancel` / `task_resume` 可从会话工具和公开 HTTP 控制当前用户的具体后台任务，继续复用同一条 Conversation Run、租约、fencing、工具账本与外部结果对账，不建立第二套执行引擎。服务端返回当前状态允许的控制动作，网页只消费该投影。

## 架构边界

| 层 | 归属与依赖方向 |
| --- | --- |
| Agent SDK | 拥有两个工具定义、独立 `ConversationTaskControlService`、控制状态 DTO、HTTP 契约和 queued-task 控制 repository 端口；不知道 Agent 应用或数据库实现。 |
| Agent application | 解析具体 task ID，通过 SDK 端口读取任务；已有执行 Run 统一调用 Conversation 的 Cancel / Resume，只有尚未生成 Run 的排队任务调用窄控制端口。应用层未导入存储、HTTP 装配或其他服务实现。 |
| Agent persistence adapter | 排队任务以 owner + task ID + 当前状态做事务 CAS；已启动任务由原 Run 状态变化在同一事务同步 task 状态、错误、完成时间和终态事件引用。 |
| Identity / 工具授权 | HTTP 控制先检查精确 `task_cancel` / `task_resume` 权限；恢复再检查 Conversation execution 权限，worker 在模型调用前重新核对冻结工具的当前定义、权限和可用性。 |
| 外部写入 | `needs_reconciliation` 继续原 Run 和原不确定调用的幂等键，调用 Reconcile 后才把账本改为完成；不会用新任务或新调用冒充重试。 |
| Web | `TaskDialog` 只调用公开任务 HTTP；按钮由服务端 `control` 投影决定，取消有页面内二次确认，已关闭的用户交互不显示错误的继续按钮。 |

[架构机器审计](evidence/2026-09-12-l03-task-control/architecture-audit.json)保存公开端口、依赖扫描、执行归属、授权、递归限制和 `llm-proxy` 边界。

## 状态处理与边界

- 排队且尚无执行 Run 的任务可以取消；重复取消返回同一终态。此类取消可以重新排队，并唤醒原后台 worker。
- running、`waiting_user`、`waiting_confirmation` 和 `needs_reconciliation` 都可以取消，已完成的业务效果和不确定外部结果继续保留在原工具账本。
- `waiting_user` / `waiting_confirmation` 不能用 `task_resume` 跳过，返回 `interaction_response_required`；取消后已关闭的 input / confirmation 返回 `interaction_closed`，页面提示重新创建任务。
- 普通 failed / cancelled Run 在恢复时清除旧错误、结果消息和任务完成引用，增加同一 Run 的 attempt；completed 任务不可恢复。
- `needs_reconciliation` 可直接继续；测试保存原 uncertain 调用和幂等键，核查完成后关闭 reconciliation interaction，并由同一 Run 提交 task completion event。
- `task_start` 对同一个权威 owner + source Run 最多接受 4 个子任务，第 5 个得到持久 `task_child_limit` 失败回执。任务准备和恢复目录都拒绝任何 `task_*`，因此后台任务深度固定为一层，不能创建后代、查询／控制自身或在同一 worker 内等待自己。

## 重新授权证据

真实 Identity／HTTP／SQLite 场景先让一个带冻结 `time_now` 范围的后台任务在 attempt 1 发生模型失败：

1. 撤销 `task_resume` 权限后，`POST /agent/conversation-tasks/{id}/resume` 返回 403，任务未变化。
2. 恢复控制权限但撤销冻结的 `time_now` 权限后，恢复请求受理；attempt 2 在进入模型前以 `tool_access_denied` 失败，没有调用模型或时间工具。
3. 恢复 `time_now` 后，模型实际调用 `task_resume`；同一子 Run 在 attempt 3 执行一次 `time_now` 并完成，旧失败完成引用已清除。

这同时证明“有控制权限”不能替代子任务工具权限，冻结范围也不是永久授权。

## 端到端与网页验收

`TestTaskStartThroughIdentityHTTPWorkerRecoveryAndSQLite` 使用真实 Identity Module、产品 HTTP 组合、SQLite、Conversation worker、宿主关闭／重开和确定性模型协议夹具。除上述重新授权外，它还验证：模型实际调用 `task_cancel` / `task_resume`；等待用户任务的直接 resume 返回 409；取消写入精确 Run 终态引用且重复取消不改变引用；失败恢复清除旧回执；前台会话和后台 Run 仍相互独立。聚焦 race 通过，Web 场景用时 86.75 秒。

编译后页面由 `frontend/tests/task-control.browser.mjs` 自动操作实际按钮：

- 失败任务显示“继续任务”，只发送该任务 ID 的 `/resume`；attempt 2 执行一项 `time_now` 后显示“失败任务已恢复完成。”，完整刷新后仍为完成状态。
- 等待用户任务显示原问题和“停止任务”，不显示继续；点击后先出现具体影响提示，再确认发送该任务 ID 的 `/cancel`。
- 取消后显示“原等待事项已关闭，无法直接继续；请重新创建任务。”；完整刷新后保持，390×844 页面无横向溢出。
- 登录后的 JavaScript error、console error 和 warning 均为 0。登录前编译应用探测产生的一次缺失资源 404 和两次未认证 401 单独记录，没有混入已认证验收结论。

[浏览器观察](evidence/2026-09-12-l03-task-control/browser-observation.json)、[自动化报告](evidence/2026-09-12-l03-task-control/browser/report.json)、[桌面失败任务截图](evidence/2026-09-12-l03-task-control/browser/failed-task.png)、[手机取消结果截图](evidence/2026-09-12-l03-task-control/browser/cancelled-task-mobile.png)及[宿主日志](evidence/2026-09-12-l03-task-control/logs/browser-host.log)保存实际结果。宿主测试含数据准备和浏览器操作共 28.51 秒，结束后 8092 已关闭。

## 最终验证

| 范围 | 结果与证据 |
| --- | --- |
| L03 应用／持久化／跨层 race | 全部通过；覆盖状态矩阵、递归和范围拒绝、4 个子任务上限、排队取消恢复、失败重试、对账完成及真实 Identity Web 场景。[日志](evidence/2026-09-12-l03-task-control/logs/l03-race.log) |
| Agent 整库 | `go test ./... -count=1 -timeout 12m` 全部通过，Web 包 158.417 秒。[日志](evidence/2026-09-12-l03-task-control/logs/agent-full.log) |
| Agent SDK | `go test -race ./... -count=1` 全量通过；`go vet ./...` 通过。[race](evidence/2026-09-12-l03-task-control/logs/sdk-race.log) |
| Agent 静态检查 | `go vet ./...`、两仓 `git diff --check`、既有 architecture 测试和禁止依赖扫描通过。[架构审计](evidence/2026-09-12-l03-task-control/architecture-audit.json) |
| 前端 | 56 项测试通过；TypeScript 和 Vite 生产构建通过；编译产物浏览器脚本通过。[测试](evidence/2026-09-12-l03-task-control/logs/frontend-test.log) · [构建](evidence/2026-09-12-l03-task-control/logs/frontend-build.log) · [浏览器](evidence/2026-09-12-l03-task-control/logs/browser-client.log) |

一次额外的 Agent 全量 race 尝试因 Web 包默认 10 分钟总时限终止，当时正在既有账号写入用例 `unknown_original_receipt`，其余已输出包均通过；该超时没有作为通过证据。[超时日志](evidence/2026-09-12-l03-task-control/logs/agent-race-timeout.log)保留原始栈。随后 L03 三层聚焦 race 和 Agent 全量非 race 都独立通过。

浏览器准备阶段曾发现测试模型错误地用整个会话历史判断当前子任务类型，导致后续等待任务被旧失败输入误分类；已改为只读取当前子 Run 最近一条用户输入并重新通过。[失败日志](evidence/2026-09-12-l03-task-control/logs/browser-host-failed-fixture.log)保留该发现。

本项没有修改 `/Users/tiger/Projects/anti/llm-proxy`。该服务只为 F04 提供现有 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`；Agent 不把模型请求接入它，也不修改它。

模型为确定性协议夹具，本项证明系统状态、权限、恢复、外部结果对账和网页操作，不声明真实模型质量验收。
