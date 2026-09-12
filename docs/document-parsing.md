# 原文件解析已移除：改为在线预览

2026-09-11。用户明确要求：检索由 Connector 提供，Agent 根据命中结果找到原文件在线展示；不做源文件解析，已有实现删除。

## 当前边界

这里的“文档索引”指知识服务为了检索文档内容维护的索引。Agent 中的索引任务与状态用于提交 Connector 请求、显示远端进度和恢复原请求，不表示 Agent 实现了全文索引或向量库。

普通会话附件上传只保存原件；用户显式选择“用于当前会话检索”才提交给知识服务。仅根据已有检索结果打开 PDF / Word / Excel 原件，不需要 Agent 再解析正文或建立索引。资料库文档上传的远端入库生命周期与会话附件的显式操作分别管理。

- Connector / 知识服务负责检索及知识内容读取。Agent 不实现索引、向量库或原文件正文解析。
- Agent 保留个人 / 共享资料库与会话附件的原文件存储、权限、上传 / 下载和删除生命周期。
- `internal/application/knowledge_document_source.go` 将受权远端命中映射为本地文档 ID；前端使用同一原件下载 API 打开查看器。外部引用 URL 不是任意文件读取凭据。
- PDF.js、docx-preview、ExcelJS 仅在浏览器按需渲染已授权的原文件，展示过程不调用模型、不将正文写入服务端缓存或检索索引。Word 的浏览器排版不保证与桌面 Office 完全相同；旧 .doc / .xls、加密文件和超限文件明确提示下载。
- 保留的字段提取工具仅处理 Connector 返回的内容；它与原文件在线展示独立。
- K07 后续新增的 `attachment_search` / `attachment_read` 仅查询专用 Connector 中当前会话已授权的片段，不读取原件字节；它们使用独立协议，不恢复旧附件解析工具。见[检索与引用验收](testing-2026-09-11-private-attachment-retrieval.md)。

## 已删除

公共 `DocumentParser` / 解析任务 / 产物存储端口；Go XLSX / CSV / TSV 解析器与 Excelize 依赖；原件解析应用服务；持久解析缓存读写、租约 worker 和默认宿主注入；本地解析正文分页 / 来源回执；私有附件读取 / 提取模型工具及其提示。

旧来源在历史读取与工具结果复用时明确拒绝，避免删除授权实现后将旧正文当成普通会话内容复用。原始会话历史记录不做破坏性重写。

## 升级

历史第 12 个迁移保持原 SQL 和校验和，支持已有数据库升级。第 13 个宿主迁移通过 ORM 清空废弃解析表中的任务元数据；该历史空表无运行时读写路径。默认 Web 宿主删除自己之前管理的 `<database>.parses` 派生缓存目录，保留 `.documents` 与 `.attachments` 原件目录。自定义宿主应移除旧解析端口配置，并按其存储归属清理旧派生产物。

相关过去的验收文档只说明当时曾实现的功能，不代表当前仍有这些能力。当前执行和验收状态以 [K06 TODO](agent-capabilities-todo.md) 为准。

## 本次代码复核（2026-09-11）

再次按用户“源文件解析不要做，如果有就删掉”的要求核对 Agent 与公共 SDK：运行代码中没有 `DocumentParser`、`DocumentParseStorage`、`documentparser`、`parsestorage` 或 Excelize 引用。`knowledge_extract` 的输入来自 Connector 的受权文本片段，没有读取原文件存储的路径。历史工具定义仅用于验证旧远端回执，不向模型注册；旧本地来源仍被拒绝。浏览器查看器为按需展示原件而解码文件，未改为服务端解析。

四份旧解析验收文档及 TODO 中的相关历史增量已明确标记为废弃，避免误作当前开发计划。会话能力说明与知识权限说明中残留的“本地附件读取 / 提取”当前能力及待办描述也已清除，统一为 Connector 检索和受权原件预览。复核时修正了附件清理代码已有的变量重复声明编译错误，并将迁移数量断言对齐已有第 14 个迁移；本次未增加源文件处理能力。

本次专项验证通过，未重新执行真实模型或整轮浏览器验收：

- 后端：旧来源拒绝、从第 12 个迁移升级后清除旧元数据和派生缓存、原件保留及二次启动、附件 Identity / HTTP / 重启、附件另存、三种数据库迁移与应用架构边界。[日志](evidence/2026-09-11-source-parsing-removal-check/backend.log)
- SDK：历史远端回执定义兼容检查。[日志](evidence/2026-09-11-source-parsing-removal-check/sdk.log)
- 前端：两个原件预览身份 / 下载响应校验测试通过。[日志](evidence/2026-09-11-source-parsing-removal-check/preview.log)

补充清理：删除前端残留的 8 个 `document_parse_*` 错误提示，提取工具提示改为“知识服务返回的内容”，避免暗示 Agent 仍会解析原文件。当前源码复查无解析端口、解析适配器或 Excelize 引用；保留历史迁移与旧来源拒绝逻辑。现有升级清理 / 原件保留和旧结果拒绝两项测试通过（[后端日志](evidence/2026-09-11-source-parsing-removal-check/final-backend.log)），3 项原件预览身份与下载响应校验通过（[前端日志](evidence/2026-09-11-source-parsing-removal-check/final-preview.log)）。
