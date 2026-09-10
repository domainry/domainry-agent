# 业务动作实现与验证记录

更新日期：2026-09-10。对应 TODO J04。**实际后端、真实模型整段流程与网页复验均已通过，J04 已勾选。**

## 实现位置

- SDK：相邻 `domainry-agent-sdk/conversation_business_actions.go` 定义可选 `ConversationBusinessActionSource`、具体动作参数、可信调用上下文和执行回执，以及 `invoke_action` 工具。
- Agent：[会话动作入口](../internal/application/conversation_business_actions.go) 校验当前权限、具体动作契约和保存的确认记录；确认绑定目标、冻结参数、操作者和工具定义。个人记忆 / 待办 / 成果授权不代替业务操作确认。
- Runtime：相邻代码库 `runtime/application/agenthost/conversation_business_actions.go` 调用实际 Action 应用服务，沿用业务权限、输入校验、并发版本和持久执行回执。核查未知效果时不重新领取已有未完成执行。
- 网页：[操作预览](../frontend/src/BusinessOperationPreview.tsx) 展示实际动作、对象、记录与修改内容；[交互卡片](../frontend/src/InteractionCard.tsx) 处理确认。

模型只提交已发布动作的业务参数。身份、确认凭据和幂等键由服务端提供。动作返回实际执行回执与记录引用；读取修改后的字段继续经过业务读取权限。

## 已通过的验证

| 验证范围 | 结果与证据 |
| --- | --- |
| 实际 Identity / Runtime / Agent HTTP、SQLite；模型为夹具 | `TestConversationBusinessActionsThroughIdentityWebAndRestart` 通过，18.72 秒。创建、更新、状态转换、确认前完整重启、重复确认、并发更新冲突、撤权 / 恢复及历史回复复核。日志 `/tmp/domainry-runtime-actions-http-9.log`。 |
| Agent 会话动作集成 | [专项测试](../integration/conversation_business_actions_integration_test.go) 验证重启、并发重复确认、未知效果核查、权限 / 契约变化、拒绝与无效参数反馈。日志 `/tmp/domainry-agent-actions-new.log`。 |
| Agent、SDK 全量 | `/tmp/domainry-agent-actions-full.log`、`/tmp/domainry-sdk-actions-full.log` 均通过。 |
| Runtime 相关回归 | agenthost、action 应用服务、action runtime、动作存储及业务 HTTP 相关测试通过；日志 `/tmp/domainry-runtime-actions-regression.log`。这是相关包及名称筛选测试，不能等同 Runtime 全库测试。 |
| 相关并发检查 | Agent 动作集成及 Runtime 宿主 / 回执 / 动作存储 race 检查通过，日志 `/tmp/domainry-agent-actions-race.log`、`/tmp/domainry-runtime-actions-race.log`。 |
| 前端状态与构建 | 19 项状态测试全部通过；构建通过，仍有 bundle 大小提示。日志 `/tmp/domainry-agent-actions-front-tests.log`、`/tmp/domainry-agent-actions-frontend-final.log`。 |

## 真实模型与网页整段验收

真实模型使用 Verdent `gpt-5.6-sol` / Responses，数据为临时合成客户；真实调用模型与实际 Runtime 业务服务，凭证没有保存到测试文件。

1. 首次运行：真实模型创建 `customer.register` 已通过实际记录校验；后续等待确认时，测试每 10 毫秒轮询触发工作区每分钟 600 次请求限制，返回 `429 capacity.workspace_rate_exceeded`。整次测试失败，日志 `/tmp/domainry-runtime-actions-live.log`。
2. 修正：真实模型验收改为每秒轮询，保留宿主实际限流规则与四分钟等待上限。
3. 复跑：首次模型调用返回 `provider_failed`，测试失败，未执行创建。公开错误不足以确定具体上游原因，不能归因于凭证、余额或协议。日志 `/tmp/domainry-runtime-actions-live-2.log`。
4. 新增仅记录 HTTP 状态与耗时的测试诊断后再次运行：模型 HTTP 均为 200，创建、更新、停用 3 阶段、22 次工具调用全部通过，122.30 秒，日志 `/tmp/domainry-runtime-actions-live-3.log`。每阶段核对实际数据库和准确目标，确认前及全部操作后重启，验证没有重复业务效果。此前 `provider_failed` 未复现，不推断已修复某个未知上游问题。

实际模型操作同一记录 `customer_1789029633085417000`：`Agent Live Created / active` → `Agent Live Renamed / active` → `Agent Live Renamed / inactive`。执行证据保存于临时合成资料目录 `/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-business-agent-fk6wyw1p/business-actions-live.json`。

此前实际浏览器已操作同一客户的创建、改名和停用，检查确认前刷新 / 宿主重启、权限撤销隐藏与恢复。服务、Identity、HTTP 和数据库为实际模块，模型为夹具。测试收尾已确认数据库仅新增一条且名称 / 状态正确，随后把保留的已答复确认记录误判为待确认而失败。断言已改为仅拒绝仍为 `pending` 的记录；修正后已完整复跑，`TestConversationBusinessActionsBrowser` **通过，297.90 秒**；日志 `/tmp/domainry-runtime-actions-browser-2.log`。本轮通过页面创建 / 改名 / 停用实际记录 `customer_1789029684438037000`，确认前刷新和完整重启后参数保持一致；撤权后两条历史业务回复隐藏，权限恢复后各自原有状态（active / inactive）重新显示。测试结束后实际数据库校验通过：总数为初始三条加一条新客户，目标名称 `Agent Renamed`、状态 `inactive`，确认记录没有仍待答复。测试宿主和页面已关闭。

## 复验入口

在 Agent 目录运行，脚本使用相邻本地代码库组成临时 go.work 与临时合成数据库：

```sh
python3 scripts/test-agent-business.py --actions
python3 scripts/test-agent-business.py --actions --browser
python3 scripts/test-agent-business.py --actions --live --browser --model gpt-5.6-sol --protocol responses
```

真实模型命令从已有非敏感配置读取服务地址，在环境未提供凭证时通过隐藏终端输入。浏览器验收需已有前端构建，并由测试服务提供页面及结束入口。

J05 流程启动、C04 业务操作授权范围、外部服务适配和发布依赖锁仍是各自未完成事项；本批后端验证不能替代这些交付。
