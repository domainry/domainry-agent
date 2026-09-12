# L01 会话后台任务启动验收

L01 已完成。会话模型可调用 `task_start`，以明确的 goal、input、工具授权范围和四项预算创建持久后台任务；工具调用只在任务与受理回执原子落库后返回稳定任务 ID。任务通过现有 Conversation worker、租约、步骤账本、当前授权与模型适配执行，没有复用旧业务 Task 的 `ProcessID`、`TaskDefinition` 或 Task Host。

## 架构边界

| 层 | 归属与依赖方向 |
| --- | --- |
| Agent SDK | 拥有 `ConversationTask*` 公共 DTO、`task_start` 工具定义及 mutation / worker repository 端口；不知道 Agent 的应用或数据库实现。 |
| Agent application | 校验并冻结目标、输入、选中工具的版本／动作／定义哈希／授权修订和预算；子运行只装配冻结工具，重新检查当前可用性与授权。 |
| Agent persistence adapter | 通过第 15 个 Agent 自有迁移维护 `_agent_conversation_tasks`；任务与 `tool.completed` 回执同事务提交，启动时再原子创建普通 ConversationRun。 |
| 现有 Conversation 执行 | 后台运行继续使用 Claim、Lease、fencing、逐步模型输入、工具调用账本和完成事务。后台 Run 不占 `Conversation.ActiveRunID`，因此前台消息可并行进入独立 Run。 |
| Web | HTTP / SSE 沿用公开 Conversation 契约；页面只使用公开 `background_task_id`、Run 范围、步骤与回执，不访问数据库或应用内部类型。 |

任务 worker 在服务启动时立即做一次持久队列恢复；`task_start` 落库和前台 Run 结束会发送状态转换信号。空队列不再周期访问 SQLite，只有存储失败才按部署 Poll 下限退避重试。这样保留重启恢复，又不会让一个没有任务的后台 worker 与无关前台长事务竞争数据库连接。[架构机器审计](evidence/2026-09-12-l01-background-task/architecture-audit.json)对四个核心生产文件执行旧 Task 字段与实现依赖扫描，结果为空。

## 已证明的行为

- `task_start` 输入 Schema 关闭额外字段，要求 goal、input、`allowed_tools` 及 `max_steps`、`max_tool_calls`、`max_output_bytes`、`timeout_seconds`；应用层再次按部署上限校验。
- 创建时只接受当前目录内、当前身份获准且当前连接可用的选中工具；拒绝重复工具和递归选择 `task_start`。
- 子 Run 的目录严格等于冻结范围。专项测试选择 `time_now` 后，未选中的 `calculate` 与 `task_start` 均没有进入模型请求，未选工具的连接状态也没有被探测。
- 稳定幂等键生成稳定任务 ID；故障注入证明任务和工具回执不会出现单边提交，重放直接返回原回执。
- 任务按 runtime / workspace / user 隔离；来源 conversation / run、执行 Run 与最终消息均保留关联。后台完成事务同步把任务更新为 `completed`，失败 Run 更新为 `failed`。
- 首次子模型调用被阻塞时，前台消息仍创建并完成；宿主关闭重开后，同一后台 Run 在 attempt 2 恢复，只执行一次 `time_now`，最终任务完成。

## 端到端与回归

`TestTaskStartThroughIdentityHTTPWorkerRecoveryAndSQLite` 使用实际 Identity Module、产品 HTTP 组合、临时 SQLite、实际 Conversation worker 与确定性模型协议夹具。它验证受理 ID、精确子目录、前台并行、宿主关闭／重开、attempt 2 恢复、消息来源标记和任务表终态。最终 race 用时 17.007 秒；编译后浏览器宿主验收用时 373.07 秒并通过。

编译后页面实际显示：

- `task_8626f0d44642ddf603bfa21fb1620f4a` 已受理；处理记录内 goal 为“核对 build 42”，input 为“确认当前时间并给出完成说明”，工具只有 `time_now`，预算为 3 步／1 次工具／1024 输出字节／30 秒。
- 子 Run 显示“后台任务”标签，唯一工具步骤为“查询时间”，结果为“build 42 已在后台核对完成。”；被阻塞期间提交的“继续前台处理这条消息”独立完成。
- 刷新页面后，任务 ID、来源、后台标签、处理记录与结果均保留；浏览器 warning / error 日志为空。详见[浏览器观察](evidence/2026-09-12-l01-background-task/browser-observation.json)及[宿主日志](evidence/2026-09-12-l01-background-task/logs/browser-host.log)。

最终验证：

| 范围 | 结果与证据 |
| --- | --- |
| L01 应用、持久化、HTTP race | 通过；包括空队列事件驱动测试与重启恢复。[日志](evidence/2026-09-12-l01-background-task/logs/l01-focused-race.log) |
| 旧账号未知写回归 race | 通过，266.5 秒；实际 `mail_send` 仅一次，两次恢复不重放。[日志](evidence/2026-09-12-l01-background-task/logs/preexisting-account-write-race-fixed.log) |
| Agent SDK 全量 race / vet | 通过；同时校正 N02 后续结果往返限额已改变但未更新的固定契约哈希。[日志](evidence/2026-09-12-l01-background-task/logs/agent-sdk-full-race-vet.log) |
| Agent 应用与持久化全量 race / vet | 通过。[日志](evidence/2026-09-12-l01-background-task/logs/agent-core-race-vet.log) |
| Agent 整库 | `go test ./... -count=1` 全部通过，Web 包 166.364 秒。[日志](evidence/2026-09-12-l01-background-task/logs/agent-full.log) |
| 前端 | 54 项测试通过，生产构建通过。[日志](evidence/2026-09-12-l01-background-task/logs/frontend-test-build.log) |
| 静态与范围 | Agent 全量 vet、两仓 `git diff --check` 通过；没有新分支或 worktree。[日志](evidence/2026-09-12-l01-background-task/logs/final-vet-diff-boundary.log) |

初次全 Web race 在旧账号写测试超时，随后定向复现发现空任务 worker 不应轮询同一 SQLite；改为状态转换唤醒，并新增“不轮询空队列”race 测试。初次失败日志原样保留，最终回归以上述通过日志为准。整库第一次还暴露测试 Identity discovery 缺少 SDK 已要求的 `workflow_workload_identity`，以及 Module 迁移断言仍为 14；两处测试契约修正后整库通过。

本项没有修改 `/Users/tiger/Projects/anti/llm-proxy`。其 HEAD 仍为 `a32407a678ea7f96d70b7557765e2a01262184d4`，工作区为空；Domainry 仍只消费已有 `POST /tool/web_search` 和 `POST /tool/web_fetch_jina`，没有把它接成模型服务。[机器清单](evidence/2026-09-12-l01-background-task.json)记录源码与日志哈希。

L01 不声明 `task_get`、`task_list`、取消或继续后台任务；这些分别属于后续 L02、L03。模型使用确定性协议夹具，未把它表述为真实模型质量验收。
