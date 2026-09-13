# C04 完成条件逐项核对验收记录

日期：2026-09-13。范围：当前部署、同一用户授权范围内的平权委派。本批补齐 C04 的完成条件核对链路；结构化分歧尚未实现，C04 保持未勾选，完整条目仍为 3／25。

## 实际行为

- `Brief.verification_rules` 随要求版本保存，以零起始条件序号声明数据 JSON Schema 或原操作回执检查。回执可约束工具、参数、结果、数量和所需状态；同一幂等操作的复用回执不能重复计数。程序检查采用真实保存的数据和回执，提交者的 `met` 不能覆盖失败结果。
- 其余条件由发起方 Agent 或用户记录 `met / unmet / unknown` 与依据。接收方 `delivery.conditions` 只是自评，不能自我验收。页面默认不把接收方的“满足”复制成验收方意见。
- `review_delivery` 保存核对而不改变交付验收状态或停止 Task；`accept_delivery` 明确绑定交付摘要，重新核对条件、未解决事项、当前需求和依赖版本，并要求接收方 Task 已完成、各次接单旧运行已停止、外部写入结果已明确。
- 只有发起方 Agent 或用户可以评估；Agent 来源会话由服务端识别，持久记录真实评估 Agent 和运行边界。无关 Agent 不能借持有协作工具替其他委派验收。
- 原工具结果在模型消息正文中附带服务端生成的 `reference`，小结果和压缩结果均可提供准确引用；原账本结果与摘要不改变。HTTP 夹具的模型只解析实际消息正文，不读取内部 `ResultReference` 字段来构造证据。
- 只有终态、摘要匹配的原回执可作为不可变引用。引用仍须经过当前来源权限校验；结果尚不明确时继续走此前实现的原操作核查。`accepted` 受理回执不能证明业务已经完成。
- 新增 migration 23 `_agent_delegation_deliveries`，按委派修订保存交付、核对和验收的不可变记录。改正交付不会覆盖旧失败记录。旧交付在首次后续写入时保留为未核对历史，不虚构历史验收来源。
- 页面支持检查方式、逐项意见、原回执链接、核对来源与交付历史。修改条件文字会移除对应旧规则；唯一且未变化的条件可在调整顺序时保留规则。刷新和窄屏访问已验证。

数据 Schema 检查证明提交数据符合约定；它本身不证明外部业务事实。对于需要判断的要求，保留明确的 Agent 评估或用户决定。

## 代码入口

| 层次 | 当前实现 |
| --- | --- |
| SDK | [核对契约](../../domainry-agent-sdk/conversation_verification.go)、[协作工具](../../domainry-agent-sdk/conversation_collaboration_tools.go)、交付历史 HTTP／RPC 与 remote 适配 |
| 核对规则 | [逐项评估](../internal/execution/verification.go)、[要求字段投影](../internal/execution/brief.go) |
| 应用 | [来源核对与历史读取](../internal/application/conversation_verification.go)、[委派管理](../internal/application/conversation_delegations.go)、[模型可见回执引用](../internal/application/conversation_execution.go) |
| 持久化 | [核对与不可变历史](../internal/infrastructure/persistence/database/agent/conversation_verification_store.go)、[状态转换](../internal/infrastructure/persistence/database/agent/conversation_delegation_store.go)、[迁移](../internal/infrastructure/persistence/database/agent/conversation_verification_schema.go) |
| 页面 | [核对与历史](../frontend/src/DeliveryVerification.tsx)、[委派表单](../frontend/src/CollaborationDialog.tsx)、[条件与规则状态](../frontend/src/collaboration-state.ts) |

## 验证证据

所有模型、Identity 数据库和浏览器场景均为隔离测试夹具，没有连接生产业务系统，也没有用模型真实任务质量冒充接口验收。

- 领域测试覆盖数据失败、受理与完成区分、未知结果、明确失败、参数期间不匹配、复用回执重复计数、缺少依据和未解决事项。
- 存储测试通过真实账本核对结果摘要，拒绝伪造及跨用户引用、无关 Agent 评估、缺少核对、交付版本替换、覆盖历史和用主观结论覆盖程序失败。修正后的新交付可以验收，原失败记录保留。
- 应用测试复核历史中的评估来源和每条回执来源。来源权限撤回后保留委派元数据，但隐藏交付和核对细节；历史读取拒绝访问。
- HTTP 场景运行独立接收方 Agent，提交数据与真实 `time_now` 回执；发起方通过 `delegation_get` 和 `review_delivery` 保存有来源的 Agent 评估，用户再显式验收。重开同一 SQLite 数据库后，验收状态与全部四条交付／核对历史保持一致；权限撤回仍立即生效。
- [Chrome 报告](evidence/2026-09-13-c04-verification/report.json)覆盖声明数据和回执规则、逐项核对、单独保存核对、验收、刷新后查看历史、需求／依赖更新与窄屏。[桌面核对](evidence/2026-09-13-c04-verification/peer-verification.png)、[历史](evidence/2026-09-13-c04-verification/peer-verification-history.png)、[窄屏](evidence/2026-09-13-c04-verification/peer-verification-mobile.png)均来自真实 Chrome 操作并已查看。
- [核心与 SDK／HTTP 测试](evidence/2026-09-13-c04-verification/core-sdk-http-final.log)、[68 项前端测试](evidence/2026-09-13-c04-verification/frontend-tests.log)、[前端构建](evidence/2026-09-13-c04-verification/frontend-build.log)和[完整 HTTP／浏览器场景](evidence/2026-09-13-c04-verification/browser-http-final.log)通过。[应用、持久化与完整 HTTP 的 race 检测](evidence/2026-09-13-c04-verification/race-final.log)也通过。构建仍有既有 Vite 大 chunk 提示。

命令、失败记录说明与源码摘要见[证据目录](evidence/2026-09-13-c04-verification/commands.md)。后续代码继续演进时，以摘要区分本批验收范围。

## 仍需继续

C04 还需结构化结论分歧、证据对照、处理决定及后续责任。E03 的通用遗漏续办与自主恢复策略、C05 的独立成果权限、C07／E05 的整个目标预算仍按原顺序推进。本批逐项核对不代表这些条目已经完成。
