# K06 会话私有附件读取与提取验收

> 已废弃：本页记录历史实现及当时的验收。附件源文件解析、缓存及直接向模型提供本地解析正文的工具已于 2026-09-11 按用户要求删除。附件保留受权原件存储、下载和浏览器查看器；详见[移除说明](document-parsing.md)。下文代码路径仅代表当时版本。

2026-09-11：本轮完成当前会话附件目录、XLSX / CSV / TSV 原件读取与提取、持久解析缓存复用、历史来源隔离及网页验证。K06 仍未全部完成；原件分页与不重复内嵌全文的来源回执继续留在 K06。证据索引：[机器记录](evidence/2026-09-11-k06-private-attachments.json)。

## 实现边界

- 公共 SDK `conversation_attachment_knowledge.go` 定义 `knowledge_attachments`，以及支持互斥 `doc_id` / `attachment_id` 的 `knowledge_read` v3；`knowledge_extract` v2 同样接受显式附件定位。模型不能传入会话、所有者、权限标识、路径或 URL。旧文档读取 / 提取定义保留为已知历史契约。
- [应用服务](../internal/application/knowledge_attachment.go)通过内部窄接口组合现有附件服务、公共 `DocumentParser` 和解析存储端口。原件、授权、来源凭据属于 Agent；文件格式解码仍在 `internal/infrastructure/documentparser`，应用层不导入解析库或其他服务实现。
- [Module](../module/factory.go)有附件存储时默认注入解析器。即使没有任何 Verdent 知识源或资料库，也能装配目录、读取、提取三项工具；仍分别要求当前工具权限与附件权限。
- `knowledge_attachments` 只列出执行器传入的当前会话。返回文件名、ID、大小、SHA、格式能力与分页游标；不返回原件路径或缓存地址。后续新增附件不会使此前目录快照失效，快照中的附件删除或权限变化会触发重新授权。
- 解析缓存资源键绑定 Runtime / Workspace / User / Conversation / Attachment、原件版本及解析器身份。读取缓存前后仍检查当前身份、会话归属、原件哈希与删除状态。解析产物不是授权凭据。
- 私有来源使用 `agent_attachment_document`，并绑定所属会话。历史搜索、原文读取、`execution_read`、`tool_result_read` 及模型上下文的来源检查传入当前消费会话；同一用户也不能通过另一个会话的旧结果绕过附件范围。用户在原会话打开历史回复仍可查看当前获准内容。
- 网页 `parse_supported` 表示格式能力，不表示已索引或免于权限检查。上传 XLSX 后提示可在当前会话读取 / 提取；原文件下载保持逐字节一致。PDF / DOCX 仍可私有保存，但本轮没有增加这两种格式的本地解析。

## 验证结果

1. [应用集成测试](../integration/conversation_attachment_knowledge_integration_test.go)：真实 SQLite、私有原件、默认解析器及持久缓存；无远端知识源。目录 → 读取 → 提取金额并验证 B4；工具之后的模型故障、宿主重建与冻结输入恢复；附件下载 / 列表撤权和恢复；之后新增文件不撤销旧快照；跨会话目录 / 读取 / 提取 / 历史原文 / 旧运行 / 完整结果隔离；跨用户与工作区拒绝；解析中撤权与删除不向模型提供正文。通过。
2. [Identity / HTTP](../internal/assembly/web/attachment_knowledge_test.go)：实际上传 XLSX，状态保持 `stored`；当前会话工具读取、引用、完整宿主重启、附件下载和提取动作分别撤权 / 恢复、删除后历史内容隐藏，缓存 ready → deleted。通过。
3. 真实模型 `glm-5.3-flash-free`：使用同一份合成 XLSX，原始文件 SHA 不变；模型自行发现附件、读取正文并选择提取规则。四个字段分别定位 B3 / B4 / B5 / B6，金额 `9007199254740993.25` 保持字符串精度，两行明细定位 A10–C11，邮箱返回 required missing。最终测试 **32.37 秒通过**，包括重启、权限撤销 / 恢复、附件删除和缓存清理。宿主未配置任何知识源，没有向 Verdent 知识服务上传文件；仅授权的合成正文发送给真实模型。
4. [浏览器脚本](../frontend/tests/attachment-knowledge.browser.mjs)：真实 Identity / HTTP / SQLite / 原件解析，模型决策为协议夹具。桌面上传、原件下载一致、字段 / 表格 / 来源、宿主重启、两类撤权 / 恢复、手机显示和删除通过。四个布局基线均无 quality failure、无页面溢出，浏览器异常为 0。人工检查手机结果与桌面来源截图通过，宿主核实解析缓存已删除。宿主 **61.07 秒通过**。
5. 最终 Agent `go test ./...` / `go vet ./...`、SDK 全量、相关 race、前端 33 项测试与构建通过。既有 Vite 大包提示仍在，不影响构建结果。旧提取 v1 的文档来源回执继续可复核，旧定义不接受附件参数；此测试不宣称变更定义后所有旧运行都能直接恢复。

## 失败记录与修正

- 首轮私有 HTTP 测试误把上传完成后的 revision 写死为 1，删除返回 409。改为使用真实上传回执 revision，未修改产品的冲突规则。
- 首轮真实模型 **33.23 秒失败**：金额、日期与明细正确，但模型把原表说明 `The contact email is intentionally absent.` 中的 `intentionally` 捕获为邮箱。工具对普通 text 的类型校验无法判断业务含义；有真实引用也不代表字段语义正确。补充当前工具说明，要求按目标值的形态匹配，不能把标签或缺失说明当成字段值，检查返回值并修正规则。复验使用相同字段要求和原文件，通过缺失邮箱断言。该说明降低错误，不是通用语义正确性的保证；本轮没有新增专门的邮箱类型校验。
- 首轮 race 与全量检查并行时，HTTP 夹具的 15 秒轮询预算用尽，运行仍为 running，无数据竞争报告。扩大测试轮询到 60 秒，产品期限不变，最终 race 通过。
- 首轮失败日志与合成来源结果保留在机器证据中，未用最后一次成功覆盖失败记录。

## 仍未完成

本地解析现在解决指定原件的读取和提取，尚未把解析出的单元格重新送入 Verdent 的全文 / 向量索引。远端搜索的召回范围仍取决于原知识服务；不能据此声称已能按所有 Excel 单元格搜索资料。

来源回执仍可能包含有界解析全文，原件按页读取、缓存摘要式来源凭据、DOCX / PDF 本地解析及硬进程隔离尚未完成。历史资料派生到个人记忆等副本的完整治理继续属于 H02；本轮隔离验收覆盖上文列明的来源链路。共享 / 移动、索引同步与完整附件生命周期继续按 K07 顺序处理。

## 复现

真实模型：`python3 scripts/test-agent-private-attachment-live.py --live`，从现有私有配置加载模型凭据，不把密钥写到命令或日志。脚本创建独立合成验收目录并保留证据。

网页：先构建前端；使用 `AGENT_TOOL_UI_ACCEPTANCE=1 go test ./internal/assembly/web -run '^TestPrivateAttachmentKnowledgeIdentityHTTP$' -count=1 -v -timeout=18m` 启动仅测试用的 8092 服务，再运行 `frontend/tests/attachment-knowledge.browser.mjs`。此夹具不修改用户 8091 的数据。
