# K07 会话附件 Connector 检索与引用

2026-09-11。本批完成 `attachment_search` / `attachment_read`、持久引用、历史来源复核和网页引用打开原文件。知识内容来自官方 Connector 的 HTTP 协议夹具，身份、Module、SaaS、SQLite、原件存储和浏览器均使用实际实现。未向真实 Verdent 写入附件；K07 尚未整项完成。

## 实现边界

- 公共 SDK 的 `AttachmentConversationTools` 定义两个只读工具，并通过现有动作目录注册各自的 Identity 权限。搜索只接受 `query`，读取只接受本地 `attachment_id`；模型不能传入用户、工作区、会话、资料库、远端 KB、ACL、URL 或文件路径。
- [工具宿主](../internal/application/conversation_attachment_tools.go)与现有个人、业务、资料库工具宿主组合，保留交互授权和结果复核。工具需要该工作区的附件知识源、当前工具权限及 `agent.conversations.attachments_download`。父会话归档或不存在时不能读取；配置移除后旧结果仍须通过显式附件来源检查，不能因宿主卸载而放行。
- [应用服务](../internal/application/conversation_attachment_knowledge.go)通过公开 `ConversationAttachmentKnowledge` 来源端口和 `ConversationAttachmentKnowledgeRepository` 元数据端口工作，不导入 Connector、具体数据库、HTTP 或资料库服务。
- [元数据查询](../internal/infrastructure/persistence/database/agent/conversation_attachment_knowledge_store.go)使用已有 owner / conversation 索引与 ORM，只取该会话的 ready 记录。现有上传事务已限制每会话最多 50 个有效附件，因此无需新增远端映射表或迁移；查询超过 50 条会明确失败。
- 检索前后检查 owner / conversation、ready 状态、索引确认、删除标记、来源身份和私有策略摘要。上游命中仍须通过本地附件允许列表；即便上游错误返回其他文档，也不会进入模型。
- 原件字节不进入这条检索链路。没有新增源文件解析器、正文缓存、提取入口或 Agent 自有检索索引。显式上传和索引沿用上一批 K07 后端，网页预览单独使用已授权原件下载接口。

## 引用与历史

结果使用 `agent_conversation_documents`，包含本地附件 `doc_id` 和服务端绑定的 `conversation_id`；不暴露远端 `dka_` / 请求 ID、KB、ACL 或存储引用。引用标题使用已登记文件名，位置仅保留来源实际提供的值，外部 URL 不用作原件读取凭据。片段始终标为不完整，不能据此推断原文件总计。

引用 ID 和范围摘要绑定用户、会话、来源身份、私有策略、操作参数、原件摘要及实际片段。保存结果再次使用时，会重新取当前 Connector 内容，严格比较完整结果；来源、内容、引用或权限改变会隐藏旧结果。代价是搜索结果排序或集合变化也可能使历史回复暂不可用，不以历史回执代替当前授权。

`history_search` / `history_read`、`tool_result_read`、`execution_read` 和来源审计递归保留消费会话。其他会话不能借历史入口取回私有正文；显式用户查看原会话仍逐次核对当前权限。旧本地解析工具及来源继续拒绝复用，新工具不复用已废弃的 `knowledge_attachments` / `knowledge_read(attachment_id)` 协议。

执行事件、运行记录和实际引用的助手消息保存同一引用。仅配置附件知识源时也添加 `[[cite:ID]]` 格式提示。网页复用现有来源弹窗和 PDF / Word / Excel 查看器，`conversation_id + att_id` 映射到同一受权附件原件 API。

## 验证

- [SaaS / 官方 Connector 集成](../integration/conversation_attachment_retrieval_integration_test.go)：实际上传和私有索引后，让 Connector 返回与原件字节不同的正文，核实检索与每次来源复核均未读取原件。验证伪造范围参数拒绝、上游外来命中过滤、搜索 / 读取 / 引用、冻结步骤重启恢复、下载撤权及恢复；六条跨会话入口均不能泄露正文。补充工具撤权、远端正文变化、在途撤权、其他用户、来源配置移除 / 恢复与删除隐藏。普通专项 0.54 秒通过，最终相关 race 通过。
- [来源回执测试](../internal/application/conversation_attachment_knowledge_test.go)：真实来源位置保留；篡改会话、引用 URL、范围摘要、片段、原件摘要和额外字段均拒绝；配置移除与删除拒绝。存储端口没有可读实现，意外读取原件会使测试失败。
- [Identity / Module / HTTP](../internal/assembly/web/conversation_attachment_index_test.go)：真实 PDF 二进制上传和私有索引、三个模型步骤、持久引用、工具和下载独立撤权 / 恢复、整个宿主与数据库关闭重开、引用仍可读、原件字节一致、父会话删除、持久终态和物理原件清理。上传与删除远端效果各一次。最终 web race 包 21.275 秒通过。
- [实际浏览器](../frontend/tests/attachment-citations.browser.mjs)：实际构建客户端登录，打开持久会话的引用、查看 Connector 片段、预览原 PDF、翻页、下载字节核对；Identity 撤权清除画布，恢复后重新展示；刷新后引用保留，撤权刷新后回复与引用隐藏，恢复后可读。12.10 秒通过。桌面 / 手机 / 撤权界面基线均无错误和页面溢出，无外部网页请求。
- 截图复核发现手机首张截图早于 PDF 的 ResizeObserver 重绘完成。测试改为等待实际适合手机宽度的画布完成渲染，仅补验手机场景，6.74 秒通过；正确手机截图和机器报告另存，保留旧截图。
- SDK 全量、最终 Agent 全量（integration 79.485 秒、web 43.487 秒、架构 0.932 秒）、前端 37 项测试与生产构建通过；引用提示补充也由 SaaS / HTTP race 专项覆盖，最终 `go vet ./...` 通过。

失败记录保留：SaaS 测试初轮误读工具结果外层结构，修正夹具解包后通过；HTTP 原清理测试沿用发送消息前的会话版本号，更新为当前版本后通过；浏览器首轮登录会清除会话选择，测试补上从会话列表打开目标的真实步骤。没有用改写业务回执或跳过断言消除失败。

## 仍待完成

K07 后续仍需附件网页显式索引、状态展示与失败核对 / 恢复，以及专用知识源的真实 Verdent 端到端验收。本批不代表已在用户运行的 8091 环境部署，也不代替真实模型或真实知识服务验收。TODO 完成计数保持 48 / 81。

完整机器报告、截图和日志见[本批证据](evidence/2026-09-11-k07-private-attachment-retrieval.json)。
