# C06 Agent 协作页面验收

日期：2026-09-14。本轮完成 C06 剩余的主会话协作概览、业务阶段进度、关联影响和问题合并，并修正“我发出的／我接收的”原先实际按当前会话过滤的语义错误。C06 验收完成，完整清单进度为 **5／25**。C05 与其他条目的待办范围不变。

## 页面行为

- [主会话协作概览](../frontend/src/ConversationCollaborationSummary.tsx)只通过当前主体的 `collaboration-access`、委派列表和可用 Agent 目录读取数据。它展示关联 Agent、业务阶段、完成条件核对结论、当前步骤／工具活动、等待原因、依赖／分歧／转交影响和合并后的待回复问题。刷新与 3 秒快照同步可在页面重载或连接中断后恢复。
- [协作状态投影](../frontend/src/collaboration-state.ts)依据真实 `owner_user_id`、当前与历史 `execution_subject`、会话与接手记录区分全局“我发出的／我接收的”和当前会话范围。“需要我处理”逐项归结补充信息、操作确认、外部结果核查、问题回复、要求变化、交付验收、分歧、失败、拒绝和资料受阻。
- [协作详情](../frontend/src/CollaborationDialog.tsx)保留原有沟通、委派管理、真实执行、参数／结果／错误、成果、回执引用、用量和交互卡片。从主会话点击后直接定位到对应委派。
- 完成条件只采用服务端保存的逐项 verification 结论。步骤数和工具调用数只表示执行活动，页面不根据它们生成完成百分比。

## 验收

- `npm test`：83 项前端状态测试通过；新增范围、待处理归属、问题合并、完成条件和当前步骤测试。
- `npm run build`：TypeScript 检查与 Vite 生产构建通过。
- `AGENT_PEER_BROWSER=1 go test ./internal/assembly/web -run '^TestPeerCollaboration' -count=1 -timeout 10m -v`：真实 Identity／HTTP／SQLite／Chrome 流程通过。新增断言覆盖主会话概览、无虚构百分比、全局发出范围、详情定位、验收后 `3/3` 核对结论、桌面与 390 px 布局。

命令、结构化报告、源码摘要和截图见[验收证据](evidence/2026-09-14-c06-collaboration-ui/commands.md)。
