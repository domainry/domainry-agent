# H06 故障恢复与外部写防重放验收（2026-09-13）

H06 已完成。工具执行前后中断、写调用超时结果不明、重复确认、取消、SSE 断线重连和完整宿主重启均以当前发布依赖完成验收。所有外部写场景都保存稳定幂等键；已完成回执直接复用，结果不明只走 owner 的核查端口，没有再次提交写操作。

## 本次补齐的缺口

此前已有丢响应和取消恢复验证，但缺少“写工具达到 Agent 外部调用上限”这一条明确断言。本次新增 `TestConversationWriteTimeoutReconcilesAfterRestartWithoutRepeatingEffect`：测试 owner 先持久化实际效果，再等待调用 context 超时；Agent 将该写调用保存为 `external_result_unknown` 和 `needs_reconciliation`。关闭并重开服务后，明确恢复只调用 `ReconcileConversationTool`，沿用原幂等键取得 `created-after-timeout` 回执。普通测试重复 10 次、H06 `-race` 矩阵重复 3 次均通过，每轮 `Invoke=1`、`Reconcile=1`。

本次没有修改生产实现。新增测试只通过 Agent SDK 的 `ConversationToolHost` 和现有持久化端口注入故障，不从 application 访问 HTTP、SQLite 或其他 owner 实现。

## 故障矩阵

| 场景 | 实际结果 |
| --- | --- |
| 工具执行前中断 | worker 在模型阶段收到关闭信号，已提交草稿和冻结模型输入保留；租约接管后 attempt 2 使用逐字段相同输入，新写入的记忆没有混入旧运行。工具效果尚未开始。 |
| 工具完成后中断 | 写回执已保存、下一次模型调用失败后关闭并重开服务；恢复复用原工具结果，实际写只调用一次，token 用量没有重复累计。 |
| 外部已写、响应丢失 | 实际 loopback HTTP owner 用文件落盘副作用后断开连接。Agent 保存结果不明；完整 Identity／Agent 宿主重启后以 GET 和原幂等键核查。连续三轮均为每个键一次 POST，未知项一次 GET。 |
| 写调用超时 | 新增专项让 owner 在效果提交后等待 50 ms Agent 上限。公开状态为待核查，审计保留 `external_result_unknown`；重启后一次核查完成，未再次调用写入口。 |
| 重复确认 | 同一个 interaction、client ID 和 revision 并发提交 8 次，随后再提交迟到重复请求；只产生一个写调用。结果不明后只核查一次，确认记录不被核查 interaction 覆盖。真实 Identity HTTP 路由的重复响应同样返回原结果。 |
| 取消与迟到回执 | 三个调用处于完成、在途、未开始时取消：完成结果保留，在途调用保存真实迟到回执或结果不明，第三项不执行；恢复时前两项不重写，未知项只核查。取消 API 返回与新 attempt 并发时，旧取消信号不能停止新 attempt。 |
| SSE 断线重连 | Module 与 SaaS 两种公开 HTTP 组合都在首个持久化 delta 后断开客户端；durable run 继续完成。携带 `Last-Event-ID` 重连只返回游标后的 `run.completed`，不重复先前文字；取消另一个流时上游模型 HTTP context 被关闭且草稿不进入历史。 |
| 服务重启 | 冻结输入、完成回执、结果不明 interaction、取消状态和 SSE 事件均从 SQLite 恢复；恢复动作沿原 Run ID、step、call ID、幂等键和授权边界继续。 |

## HTTP 与浏览器端到端

真实 Identity、Agent HTTP、SQLite、worker、官方 Knowledge Connector 和编译后的产品页面在 `127.0.0.1:8092` 运行；模型与知识正文为受控协议夹具。浏览器先得到一个精确计算结果，第二个知识 HTTP 请求保持在途，第三个计算尚未开始，然后点击“停止生成”。

- 取消前同一 Run 的调用状态为 `completed / running / queued`。
- 取消和真实 HTTP context 收尾后为 `completed / failed / not_started`，只有用户消息，没有保存取消后的 assistant 回复。
- 页面刷新后状态和事件序号不变；重复取消返回原事件序号。
- 完整关闭并重开宿主后仍为取消状态。用户明确点击“继续处理”后，同一 Run 进入 attempt 2，仅执行第三项并保存唯一正式回复。
- 浏览器报告中的 JavaScript 错误为 0；宿主记录模型 HTTP 11 次、知识 HTTP 26 次，其中官方 Connector 在途请求实际停止 1 次。四张截图已目视核对。

本次浏览器 Run 为 `crun_908b8f6e9b0f4897c067540da2486087`。结构化快照和截图见[浏览器证据](evidence/2026-09-13-h06-recovery/browser/report.json)。

## 验证结果

- H06 集成矩阵 `GOWORK=off go test -race ./integration ... -count=3` 通过，8 个顶层场景每轮通过，包耗时 15.889 秒。
- Identity HTTP／外部落盘／确认与完整宿主重启 `-race -count=3` 通过，Web 包耗时 47.461 秒。
- 编译页面浏览器验收及其真实 Web 宿主 race 通过，宿主耗时 90.641 秒。
- Agent `GOWORK=off go test ./...` 通过，integration 包 80.771 秒；`go vet ./...` 和 `go build ./...` 通过。
- 前端 61 项状态测试、TypeScript 检查和生产构建通过。
- Agent 架构套件 `GOWORK=off go test -race ./internal/architecture -count=3` 通过，确认 application 仍不导入适配器、持久化实现或其他服务实现。

原始日志、截图和结构化结果位于 [H06 证据目录](evidence/2026-09-13-h06-recovery/)。机器清单见[结构化证据](evidence/2026-09-13-h06-recovery.json)，边界见[架构审计](evidence/2026-09-13-h06-recovery/architecture-audit.json)。

## 限制

外部落盘服务和模型是可控故障夹具，用来证明精确传输故障、幂等键和恢复行为，不冒充真实第三方厂商。实际 Google Workspace／Microsoft 365 账号没有配置，真实厂商故障与目标部署留在 H08。H07 的双用户、跨工作区、连接失效和后台重新授权不由本项结论代替。
