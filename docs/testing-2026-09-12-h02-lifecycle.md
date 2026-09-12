# H02 会话保留与用户数据清理验收（2026-09-12）

H02 已完成。已归档且没有活动 run 的持久会话现在进入 `agent.dialog.v1` 保留策略；清理前归档完整会话执行图，随后用 workspace、owner 和 revision 共同限定删除。用户删除通过 Lifecycle 的验证、预览、独立审批、执行与恢复流程到达 Agent、Todo、Knowledge 三个 owner，各 owner 只处理自己的数据和持久回执。

## 保留策略与清理内容

- Runtime 启动继续安装 Lifecycle 默认 `agent.dialog.v1`。真实组合测试在同一公开 Governance 契约上发布带审批引用和变更计划的版本 2，执行 `PreviewCleanup → CreateCleanupJob → ProcessCleanupJob → ListArchiveEntries`。测试策略为 24 小时，生产默认仍由已发布策略决定，本批没有硬编码绕过 Lifecycle。
- Agent 只选择已归档且不存在 `queued`、`running`、`waiting`、`pending`、`uncertain` run 的会话。候选和最终删除都核对 workspace、owner、conversation ID 与 revision；期间有更新或换 workspace 时不会删除。
- 归档 payload 包含会话、消息、run、摘要、事件、冻结输入、步骤、工具调用及外部响应、交互和会话来源任务。归档成功后才执行 purge；E2E 从 Lifecycle 归档表核对了用户输入和 `web_fetch` 外部响应原文。
- Agent retention 只删除 Agent 会话执行图。Knowledge 持有的附件、成果、资料库和另存副本不会被 Agent 的 retention 事务连带删除。

## 用户删除、引用与副本

- Agent 删除自己的会话、run、步骤、工具调用、事件、交互、后台跟进状态和个人记忆。Todo 删除自己的待办及变更回执。Knowledge 删除个人附件、成果、个人资料库／文档和本地内容，并分别写入可精确重放的 owner 回执。
- 共享资料不因某个用户删除而整库消失：删除该用户的成员关系；该用户创建的共享文档清除来源并匿名化创建者；其他成员持有的共享副本保留。个人另存副本和对应物理内容会删除。
- Knowledge 对已开始远端写入的附件／文档先进入 `deleting` 并排入有 fencing 的恢复 worker。厂商删除未明确确认前不写最终用户删除回执，也不丢弃本地原件和远端操作证据；确认后才完成物理与关系清理。
- 手工删除单个会话时，Agent 先提交自己的删除和请求回执，再调用 Knowledge 的公开 `ConversationReferenceLifecycle`。Knowledge 在自己的事务内处理引用和回执；响应丢失或宿主重启后使用同一 request ID 重放。Agent 的删除事务没有调用 Knowledge 表或私有存储方法。
- 三个 owner 都验证了 legal hold 拒绝删除、Alice/Bob 隔离和回执重放。Lifecycle 还保存删除注册，恢复备份后可重新执行相同删除。

## 端到端结果

Runtime 的实际模块组合使用真实 Identity Binding、Lifecycle Module、Agent Module 和同一个 SQLite 数据库。测试先为 Alice/Bob 建立 Agent 会话，并分别写入 Todo 与 Knowledge owner 数据；Lifecycle 依次执行创建、二次验证、预览、独立审批和删除。执行步骤中出现 `agent`、`todo`、`knowledge` 三个完成回执，Alice 数据消失，Bob 数据保持不变，删除注册重放成功。

同一组合随后建立一个 48 小时前的归档会话，写入消息和工具外部响应，发布 H02 验收策略并创建 purge job。worker 完成 1 次归档和 1 次清理，活动的 Bob 会话仍可读取，归档内容同时包含 `retained user input` 与 `retained external response`。这条测试在普通模式和 `-race` 模式都通过。

Knowledge 的外部删除使用隔离协议夹具，因为当前没有厂商 OAuth 配置；它实际覆盖远端写响应丢失、远端删除响应丢失、重启恢复、删除确认前保留证据以及预检期间撤权。这里没有把协议夹具写成真实厂商账号验收。

## 验证结果与边界

- Agent、Agent SDK、Knowledge、Todo 全量 `go test ./...` 通过；Agent 全量包括 integration 与 Web/Identity/完整宿主重启场景。
- Runtime 的 `runtime/bootstrap/runtime` 和 `runtime/boundary` 包通过；H02 的 retention + subject erasure E2E 在 `-race` 下通过。
- Agent、Knowledge、Todo 的 H02 持久化专项 `-race` 通过；五仓对应 `go vet` 通过。
- 架构测试确认 Agent subject handler 不读取或删除 Todo/Knowledge 表，Agent 会话删除事务不调用 Knowledge 清理；Runtime 只通过 Agent SDK 的可选 `SubjectBinding` 组合 owner handler。
- Runtime `go.mod`、`go.sum` 和发布锁没有在 H02 提前修改，依赖发布仍按顺序留给 H04。PostgreSQL/MySQL 和多实例验收按清单留给 H08。
- `llm-proxy` 两个工作区保持干净；它在本项目中的职责仅是提供已有 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina` 工具接口，不作为模型代理，本批也未修改它。

全部命令、日志、源码摘要与限制见[机器清单](evidence/2026-09-12-h02-lifecycle.json)，依赖方向和工作区检查见[架构审计](evidence/2026-09-12-h02-lifecycle/architecture-audit.json)。
