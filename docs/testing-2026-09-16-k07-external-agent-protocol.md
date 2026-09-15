# K07 外部平权 Agent 协议验收

日期：2026-09-16。本轮把其他进程或服务中的 Agent 接入现有平权协作模型。K07 验收完成后，完整清单进度为 **22／25**。

## 产品与 owner 边界

- 外部 Agent 仍是普通 Agent 配置，可以被目录发现、共享和直接委派；系统没有新增父子层级。
- Agent SDK 拥有公开协议、能力和载荷契约；Agent 服务拥有当前身份授权、委派状态机、事件账本、交付及页面投影。远端执行器拥有自己的模型、工具和进程，但不能用这些声明取得 Domainry 本地工具或业务权限。
- 配置只保存 `domainry-peer` v1 和五项协商能力，不保存远端 endpoint、凭证、模型或工具。协议调用沿用产品已有 Identity 身份，服务端再与委派冻结的执行主体核对。
- 外部任务不进入本地 worker。队列扫描分页跳过全部外部任务，避免大量外部任务遮住后面的本地任务；外部 assignment 查询同样遍历完整当前主体队列后再分页返回匹配项。

## 协议与恢复

公开接口分为 assignment query、claim 和 report。认领时 Agent ID、任务、执行主体、协议版本和五项能力必须完全一致；生成稳定外部 session，重复 `client_id` 返回原回执。报告通过 `expected_last_event_seq` 和连续事件序号防止丢失、乱序及重复追加，每批最多 16 项，页面投影保留最近 128 项并明确是否完整。

插话继续使用现有 Agent 消息并由外部 report 确认消费。取消／暂停先形成 stop request，外部 Agent 必须用取消事件确认 `none`、`known` 或 `unknown` 效果，服务端才允许按能力恢复到新 attempt。没有执行详情能力的协议只能报告有界状态摘要，工具、详情和用量会被拒绝；页面明确提示没有过程详情，不把最终文本当作轨迹。

结构化交付仍使用现有 `deliver` 动作并绑定当前 brief、agreement 与 Schema。外部 `completed` 事件在正式交付之前会冲突；交付完成后任务才可完成，委派方继续通过原验收和分歧流程决定是否接收。

## 真实链路

- SQLite 测试覆盖认领、能力不匹配、事件游标、消息确认、报告幂等、先交付后完成、停止确认后恢复，以及本地 worker 隔离。
- 双用户网页测试由接收方创建并共享外部 Agent，委派方直接委派；错误用户不能查看或认领，接收方能报告过程和结构化交付，委派方能查看过程并完成验收。
- Module HTTP Adapter 与 SaaS remote binding 使用同一 SDK 定义。SaaS 测试证明 execution authority、五项能力、session、事件游标和消息 ID 原样穿过服务端，错误 workspace 在到达业务服务前被拒绝。
- 前端显示协议、attempt、session、能力、过程事件、截断、停止确认和未知效果；无详情能力有独立文案。外部 Agent 不能作为普通前台会话启动，必须走可管理的委派链。

## 与 DeepSeek Harness 当前源码对照

对照固定到官方 `master` 的 `0d1f50007f9bca3f52b06e1c3074fa14d5fb0720`：

- DSH 的通用 ACP 自动化服务支持持久 Session、模型／推理配置、MCP、取消和标准语义更新，适合受信控制器；它有意不提供 DSH 页面、计划、终端、客户端文件系统或 elicitation。
- DSH 的 `subagent-acp` 委派后端每次运行启动全新子进程，隔离自己的 Session、模型和工具，只共享本地 cwd。父级只收到最终已提交文本或安全错误；中间消息、工具流和计划留在子 Session。
- 该委派后端不声明可选启动能力，所以结构化输出、工具过滤、persona、depth cap 等请求会被拒绝；权限提示按配置自动允许或拒绝，当前也没有进程池、远端 workspace 映射或可继续的 ACP child。
- Domainry K07 面向跨进程平权业务协作：执行过程作为受控事件投影，双向消息、停止效果确认、正式结构化交付和验收属于同一委派。它不是完整 ACP 实现；需要接 ACP 时由适配器把 ACP 能力和更新映射到此公开协议，不能宣称 ACP 未提供的过程或控制能力。

官方依据：[DSH ACP 委派后端](https://github.com/deepseek-ai/deepseek-harness/blob/0d1f50007f9bca3f52b06e1c3074fa14d5fb0720/packages/subagent/subagent-acp/README.md)、[DSH ACP 自动化服务](https://github.com/deepseek-ai/deepseek-harness/blob/0d1f50007f9bca3f52b06e1c3074fa14d5fb0720/packages/acp/acp/README.md)。

## 验收结果

- Agent SDK 全包通过，路由、OpenAPI、能力与序列化契约完整。
- Agent application、SQLite、Module Adapter、SaaS server／remote 和真实双用户 HTTP 测试通过。
- 前端 94 项状态测试与生产构建通过。
- K07 涉及源码 `git diff --check` 无错误，证据清单与源码 SHA-256 可复核。

原始命令、行为摘要、源码哈希和证据文件哈希见 [`docs/evidence/2026-09-16-k07-external-agent-protocol`](evidence/2026-09-16-k07-external-agent-protocol)。
