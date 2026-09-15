# K06 业务事件触发 Agent 验收

日期：2026-09-16。本轮把 Integration 已有的可信 Webhook 链路接入 Runtime 与 Agent 的普通后台任务。K06 验收完成后，完整清单进度为 **21／25**。

## owner 边界与事件契约

- Integration 继续拥有 Provider 验签、原始载荷、外部身份映射、事件持久化、租约、重试、死信和人工重放；Agent 不保存 Webhook 密钥或原始请求体。
- 事件映射新增 `agent_task` 目标，部署定义必须给出有限的 Agent、会话和 `start`／`wake` 模式。输入只能引用已声明事件字段；映射内容生成稳定 SHA-256 revision。
- Runtime 只接收验签后且经过规则选择的中立事件，解析映射输入并把 Integration 外部身份解析成当前 Identity 用户／角色。Agent 目标不允许退回系统主体，也不能从事件载荷自报用户。
- Runtime 通过只供 Runtime 使用的明确 service action 调用 Agent。Agent 仍按当前 Identity 核对会话执行权、会话绑定的准确 Agent、当前 Agent 版本、模型、工具目录和逐项工具授权。

## 创建、唤醒与恢复

`start` 创建普通排队任务；事件 ID、Provider、类型、外部 ID、接收时间、规则 key／revision、当前执行用户／角色、目标 Agent 和幂等键随任务冻结。相同所有者与幂等键的准确重投返回原任务；内容变化的重投拒绝，不能覆盖旧任务。

`wake` 仅能引用同一会话、同一 Agent 的终态任务，并新建不可变后继任务。活动任务不能被并发唤醒，旧运行和已发生效果不会原地重放。模型提示把事件字段标为来源与关联信息，不视为授权，并明确要求检查原回执、保留且不重复旧效果。

Integration 在 Runtime 调用失败后沿用既有有界退避，累计五次进入死信；事件、映射意图和 Runtime／Agent 任务回执均持久保存。重试时稳定幂等键落到同一个 Agent 任务，进程重启或人工重放不会重复创建任务。

## 页面与真实链路

任务详情显示业务事件来源、外部 ID、规则 revision、当前执行用户／角色、目标 Agent、模式及关联任务。真实 Identity／HTTP／SQLite／worker 测试验证：浏览器当前账号创建 Agent 与会话；缺 Runtime service action 的直接调用被拒绝；正确动作创建并完成任务；重复事件命中原任务；HTTP 详情和模型提示包含准确来源；终态任务由后续事件创建独立 wake 后继；错误 Agent 目标被拒绝。

## 与 DeepSeek Harness 的对应关系

2026-09-16 核对的官方 HEAD 为 `0d1f50007f9bca3f52b06e1c3074fa14d5fb0720`。其 [Webhook runtime](https://github.com/deepseek-ai/deepseek-harness/blob/0d1f50007f9bca3f52b06e1c3074fa14d5fb0720/packages/webhook/webhook/src/index.ts)把经过 Provider 验证的 delivery 交给受信规则，并可创建普通根 Session；[边界说明](https://github.com/deepseek-ai/deepseek-harness/blob/0d1f50007f9bca3f52b06e1c3074fa14d5fb0720/docs/subsystems/webhook.md)明确它是进程内 fire-and-forget，不保存队列、重试、去重、崩溃重放或 Agent 完成状态，重复投递可创建重复 Session。

Domainry 本轮也把事件落成普通 Agent 任务，同时复用 Integration 的持久事件队列、去重、租约、失败恢复、死信和人工重放；规则目标在 manifest 中绑定有限 Agent／会话，执行身份来自当前 Identity，wake 使用不可变后继任务。当前只接入已注册 Integration Provider／Connector 的 JSON 业务事件；新增 Provider 仍需在对应 owner 中实现验签和规范化。

## 验收结果

- Integration SDK 全包、Integration 事件存储／映射／HTTP、Agent SDK 全包、Agent 应用／存储／远端／服务端／模块和真实 Web 链路通过。
- Runtime 服务装配、事件路由、manifest 和 AppSchema 聚焦包通过；前端 93 项测试与生产构建通过。
- 五个改动仓库的 K06 文件 `git diff --check` 通过。
- Integration 单仓默认 `go.mod` 仍指向已发布旧版 Integration SDK／Connector SDK；使用仓库现有联合 `go.work` 的当前源码验证通过。发布时需先发布 SDK 依赖再更新单仓版本锁。

完整命令与记录见[验收证据](evidence/2026-09-16-k06-business-event-trigger/commands.md)。
