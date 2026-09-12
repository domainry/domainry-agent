# G06 有界后台跟进完成验收（2026-09-12）

## 完成结论

G06 已完成。后台跟进是 Scheduler 每个触发窗口启动的一次普通 Agent 后台任务，不占用长期 worker。计划必须保存明确的 `completion_condition`；Agent 只接受严格、封闭的跟进报告，按 owner + plan 保存规范化 observation 摘要，并只在变化、完成、失败或需要用户操作时写出通知事实。首次成功运行只建立基线，同义 JSON 状态保持静默，完成状态为终态，已在途的过期窗口不能重新打开跟进或发出过期失败／确认通知。

用户可在“计划与提醒”里查看完成条件并点击“停止跟进”。停止复用 Scheduler 的 pause + revision CAS，重启后仍为暂停；Agent 没有自建调度循环或停止状态。

## 架构边界

- Tools SDK 定义 `follow_up` 计划输入，Tools 适配器只把用户目标、允许工具和完成条件映射为 Scheduler 公共计划；模型与浏览器不能指定 owner、Runtime target、Action、收件人、渠道或 Provider。
- Scheduler 继续独占时间、触发、计划 revision、暂停和恢复。每次触发只投递签名事实，不读取 Agent 状态或 Notification 表。
- Agent SDK 定义来源中立的跟进 scope、报告、事件和持久 outbox 端口。Agent 独占报告校验、变化判定、终态、任务结果及 at-least-once outbox；应用层未导入 Runtime、Scheduler、Notification 或 Tools 的实现包。
- Runtime 只有一处组合适配器把中立 Agent 事件映射为内建 `agent.follow_up.*` 通知类型，并在投递前重新解析当前 Identity。Notification 继续独占接收人、模板、偏好、站内 inbox、渠道、投递结果和去重。
- 启动使用两阶段绑定：Runtime 先提供当前授权与通知端口，Agent 模块成功装配后再把公开 scheduled-task service 绑定回 Runtime dispatcher。这样没有循环实现依赖，也不会在能力尚未就绪时领取任务。

机器审计见[架构审计](evidence/2026-09-12-g06-follow-up/architecture-audit.json)。`/Users/tiger/Projects/anti/llm-proxy` 保持干净；G06 没有接入模型代理，也没有修改其 web search／fetch 服务。

## 端到端行为

跨模块 E2E 使用真实 Runtime、Identity 测试身份、Agent 模块、Notification 模块、SQLite 和签名 Scheduler callback。模型仅为确定性跟进协议夹具，用于控制四次报告：

1. 第一次返回 `open=2, blocked=0`，只建立基线，站内通知为 0。
2. 完整关闭并重新打开 Runtime 与同一 SQLite 后，返回键顺序不同但语义相同的 JSON，通知仍为 0。
3. 返回 `open=1`，产生 `agent.follow_up.changed`。
4. 返回 `open=0` 且 completed，产生 `agent.follow_up.completed`；两次事件由 Notification 聚合为 occurrence count 2。

持久层专项另验证无界文本／未知字段报告失败、Provider 失败、需要用户操作、事件去重、发布失败释放、租约过期接管、稳定事件 ID、完成后的过期成功／失败保持静默。Runtime 输入适配拒绝 follow-up 内未知的收件人等权限字段。

编译后的生产页面通过真实 Identity HTTP、同一 Tools 适配器和 Scheduler SDK 服务完成四步浏览器验收：显示完成条件与稀疏通知规则；点击“停止跟进”得到 pause 200 和 revision 2；完整 Agent 宿主重启后仍为暂停；390px 下无横向溢出。JavaScript 错误、非预期控制台错误和警告均为 0。报告见[浏览器结果](evidence/2026-09-12-g06-follow-up/browser/report.json)，截图见[运行中跟进](evidence/2026-09-12-g06-follow-up/browser/follow-up-enabled.png)和[手机暂停状态](evidence/2026-09-12-g06-follow-up/browser/follow-up-mobile-paused.png)。

## 验证结果

- Agent 整库测试与 `go vet ./...` 通过；G06 持久层／应用／网页专项 race 通过。
- Agent SDK、Tools SDK、Tools 整库测试与 vet 通过；Tools schedule 适配器 race 通过。
- Runtime composition、transport、G06 integration E2E 与 FollowUp 专项 race 通过。E2E 2.34 秒，race integration 27.996 秒。
- 前端 59 项状态测试通过，生产构建通过；浏览器端到端最终 9.41 秒通过。
- Runtime 的完整 integrationtest 集合同时暴露下一项 H01 的既有审计契约失败：`TestRuntimeBusinessEventStreamConnectsReplaysAndRejectsCrossTenant` 尚未提供测试要求的顶层 workspace 审计证据。该失败不在 G06 跟进链路，原始输出保存在 `runtime-go-test.log` 和 `runtime-unrelated-rerun.log`，将在紧接的 H01 按顺序处理，不作为通过结果隐藏。

完整命令、日志和摘要见[机器清单](evidence/2026-09-12-g06-follow-up.json)。
