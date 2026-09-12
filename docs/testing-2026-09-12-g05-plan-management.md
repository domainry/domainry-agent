# G05 计划管理与自然语言入口验收

G05 已完成。用户可以在对话中创建一次性或重复的后台任务／提醒，也可以在“计划与提醒”页面查看、修改、暂停、恢复和删除当前产品中的个人计划。所有写操作都携带当前 revision；删除保存 Scheduler 墓碑，进程重启不会重新启用计划。

## 架构边界

| 层 | 责任与依赖方向 |
| --- | --- |
| Scheduler SDK | 发布 owner 中立的计划 DTO、管理服务和 Module／SaaS transport；不依赖 Agent、Tools 或 Scheduler 实现。 |
| Scheduler | 独占计划持久化、owner 过滤、revision CAS、删除墓碑和执行定义投影；继续复用 G02 的 run／lease／retry 内核。 |
| Tools SDK／Tools | Tools SDK 定义七个封闭的模型输入；Tools 将自然语言生成的结构化参数映射为 Scheduler SDK 命令，只从当前工具目录推导后台任务 Action。 |
| Agent composition | 注入 `ScheduledPlanService`，用当前 Request Identity 生成 owner，并将对话和产品 HTTP 都组合到同一个 Tools 适配器；不导入 Scheduler 实现。 |
| 产品页面 | 只提交计划 ID、当前 revision 和可编辑字段；不能提交 owner、Action、Runtime target、连接、Provider 载荷或原始 cron。 |

[架构机器审计](evidence/2026-09-12-g05-plan-management/architecture-audit.json)和[源码扫描](evidence/2026-09-12-g05-plan-management/boundary-scan.log)记录五仓依赖方向、diff 检查及 llm-proxy 工作区状态。

## 计划管理契约

Tools SDK 发布 `schedule_create`、`schedule_list`、`schedule_get`、`schedule_update`、`schedule_pause`、`schedule_resume` 和 `schedule_delete`。输入只接受：

- 一次性 RFC 3339 时间，或每天／每周／每月的结构化本地时间规则；
- 后台任务的目标、补充输入和当前可用的非 `task_*`／`schedule_*` 工具；
- 提醒的标题和正文。

后台任务 target 固定为 `agent/conversation_task_start`，allowed Actions 从当前已授权工具目录推导；提醒 target 固定为 `notification/publish_reminder`，Action 固定为 `notification.reminder.publish`。owner 来自当前 Runtime、workspace、user 和产品配置。模型与浏览器都不能覆盖这些字段。

Scheduler 的列表按 owner 和稳定 plan ID 分页。更新、暂停、恢复和删除都使用 owner + plan ID + expected revision 的数据库 CAS。完全相同的状态操作可以重放；旧 revision 返回冲突。删除把状态改为内部 `deleted`，公开 get/list 隐藏该记录，恢复投影仍会把执行定义保持为 disabled。Tools 的删除核查会再次调用 Scheduler，由真实墓碑判断重放，不会把从未存在的计划猜成成功。

## 自然语言与产品整链

确定性模型协议夹具经过实际 Agent conversation engine、真实 Identity、Tools 适配器和产品 HTTP 完成两条中文请求：

- “每周一整理待办”生成每周一 09:00、`Asia/Shanghai` 的后台任务，只允许 `todo_list`；
- “周五提醒我提交周报”生成每周五 17:00 的提醒，并使用固定 Notification target。

两次创建都进入持久确认卡片，批准后通过同一计划适配器保存。随后产品 API 完成列表、周一改周二、旧 revision 冲突、暂停、恢复、删除、跨用户隔离和 Agent 宿主关闭重开；删除记录没有重新出现。Scheduler 自身另有实际 SDK → 私有 HTTP → SaaS → application → SQLite 测试，覆盖重启、owner 隔离和 CAS，因此 Agent 测试无需导入 Scheduler 实现。

编译后的生产前端使用实际 Chrome 完成七步验收：列表、修改、刻意的 409 旧版本写入、刷新后暂停状态、Agent 宿主重启后恢复、二次确认删除、将精确 plan ID／revision 交回对话输入，以及 390px 无横向溢出。预期 409 单独记录，其余 JavaScript 错误、控制台错误和警告均为 0。[浏览器报告](evidence/2026-09-12-g05-plan-management/browser/report.json)、[宿主审计](evidence/2026-09-12-g05-plan-management/browser/host-audit.json)、[桌面截图](evidence/2026-09-12-g05-plan-management/browser/plans-initial.png)和[手机截图](evidence/2026-09-12-g05-plan-management/browser/plans-mobile.png)保留完整观察。

## 最终验证

| 范围 | 结果与证据 |
| --- | --- |
| Agent 整库 | 全部通过；Web 包 160.825 秒、Integration 包 81.307 秒。[日志](evidence/2026-09-12-g05-plan-management/agent-test.log) |
| Scheduler／SDK 整库 | 全部通过，包含真实 SaaS／SQLite 管理生命周期、墓碑和执行投影。[Scheduler](evidence/2026-09-12-g05-plan-management/scheduler-test.log) · [SDK](evidence/2026-09-12-g05-plan-management/scheduler-sdk-test.log) |
| Tools／SDK 整库 | 全部通过，包含严格 Schema、中文计划映射、路由字段拒绝和删除重放。[Tools](evidence/2026-09-12-g05-plan-management/tools-test.log) · [SDK](evidence/2026-09-12-g05-plan-management/tools-sdk-test.log) |
| 关键 race | Scheduler application／SaaS、Tools schedule adapter、Agent 自然语言／产品路由全部通过。[Scheduler](evidence/2026-09-12-g05-plan-management/scheduler-race.log) · [Tools](evidence/2026-09-12-g05-plan-management/tools-race.log) · [Agent](evidence/2026-09-12-g05-plan-management/agent-race.log) |
| 编译页面 Chrome | 七步通过，实际 GET／PUT／POST／DELETE 与预期 409 均记录。[日志](evidence/2026-09-12-g05-plan-management/browser-test.log) |
| 前端 | 58 项状态测试和生产构建通过。[测试](evidence/2026-09-12-g05-plan-management/frontend-test.log) · [构建](evidence/2026-09-12-g05-plan-management/frontend-build.log) |
| 静态与格式 | 五仓 `git diff --check`、Scheduler／Tools 两仓及 SDK 全量 vet、Agent product／web vet、禁止依赖扫描均通过。[扫描](evidence/2026-09-12-g05-plan-management/boundary-scan.log) |

本项没有修改 `/Users/tiger/Projects/anti/llm-proxy` 或 `/Users/tiger/Projects/devops/llm-proxy`。已有 F04 的实现只消费 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`，没有把 llm-proxy 接成模型服务。
