# H01 运行详情与审计验收（2026-09-12）

H01 已完成。运行详情现在展示同一个 Agent 运行关联 ID、步骤和模型／工具调用统计、模型用量、排队与执行耗时、稳定错误码、逐次授权结果和用户确认结果。独立 tracing 平台是按需能力，本批没有新增部署依赖。

## 实现与边界

- Agent SDK 公开有界运行详情：`ConversationRun` 汇总关联 ID、指标、用量、时刻和安全审计事件；步骤和工具投影补充各自耗时、授权版本与确认回执。投影不公开模型输入／输出正文、工具参数／结果、凭证或授权证据。
- Agent 只查询自己持有的 `_agent_conversation_events`，按 workspace、用户、会话和 run 共同限定范围；最多投影 4096 条安全事件，超过时明确返回 `audit_complete=false`。调用次数区分逻辑工具调用和实际尝试，恢复／核查不会被误报为另一个逻辑调用。
- Agent 以服务端生成的 run ID 作为关联 ID，经 Tools SDK 的中立 `Request` 传给工具 owner；历史结果复核、确认和多操作授权也沿用对应 run ID。业务动作／流程经 Agent SDK 传到 Runtime，Runtime 拒绝不匹配的关联 ID，并映射到自己的 Principal 和 Audit SDK Actor。
- Audit 仍只负责自己的审计数据。本批修正其 SQL 类别筛选，使 `auth`、`auth_*`、`auth.*`、`authentication_*`、`authentication.*` 与公开 `ClassifyEvent` 契约一致；`oauth_connection_*` 仍归治理类，未用宽泛 `auth` 子串误收。
- 产品前端只消费 Agent SDK 投影，运行详情展示总体指标、步骤／工具耗时、授权和确认结果及审计时间线；390px 下时间与长授权版本可换行。

Agent 没有导入 Runtime／Audit 实现，也没有读取它们的表；Runtime 只通过 Agent SDK 和 Audit SDK 传递身份；Audit 没有依赖 Agent／Runtime。边界机器检查见 [architecture-audit.json](evidence/2026-09-12-h01-observability/architecture-audit.json)。`llm-proxy` 工作区保持干净，本批没有接模型代理或修改其接口。

## 端到端结果

编译后的产品页面连接真实 Identity HTTP 和 Agent SQLite，模型端使用会报告 token 用量的 OpenAI 兼容协议服务，外部写目标使用隔离验收 owner。一次运行经历写操作授权、用户确认、实际写入后结果不明、核查恢复和完整宿主重启：

- 运行只有 1 个逻辑工具调用、2 次实际工具尝试、2 次模型调用、3 次授权检查和 1 次确认决定；模型用量合计 prompt 14、completion 6、total 20。
- 外部 owner 仅产生 1 个持久写效果和 1 次核查；重启后没有重放写入。
- 页面重启后仍显示同一关联 ID、授权版本、确认用户和时间、步骤／工具耗时以及终态错误事实。
- 审计 JSON 不含测试工具参数中的 `周报验收事项` 或工具结果中的 `fixture-record`；这两个值只存在于原执行详情的既有受权限内容。
- 桌面与 390px 页面均通过，无页面横向溢出、JavaScript 错误或 console warning。启动前匿名会话探测产生两个预期 401，未被当作运行失败。

浏览器报告、宿主副作用核对和截图分别见 [report.json](evidence/2026-09-12-h01-observability/browser/report.json)、[host-audit.json](evidence/2026-09-12-h01-observability/browser/host-audit.json)、[桌面截图](evidence/2026-09-12-h01-observability/browser/run-audit-desktop.png)与[手机截图](evidence/2026-09-12-h01-observability/browser/run-audit-mobile.png)。

## 验证结果

- Agent 全量 `go test ./...` 通过；H01 persistence／HTTP 场景的 race 通过，相关 `go vet` 通过。
- Agent SDK、Tools SDK 全量与 `go vet` 通过；前端 61 项状态测试和生产构建通过，构建只有既有的大 chunk 提示。
- Audit 全量、`go vet` 和分类专项 race 通过。
- Runtime 的 action／workflow 关联 ID、Audit actor 映射和跨工作区拒绝审计专项 race 通过；该跨服务集成测试使用本地 Audit 后通过。
- Runtime 全量中业务与集成包通过；`runtime/bootstrap/runtime` 的发布锁测试因当前多仓库开发版本和 capability digest 尚未写入发布锁而失败。这属于顺序中的 H04 发布工作，原始输出保存在 `logs/runtime-full.log`，未拿失败测试冒充通过，也不影响 H01 专项验收。

全部日志和源文件摘要见 [机器清单](evidence/2026-09-12-h01-observability.json)。
