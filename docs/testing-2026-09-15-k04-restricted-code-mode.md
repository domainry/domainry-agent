# K04 受限 Code Mode 验收

日期：2026-09-15。本轮实现程序化工具组合和中间 JSON 加工，并保持 Agent、Runtime 与业务工具 owner 的边界。K04 验收完成后，完整清单进度为 **19／25**。

## 组件边界

- Agent SDK 定义固定 `run_code` 工具、请求／返回／失败协议和 `ConversationCodeRuntime` 端口。它是 Agent 控制面能力，不是普通业务工具。
- Agent 冻结本模型步骤的可用工具，只把 `run_code` 之外的定义交给 Runtime；代码中的每次绑定调用经同步 dispatch 返回 Agent。Agent 负责当前主体、来源、目录和定义复核、Schema、可用性、动作授权、用户确认、调用预算、回执、结果授权及生命周期事件。
- Runtime 实现独立执行端。生产组合启动同一发行程序的隐藏 worker 命令；worker 只有逐帧协议，没有 ToolHost、Identity、凭证、数据库或业务服务句柄。
- Tools／Integration／Connectors 等仓库继续拥有业务工具、连接和外部效果。Code Mode 没有复制这些实现，也没有新增绕过工具宿主的执行口。

## 执行限制

- 每次代码运行创建全新进程、全新临时工作目录和空环境，不复用前次状态。当前语言为 Lua。
- worker 只打开 base、table、string 和 math；移除文件／Shell／环境、网络、时钟、随机数、动态加载、字节码导出、模块加载、元表和 raw 操作。
- 源码最多 32 KiB；最多 64 个 dispatch；最终结果最多 48 KiB，外层工具回执最多 64 KiB；日志最多 128 条、每条 2 KiB、合计 8 KiB；JSON 转换限制深度和项目数。
- `run_code` 的 30 秒工具 timeout 与宿主更短的外部调用上限取较小值。超时由 Agent 形成 `code_timeout` 失败回执，Runtime 使用 Context 硬终止子进程。
- 程序必须返回恰好一个 JSON 兼容值。中间调用只在进程内使用；模型下一步只接收外层值、有界日志和 dispatch 数。

## 权限、预算和回执

权限没有交给 Runtime 判断。调用链为：

1. `run_code` 先通过 Agent 的当前个人动作授权，且只有配置允许并成功装配 Runtime 时才进入工具目录。
2. 每个 `tools.<key>(args)` 必须来自当前步骤冻结目录，参数必须是完整 JSON 并通过该工具的冻结 Schema。
3. Agent 为子调用生成由 run、父调用和 dispatch 序号决定的稳定 ID，先以 `queued` 保存父子关系、完整参数和调用预算占用。
4. 子调用进入普通 `executeConversationToolWithParent` 流程，重新检查执行 claim、来源、当前目录、定义摘要、工具可用性和具体 Action 授权。写操作需要原来的精确用户确认。
5. 获准后才把子账本改成 `started` 并调用原 ToolHost。完成、失败或未知结果继续写入 `_agent_run_steps` 的 `tool_call` 记录和事件账本；模型、页面、共享执行和历史轨迹复用现有结果授权。

调用预算统计顶层调用和 Code Mode 子调用。子调用预留在同一数据库事务中重新统计整轮顶层与嵌套调用，多个 worker 不能靠并发越过上限。运行审计将 `run_code` 和实际子调用分别计数。

## 恢复和页面

- 等待写确认时，子调用保持 `queued／waiting_confirmation`，外部效果为零。恢复会重新运行无状态代码；相同序号只能匹配相同工具、参数和定义，已批准的子调用只执行一次。
- 写调用返回不确定结果时，子回执为 `uncertain／needs_reconciliation`。恢复再次运行程序，但同一子 ID 和幂等键使执行器调用 Reconcile；不会再次 Invoke。
- 普通代码错误、无返回值、非法返回、资源上限和 timeout 形成明确失败结果。读取工具的确定失败可作为程序可见状态处理；未知写入不会被降级为普通失败。
- 取消终止受限进程。存储按每个子调用保留 completed、not_started、interrupted 或 uncertain，迟到回执继续受原 lease／fence 限制。
- SSE 和运行快照使用 `parent_call_id`、`dispatch_index` 和 `subcalls`。页面在“运行受限代码”下展示“代码内调用”、参数、状态、结果、错误和回执入口；共享与撤权按子调用递归处理。

## 与 DeepSeek Harness 的对应关系

2026-09-15 核对的官方 HEAD 为 `0d1f50007f9bca3f52b06e1c3074fa14d5fb0720`。其 [Tools Code Mode](https://github.com/deepseek-ai/deepseek-harness/blob/0d1f50007f9bca3f52b06e1c3074fa14d5fb0720/packages/core/tools/README.md) 同样由工具注册表提供 `run_code`，每个 binding 回到完整工具 pipeline；[Code Runtime](https://github.com/deepseek-ai/deepseek-harness/blob/0d1f50007f9bca3f52b06e1c3074fa14d5fb0720/packages/code-runtime/code-runtime/README.md) 也不拥有工具和会话。

当前 Domainry 实现覆盖 K04 所需的隔离、组合、授权、预算、回执、取消和未知效果恢复，并把子调用持久化到原执行账本。与 Harness 当前实现相比，Domainry 只提供 Lua、顺序子调用和 native＋code 同时展示；Harness 另有 TypeScript、实验 Python、按语言生成的类型 SDK、native／code／both 模式和安全读取并发。Domainry 当前额外限制中间 JSON 的深度和项目数，并逐次重查保存结果和来源权限。语言化类型 SDK、独立展示模式与读取并发属于可测的模型效率优化，不影响本轮安全闭环。

## 验收结果

- 四条 Agent 真实链通过：程序调用读取工具并组合结果；嵌套写入在确认前零效果、确认后一次；未知嵌套写入恢复时一次 Invoke／一次 Reconcile 且幂等键一致；代码超时保存 `code_timeout` 并让模型收到失败回执。
- Runtime 使用真实子进程验证工具 dispatch、空 `os／io／require`、动态加载拒绝、缺少返回拒绝、dispatcher 等待错误原样传播及无限循环硬终止。
- Agent SDK 全包、Runtime coderuntime／runtimehost、Agent integration／application／persistence／module、前端 92 项测试和生产构建通过。
- Agent 的实际产品包全量运行中，除 `internal/assembly/web` 外全部通过。Web 整包在 10 分钟上限时运行与 K04 无关的 `TestKnowledgeDocumentTransfersIdentityHTTPProtocol`；Web 包独立编译通过，该场景单独运行 10.32 秒通过。根目录 `go test ./...` 还会扫描 `docs/evidence` 内故意不完整的历史源码快照，不作为产品包结果。
- Agent、Agent SDK 与 Runtime 的 K04 相关 diff 检查通过。

完整命令与日志见[验收证据](evidence/2026-09-15-k04-restricted-code-mode/commands.md)。
