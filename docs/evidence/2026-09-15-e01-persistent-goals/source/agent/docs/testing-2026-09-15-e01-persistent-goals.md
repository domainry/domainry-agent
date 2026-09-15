# E01 持久目标与持续推进验收

日期：2026-09-15。本轮完成普通后台任务的持久目标生命周期、完整可更新约定和跨执行轮次继续。E01 验收完成，完整清单进度为 **7／25**。委派任务仍通过既有协作约定更新；完成条件的独立程序、Agent 或用户验收继续归 E03。

## 契约与持久化

- [Agent SDK 任务契约](../../domainry-agent-sdk/conversation_task.go)增加完整 `ConversationTaskBrief`、明确／推定字段来源、`ConversationGoalProgress`、约定修订和先前运行引用。旧调用缺少 brief 时生成推定 v1；`due_at` 保持可选，服务端不补造截止时间。
- `task_update`、`PATCH /agent/conversation-tasks/{taskID}/agreement` 和对应 RPC／Module／SaaS 转发使用完整替换、`expected_revision` 与 `client_id`。任务运行中或已完成时拒绝修改；委派任务要求走 `delegation_update`，避免绕过依赖传播。
- [任务约定存储](../internal/infrastructure/persistence/database/agent/conversation_task_agreement_store.go)在同一事务内核对 owner、当前修订、状态和幂等请求。新 brief 版本必须恰好递增一版；旧执行进入 `previous_execution_runs`，恢复后建立冻结新约定版本的新 Run。
- 目标投影随任务记录持久化，区分 `active`、`blocked`、`paused`、`completed` 与 `budget_exhausted`，并保存阶段、完成／剩余事项、阻塞代码和更新时间。运行步骤与调用数继续作为执行活动，不冒充业务完成比例。

## 权限与来源

- HTTP 入口先由 Identity 解析当前主体；SDK 的 `task_update` 属于 `BackgroundTasks` 写范围，应用层再检查当前工具授权，拒绝任何需要确认但尚未确认的写入。
- 任务读取、取消、恢复与约定更新继续检查当前协作 `execution_read` 和 `manage`；当前 owner／任务 ID／修订由存储层再次绑定。委派任务不能从普通任务入口修改约定。
- 约定更新回执保存当时完整 brief 和修订。后续模型读取历史回执时核对准确工具调用、不可变返回值、运行／结果／成果引用及当前阅读权限；旧回执不授予当前控制权。

## 页面行为

- [任务约定页面](../frontend/src/TaskAgreement.tsx)展示当前版本、修订、目标状态、阶段、剩余／完成事项、阻塞原因，以及目标、交付物、使用者、截止时间、完成条件、约束和假设的来源。
- 可修改状态下提交完整新版本；变更字段标为“用户明确”，保留字段继续标记原来源。页面要求使用者和完成条件，未填写期限时明确显示“未设置截止时间”。
- 失败任务更新约定后可继续；先前运行保持可打开。完成、等待、取消和 390 px 页面在刷新后都从服务端恢复。

## 验收

- Agent SDK `go test ./...` 通过；HTTP 路由总数和 `task_update` 工具目录回归通过。
- Agent 产品包 `go test ./internal/... ./module ./remote ./server ./integration` 通过，其中 Web 组合包 440.058 秒，integration 92.355 秒。
- 应用与持久化核心测试在 `-race` 下通过；另行复验协作执行、权限撤回后的历史任务回执和外围包。
- 前端 `npm test -- --runInBand` 共 85 项通过；TypeScript 与 Vite 生产构建通过。
- 真实 Identity／HTTP／SQLite／Chrome 根场景 30.775 秒通过。浏览器完成推定 v1 → 用户完整 v2 → 新 Run 继续并完成 → 整页刷新保留；另验证等待任务显式取消和 390 px 页面。登录后 JavaScript 错误、控制台错误与警告均为 0。

命令、结构化报告、源码摘要、日志与截图见[验收证据](evidence/2026-09-15-e01-persistent-goals/commands.md)。
