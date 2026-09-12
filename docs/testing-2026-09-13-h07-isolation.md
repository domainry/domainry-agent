# H07 用户、工作区、权限与连接隔离验收（2026-09-13）

H07 已完成。两个当前用户、不同个人工作区、权限撤销、连接失效与后台恢复均通过实际 Identity HTTP、外部身份验证边界、Agent／Integration HTTP、SQLite、worker 和编译产品验证。未发现需要修改生产实现的缺口；本项只新增验收记录与原始证据。

## 隔离与拒绝结果

| 场景 | 实际结果 |
| --- | --- |
| 两用户个人数据 | 外部 Identity 的 `account-a` 与 `account-b` 得到不同用户和不同个人工作区。B 读取 A 的会话返回 404，B 的记忆列表不包含 A 的内容。Integration 中第二个真实 Identity 用户和无权管理员都不能读取第一个用户的个人连接。 |
| stale／伪造 scope | B 携带 A 的 scope 创建会话返回 409；将 A 的主体与 B 的 workspace 拼接后，Knowledge 授权和后台个人工具授权均拒绝。Module／SaaS 的错误工作区 task poll 返回 `agent.task.not_found`，持久化仓库也找不到跨 workspace 记录。 |
| 持久归属 | 完整关闭并重开 Agent 后，A 的 workspace scope 不变且原会话可读。Agent SQLite 有 2 个 workspace 映射、0 张 Identity 表；每次外部身份请求重新调用验证端点，没有把认证库复制进 Agent。 |
| 当前用户停用 | 已登录用户提交真实 durable Run 后停在 worker admission。Identity 停用使 `/app/session` 和会话读取同时返回 401；Run 在模型调用数为 0 时保存 `execution_access_denied`，没有 assistant 回复。恢复账号和重开宿主仍保留失败状态，用户明确恢复后同一 Run 以 attempt 2 完成。 |
| 权限撤销 | 账号列表权限撤销立即返回 403，并在 Integration／Agent 完整重开后继续拒绝；恢复权限后才可再次读取。后台等待确认时撤销成果权限，批准被拒绝且成果版本仍为 0。`task_resume` 权限撤销在入口返回 403，不预留新执行；恢复入口权限但撤销冻结 `time_now` 后，worker 以 `tool_access_denied` 失败。 |
| 连接失效 | 冻结输入中已完成的写调用保存后，模型失败并重启；连接设为不可用时明确恢复在模型前返回 `tool_unavailable`，连接恢复后完成且不重复写效果。具体操作已经授权后连接再失效，也在 Begin／Invoke 前二次拦截。 |
| 连接撤销与 scope | OAuth callback 身份切换不提交 callback，也不交换 token；明确撤销连接跨完整重开保留 `revoked`。仅日历授权仍可作为连接存在，但需要账号资料 scope 的“测试连接”在 Provider I/O 前拒绝，重启不会扩大 scope。 |
| 后台重新授权 | Module／SaaS callback 在原子预算预留和工具效果前重新检查当前授权；撤权时没有预算和效果。后台任务恢复分别重新检查 `task_resume` 与冻结工具权限；恢复权限后沿原任务进入后续 attempt 完成。计划触发的等待确认也按当前 Identity 检查，不复用触发时权限。 |

## HTTP 与浏览器端到端

身份撤销浏览器测试使用真实 Identity HTTP、Agent HTTP、SQLite、worker 和编译后的产品页面。浏览器先提交一个 durable Run，随后测试控制端停用当前 Identity 并释放 worker：浏览器登录态与会话读取立即失效，模型调用数保持 0。恢复账号后，页面显示持久化的拒绝状态；完整关闭并重开宿主仍不自动推进。用户点击“重新生成”后，同一 Run `crun_38d5d76f9bfef15b78cc58e790d69cc9` 从 attempt 1 进入 attempt 2，最终只新增一个 assistant 回复。报告中的 JavaScript 错误为 0，三张截图已目视核对。

外部账号浏览器测试使用两个真实 Identity 本地账号、Integration SQLite、Agent Web 组合和 OAuth 协议夹具。9 个步骤验证个人连接隔离、回调身份变化、拒绝回执、丢响应恢复、列表权限撤销、连接撤销、最小 scope 和完整模块重开。Provider 端共发生 3 次授权码交换和 1 次允许的连接探测；拒绝、身份变化与 scope 不足没有增加对应 Provider I/O。页面没有敏感 referrer，JavaScript 错误为 0，390px 弹窗无横向溢出，六张截图已目视核对。

浏览器结构化结果和截图分别位于[身份撤销证据](evidence/2026-09-13-h07-isolation/browser-authorization/report.json)与[连接撤销证据](evidence/2026-09-13-h07-isolation/browser-accounts/report.json)。

## 验证结果

- H07 集成 race 矩阵 8 个顶层场景连续 3 轮通过，包耗时 31.456 秒。覆盖每个模型边界、恢复、确认后继续、连接状态二次检查、后台 callback 重新授权和 Module／SaaS 跨工作区读取。
- 带 `external_identity` 的真实 Web race 矩阵 5 个顶层场景连续 3 轮通过，包耗时 344.521 秒。覆盖两用户、外部身份重复验证、账号与宿主重开、等待确认撤权和后台任务恢复。
- 编译产品身份撤销 Chrome 验收及其 race 宿主通过，宿主耗时 47.221 秒，4 步，JavaScript 错误 0。
- 编译产品外部账号 Chrome 验收及其 race 宿主通过，宿主耗时 35.516 秒，9 步，JavaScript 错误 0、敏感 referrer 0。
- Agent `GOWORK=off go test ./...`、`go vet ./...` 和 `go build ./...` 通过；前端 61 项状态测试与生产构建通过。
- Agent 架构套件 `GOWORK=off go test -race ./internal/architecture -count=3` 通过，耗时 1.437 秒。

原始日志、报告和截图位于[H07 证据目录](evidence/2026-09-13-h07-isolation/)，机器清单见[结构化证据](evidence/2026-09-13-h07-isolation.json)，职责边界见[架构审计](evidence/2026-09-13-h07-isolation/architecture-audit.json)。

## 架构边界

Agent application 只编排会话归属、冻结输入和持久运行状态，并通过 Agent SDK 的 authorizer／tool host 端口请求当前授权和连接可用性。Identity 独占用户、角色、permission、session 和 workspace 身份事实；Integration 独占 OAuth application、authorization session、token、connection account、scope、探测和撤销状态。Web composition 是唯一把这些公开边界装配在一起的位置。前端只调用 Agent／Integration 的公开 HTTP 路由并投影服务端状态。

架构 race 门禁确认 application 不导入 adapter、持久化实现或其他服务实现，持久化实现保持 `internal`，Module 使用发布标签，执行层不依赖账号业务协议。源码扫描没有发现 `internal/application` 对 Identity／Integration 的 `internal`、`module` 或 `saas` 实现依赖。Agent 数据库的 0 张 Identity 表提供了运行时证据：隔离通过 owner 边界完成，没有把 Identity 数据模型复制到 Agent。

## 限制

Identity、Agent、Integration、HTTP、SQLite、worker 与编译产品均为实际实现；模型回复和外部 OAuth Provider 为受控协议夹具。当前没有 Google Workspace 或 Microsoft 365 外部配置，用户已明确配置由具体服务自行负责，因此本项不宣称真实厂商 OAuth 验收。实际启用的模型协议、厂商账号和目标部署模式按顺序留在 H08。

`llm-proxy` 未修改，也没有接成模型服务；Agent 只消费其现有 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina` 工具接口。本项没有触碰这两个接口。
