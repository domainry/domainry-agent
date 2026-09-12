# L02 后台任务查询验收

L02 已完成。会话模型、HTTP 客户端和编译后网页均可通过 `task_get` / `task_list` 查询当前用户获准查看的后台任务，看到进度、结果、等待事项和成果，并通过公开关联 ID 返回来源会话、执行 Run 与成果版本。关闭并重新打开网页后从服务端重新读取同一任务状态。

## 架构边界

| 层 | 归属与依赖方向 |
| --- | --- |
| Agent SDK | 拥有查询 DTO、`ConversationTaskService`、HTTP 路由、`task_get` / `task_list` 定义和 `ConversationTaskReadRepository` 端口；不知道 Agent 应用和数据库实现。 |
| Agent application | 组合当前获准读取的 Conversation Run、原消息与公开 Artifact 服务，生成受限任务投影；只依赖 SDK 端口，不导入具体存储或 Knowledge 实现。 |
| Agent persistence adapter | 按 runtime / workspace / user 所有者查询任务，状态、来源会话和目标子串筛选使用绑定过滤条件与稳定游标；内部冻结范围继续留在持久化模型。 |
| Conversation 执行 | 进度、等待和结果以当前授权后的持久 Run / 消息为准。完成 Run 的终态事件与任务终态引用在同一事务提交，running → terminal 采用 CAS。 |
| Knowledge / 成果 | 任务只调用公开 Artifact 查询并匹配精确执行 Run；撤销来源访问后，步骤、结果和成果立即隐藏，恢复后重新投影。 |
| Web | `TaskDialog` 只调用公开 HTTP；来源会话、Run 与成果跳转只消费返回的公开 ID。筛选结果改变时同步校正选中任务。 |

[架构机器审计](evidence/2026-09-12-l02-task-query/architecture-audit.json)记录公开端口、依赖方向、幂等完成事务和 `llm-proxy` 边界。

## 已证明的行为

- `task_get` 返回稳定任务 ID、状态、目标、输入、允许的工具名、四项预算、当前 Run 进度、等待项、结果预览、成果和来源关联；不会返回 `tool_scope`、definition hash 或 authorization revision。
- `task_list` 按当前所有者隔离，支持状态、来源会话、目标子串、limit 与绑定用户和过滤条件的稳定游标。模型可在一次会话 Run 内先调用 `task_list` 再调用 `task_get`。
- 等待确认的后台 `artifact_create` 显示 `waiting_confirmation`、具体工具和问题；沿用现有交互答复后从 attempt 2 继续，完成时关联精确成果。等待补充信息的 `ask_user` 显示 `waiting_user`、问题和两个选择。
- 任务结果来自与任务执行 Run 精确绑定的 assistant 消息；成果来自公开 Artifact 服务且 `source_run_id` 必须等于任务执行 Run。
- 撤销成果来源读取权限后，任务仍可作为本人任务被定位，但步骤、结果和成果被隐藏并返回访问错误；恢复权限后重新出现。
- `Finish` 已在同一数据库事务写入 `run.completed` / `run.failed` 事件，再用 running 状态 CAS 保存精确 event sequence 和确定性完成引用。重复 `Finish` 不会增加终态事件，持久化测试核对终态事件始终为 1。

## 端到端与网页验收

`TestTaskStartThroughIdentityHTTPWorkerRecoveryAndSQLite` 使用真实 Identity Module、产品 HTTP 组合、SQLite、Conversation worker、宿主关闭／重开和确定性模型协议夹具。它先完成 L01 的同一子 Run attempt 2 恢复，再通过真实 HTTP 查询任务，实际让模型调用 `task_list` / `task_get`，验证等待确认、批准恢复、成果创建、权限撤销／恢复与等待用户。专项 race 通过，用时 53.866 秒。

编译后网页实际完成以下检查：

- 页面完整重载后仍显示三条服务端持久任务：等待补充发布渠道、生成后台验收纪要、核对 build 42。
- 等待任务 `task_1345266f259753cee8c0c099c2985501` 显示 `ask_user`、1 步、1 次工具、第 1 次处理、3/1/30 预算和“请选择发布渠道”。
- 选择“已完成”后等待任务隐藏，详情自动改为当前结果中的任务；搜索 `build 42` 后只显示对应任务及已保存结果。
- 任务处理记录跳到精确 `time_now` Run；成果任务打开“后台验收纪要”版本 1，再由成果来源回到精确 `artifact_create` Run，两处均显示第 2 次处理、1 次完成调用及正确回复。
- 浏览器 console error / warning 均为 0。[浏览器观察](evidence/2026-09-12-l02-task-query/browser-observation.json)和[宿主日志](evidence/2026-09-12-l02-task-query/logs/browser-host.log)保存结果；宿主测试含人工验收等待共 337.496 秒。

真实网页验收发现并修复两处问题：过滤请求返回新页面时旧选中详情未同步，跨会话打开 Run 时 `selectedId` effect 又清空目标 Run。现在任务页用当前结果原子校正 selected ID，`activate` 在一次状态变更中设置会话和目标 Run，重新构建并在真实浏览器复验通过。

## 最终验证

| 范围 | 结果与证据 |
| --- | --- |
| Agent L02 核心 race | 应用、持久化和 HTTP manifest 通过；覆盖 owner 隔离、过滤／游标、完成事件唯一性。[日志](evidence/2026-09-12-l02-task-query/logs/l02-core-race.log) |
| 完整跨层 E2E race | 通过，53.866 秒。[日志](evidence/2026-09-12-l02-task-query/logs/l02-web-race.log) |
| Agent SDK 全量 race / vet | 全量 race 通过；SDK vet 通过。[race 日志](evidence/2026-09-12-l02-task-query/logs/sdk-race.log) |
| Agent 整库 | `go test ./... -count=1` 全部通过，Web 包 205.891 秒。[日志](evidence/2026-09-12-l02-task-query/logs/agent-full-final.log) |
| Agent 静态检查 | `go vet ./...`、`git diff --check` 和既有 architecture 测试通过。[vet](evidence/2026-09-12-l02-task-query/logs/agent-vet.log) · [diff](evidence/2026-09-12-l02-task-query/logs/diff-check.log) |
| 前端 | 56 项测试全部通过；TypeScript 与 Vite 生产构建通过。[测试](evidence/2026-09-12-l02-task-query/logs/frontend-test.log) · [构建](evidence/2026-09-12-l02-task-query/logs/frontend-build.log) |

首次组合 race 在所有 L02 断言通过后，仅最后一个父 Run 在 race 插桩和 10 秒测试等待上限下超时；把同一完整 E2E 的等待上限改为 30 秒后，隔离 race 通过。第一次 Agent 整库暴露 SaaS manifest 的固定测试计数仍为 96，新增两个查询路由后应为 98；只修正契约计数后整库重新运行并全部通过。初始日志保留在 `l02-focused-race-initial.log` 与 `agent-full-initial-count-failure.log`，最终判定以上述通过日志为准。

本项没有修改 `/Users/tiger/Projects/anti/llm-proxy`。Domainry 对它的边界仍是只消费现有 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`，没有把它接成模型服务。

L02 不包含任务取消、失败重试或用户等待后的通用继续控制；这些严格留在下一项 L03。模型使用确定性协议夹具，本项证明协议和系统恢复，不声明真实模型质量验收。
