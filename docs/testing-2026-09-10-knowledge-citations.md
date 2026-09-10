# 知识引用实现与验收记录

日期：2026-09-10。本记录区分代码实现、测试夹具的网页验收和真实外部服务联调。主清单见 [Agent 能力 TODO](agent-capabilities-todo.md)。

## 已实现

- SDK 增加 `ConversationCitation`，包含来源 ID、Provider、知识库、文档 ID、标题、链接、片段和片段截断标记。
- Provider 通过宿主配置的 `KnowledgeResponseMapping` 从真实返回数据提取字段，不猜测未声明的字段。未配置映射时保留原始 JSON，不生成结构化引用。
- 引用 ID 绑定当前来源 / 用户范围、完整响应及片段元数据；复用结果时重新检查当前来源，并核对保存的引用元数据。
- 完成的工具结果、执行事件、SSE 和历史消息提供引用投影。助手使用 `[[cite:ID]]` 标记，网页只接受该次已完成知识工具实际返回的 ID。
- 网页支持引用按钮、资料来源窗口和工具结果来源卡片。窗口显示实际标题、文档 ID、知识库、片段及安全的 HTTP(S) 链接；未知引用显示为未验证。

代码入口：[Provider 映射](../internal/infrastructure/provider/knowledge_citations.go)、[当前来源复核](../internal/infrastructure/provider/knowledge_execution.go)、[历史消息引用](../internal/application/conversation_citations.go)、[网页引用](../frontend/src/KnowledgeSources.tsx)。

## 已完成的浏览器验收

使用真实 Identity、HTTP、临时 SQLite 和当前前端构建；模型与知识返回由本地测试夹具提供。对应测试：`TestKnowledgeToolsUseLiveIdentityDocumentPermissionsAndStoredResultChecks`。

1. 打开已有回复的“来源 1”，核对标题、文档 ID、片段和链接。
2. 刷新页面后，原回复仍可打开同一引用。
3. 撤销当前用户的文档权限，刷新后相关历史回复、引用入口和资料片段均不再显示。
4. 恢复权限后，原引用重新可读。
5. 再次通过聊天搜索和读取资料，回复产生新引用，工具结果显示读取依据卡片。

浏览器验收完成后测试继续运行并 PASS；临时 8092 服务和专用浏览器标签已关闭。该结果不代表真实 Verdent 私有文档 ACL 已验收。

## 自动检查证据

- Agent 与 Agent SDK 全量 Go 测试通过；Go 静态检查通过。
- Provider、应用、存储、集成和 Web 相关 race 检查通过。
- 前端状态测试 19 项通过，前端构建通过。
- 最终 Provider 引用复核错误分类调整后，Provider 测试再次通过。
- 开发中曾因全局加入引用提示导致紧预算恢复测试失败，已改为仅在当前工具目录开放知识工具时加入提示；修改后全量测试通过。

本机日志：`/tmp/domainry-agent-citations-full.log`、`/tmp/domainry-agent-sdk-citations-full.log`、`/tmp/domainry-agent-citations-vet.log`、`/tmp/domainry-agent-citations-race.log`、`/tmp/domainry-agent-citations-frontend-tests.log`、`/tmp/domainry-agent-citations-build.log`、`/tmp/domainry-agent-citations-browser.log`、`/tmp/domainry-agent-citations-provider-final.log`。日志为本地临时证据，未纳入仓库。

## 真实 Verdent 联调

使用用户此前提供并授权用于联调的凭证，经终端隐藏输入运行只读连接探测；凭证没有写入仓库或此文档。

| 请求 | 实际结果 | 能证明什么 |
| --- | --- | --- |
| `POST /v1/chat/completions`，`glm-5.3-flash-free` | HTTP 200，返回一项 choices | 当前模型连接和鉴权可用 |
| `POST /v1/kb/search`，合成测试查询 | HTTP 200，`err_code: 0`，`data.hits: []` | 当前知识请求成功，已确认空结果包装与集合路径 |

真实结果集合路径为 `/data/hits`；夹具使用 `/hits`，二者不可混用。未获得非空命中前，不把夹具的文档字段映射写为生产配置。零命中不证明知识库为空。

## 仍待完成

- 真实文档命中及 fetch、实际文档字段映射、私有文档访问与撤权。
- 真实模型连续调用工具、保存结果并在重启后继续工作的完整流程。
- 上游非零 `err_code` 的真实错误契约与 Connector 业务错误处理。
- 使用逐文档权限 / 版本凭据替代当前完整响应重查比较；当前方法可能因排序或内容变化使旧结果不可复用。
- 附件上传、解析提取，以及派生成果 / 记忆 / 待办的完整来源与保留策略。
