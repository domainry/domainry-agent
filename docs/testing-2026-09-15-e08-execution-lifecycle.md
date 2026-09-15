# E08 执行生命周期扩展接口验收

日期：2026-09-15。本轮在现有 Go SDK、Conversation Service 和延迟宿主装配边界上补齐静态执行生命周期扩展。输入准入、上下文装配与压缩、模型请求与恢复、工具执行前后以及运行／任务终态都有公开阶段；规划、压缩、重试和观察可以独立组合。E08 验收完成后，完整清单进度为 **14／25**。

## 公开契约与装配

- [SDK 生命周期契约](../../domainry-agent-sdk/conversation_lifecycle.go)定义 `input.admitting`／`input.received`、上下文、模型、工具和终态阶段。策略只能返回阶段限定的规划、上下文上限或重试决定；观察器不能返回决定。
- 每个扩展声明稳定 key、实现版本、非敏感配置版本、顺序、类型、失败模式和订阅阶段。服务启动时校验并按 `order + key` 固定顺序，最多接入 64 个扩展；运行和后台任务保存相同的有序 manifest 与摘要。恢复时 manifest 不一致会以 `lifecycle_changed` 停止，旧运行不会静默采用新配置。
- [宿主端口](../../domainry-agent-sdk/modulehost/conversation.go)通过已有延迟装配一次性提供扩展；[模块装配](../module/conversation_assembly.go)在发布 Conversation Service 和启动 worker 前完成注入。运行时安装、卸载和版本迁移不属于当前契约。
- 固定对照的 [DeepSeek Harness Agent 循环](https://github.com/deepseek-ai/deepseek-harness/blob/c291e7961a515f6d7af9304e7fd1d257929aef26/packages/core/agent-loop/src/agent.ts)提供 `agent/pre-step`、`agent/request`、`agent/request-error`、`agent/turn-stopping` 及状态／错误／流事件。本实现把类似 waterfall 的可变部分收窄为三个显式决定，并补上运行冻结、配置版本和工具回执边界。

## 顺序、失败与恢复

- `input.admitting` 在当前协作、Identity、执行授权和 Agent 冻结完成后、写入队列前运行，可用 fail-closed 拒绝输入；`input.received` 只在原子入队成功后发出，属于不能回滚的 continue 阶段，并带实际 Run ID。
- 上下文策略在模型输入首次持久化前追加有界可信规划指导，保持所有来源消息索引不变；压缩策略只能降低宿主上下文上限。该上限写入文本请求或每个工具步骤的冻结 ContextWindow，恢复不会重新放宽。
- `model.request` 在持久化请求尝试后、外部模型调用前发出；`model.failed` 可以关闭重试、降低最大次数或增加受宿主上限约束的等待。失败和计划重试先持久化，再发 `model.retry`；成功尝试先持久化完成，再发 `model.completed`。
- fail-closed 只允许出现在效果发生前。已收到输入、模型完成／重试、工具完成／失败和运行／任务结束均强制 continue，避免回调否定已提交的状态。观察器一律 continue；错误和 panic 对外只形成稳定 `lifecycle_extension_failed`，不泄露扩展原始错误。
- 同一恢复边界可能再次发送相同 Event ID。扩展必须按 Event ID 保持确定性或去重；本轮没有为观察器另建持久 outbox，因此进程在已提交状态之后立即崩溃时，不保证观察回调一定送达。

## 授权、工具与数据隔离

- `tool.before` 位于当前运行／来源／目录／具体动作授权、参数结构、人工确认和实时可用性检查之后，并位于执行预留和外部调用之前。它不能修改调用、定义或授权；返回不属于该阶段的决定会按扩展失败处理。
- `tool.completed`／`tool.failed` 只在准确结果或不确定结果已经保存为原回执之后发送。回放已完成结果时仍重新核对当前授权、保存资源和嵌套来源；生命周期接口不能跳过这些检查。
- 每个处理器收到独立深复制的事件、manifest、模型／步骤请求、工具参数和结果。扩展保留或修改指针不会改变持久请求、实际工具调用或保存回执；不可序列化的事件不会降级为空事件。
- 公开事件只携带当前 Authority 标识、冻结定义与执行数据，不携带具体授权证据、确认凭据或工具宿主凭据。扩展是宿主选择的可信进程内代码，但不成为新的权限判定源。

## 回归结果

- SDK 全量、除历史证据目录外的 34 个实际产品包、完整 Integration 和完整 Web 包全部通过；Web 为 522.725 秒，Integration 为 93.012 秒。
- E08 race 覆盖 manifest 顺序与变更拒绝、失败边界、输入准入、规划／压缩组合、模型重试收紧、工具授权与回执顺序、事件副本隔离、任务终态和模块宿主装配，全部通过。
- Integration 验证工具型两次模型请求都先持久化为 `completed` 再观察；输入 fail-closed 不产生消息或模型调用；观察器故障不改变执行结果。前端 91 项测试和 TypeScript／Vite 生产构建通过。

命令、日志、源码摘要和结构化结论见[验收证据](evidence/2026-09-15-e08-execution-lifecycle/commands.md)。
