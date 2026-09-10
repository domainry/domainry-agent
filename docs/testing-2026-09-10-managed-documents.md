# 资料库文档存储、索引任务与访问过滤验收

日期：2026-09-10。本批完成 K05 / K07 的后端增量，采用已确认的个人资料＋共享资料库。没有新增文档管理网页，也没有以本批代码进行真实 Verdent 多库验收；外部知识服务和模型使用协议夹具，Identity、数据库、文件存储及 HTTP / SaaS 调用使用实际实现。

## 已交付代码

- SDK `knowledge_document.go` 提供五个可选文档服务方法、资料库原文件存储端口和严格的文档片段投影端口。文档管理是用户 HTTP / SDK 操作，没有新增模型写文件工具。
- Agent 第 10 个迁移增加 `_agent_knowledge_documents`、`_agent_knowledge_document_jobs`、`_agent_knowledge_document_sources`。分别保存文档归属与不可变原文摘要、可领取且带隔离令牌的持久任务、不能被配置降级绕过的远端来源登记。
- `internal/infrastructure/documentstorage/files.go` 复用不可变文件引擎，但使用独立目录及 Runtime / Workspace / Library 命名空间。共享原件独立于上传者后续的成员身份，删除标记在重启后仍阻止迟到的写入。
- `internal/application/knowledge_document.go` 实现登记、查询、下载和删除。相同用户／资料库／client_id 的重复请求复用记录，内容变化返回冲突。单文件上限 16 MiB，每库同时保留最多 1000 个未清理文档、256 MiB 原文件配额。
- `knowledge_document_worker.go` 在网络请求前持久记录上传开始；已开始的上传不会因失联或租约接管再次推送。实际 INDEXED 才开放检索，未知写入保留 `needs_reconcile`。状态查询失败和不确定结果保存稳定错误码，不保存上游错误正文。
- 删除首先提交 `deleting`，立即阻止检索、原文下载和历史来源复用。未启动推送的文档可直接清理；已启动推送的文档需观察到 INDEXED，记录删除开始，再核对远端缺失并清理原件。未知 DELETE 不盲目重发。长期不能核对的任务保留待清理状态，不能宣称删除完成。
- `knowledge_document_source.go` 仅将显式映射出的文档片段按本地 ready 文档白名单投影，去除未登记命中、任意额外字段及全局响应字段。对外引用使用本地 kdoc ID。再次读取 / 历史重放先检查本地资料库、文档状态与 Identity，再比较当前规范化片段结果。
- 受管理远端 KB 的登记不会随删除最后一个文件、关闭写入配置或去掉资料库绑定而消失。默认源及旧的库检索都检查这一登记，不能降级为不受过滤的完整上游响应。同一个宿主数据库内，一处物理来源只能属于一个本地资料库。

## 启用方式与接口

先创建个人或共享资料库，再由可信宿主为该库配置独立远端 KB。Go 配置使用 `KnowledgeLibraryConfig.ManageDocuments = true`；`AGENT_KNOWLEDGE_LIBRARY_BINDINGS` 的单条配置支持 `manage_documents: true`。必须提供实际 search / fetch 返回字段的 `response_mapping`，两种映射都要有正文内容字段。

此模式要求固定工作区、无动态 PermissionIDs / AuthorizeWorkspace 回调，且不能与其他库或默认源复用同一远端 KB。知识服务管理员和其他宿主仍需保证实际物理隔离；本地登记不能证明不同数据库或别名背后的远端配置。没有自动创建远端 KB，也没有替现有默认知识源开启管理。

Web 宿主默认在数据库旁的 `.documents` 私有目录保存原件；其他 Module / SaaS 宿主需显式注入 `ConversationOptions.DocumentStorage`。未配置托管来源时，文档上传返回 unavailable。

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| POST | `/agent/knowledge-libraries/{libraryID}/documents?client_id=...&filename=...` | 原始二进制上传，返回 queued 等实际状态 |
| GET | `/agent/knowledge-libraries/{libraryID}/documents` | 按库分页列出文档 |
| GET | `/agent/knowledge-libraries/{libraryID}/documents/{documentID}` | 查询文档及索引 / 清理状态 |
| GET | `/agent/knowledge-libraries/{libraryID}/documents/{documentID}/content` | 受权原文件下载，attachment / no-store / nosniff |
| DELETE | `/agent/knowledge-libraries/{libraryID}/documents/{documentID}?expected_revision=...` | 立即停止本地访问，持久保存清理任务 |

五个动作分别注册 `agent.conversations.documents_upload/list/get/download/delete`。阅读者可读和下载；编辑者 / 管理者可上传、删除。还必须拥有当前 Identity 操作权限，成员关系本身不授予全局权限。会话附件上传继续保持会话私有，不调用本套入库服务。

## 验证结果

- `TestManagedLibraryDocumentsIndexFilterRevokeAndRestart`：实际 Connector HTTP 夹具、共享成员角色、相同上传幂等、完整原件下载、PENDING / INDEXED、未登记命中和全局字段过滤、成员 / Identity 撤权、删除后模型和历史隐藏、关闭管理后的持久过滤、恢复清理、默认源降级拒绝。通过。
- `TestKnowledgeDocumentLeaseTakeoverNeverRepeatsPutOrRevivesDeletion`：上传开始后丢失响应、租约接管不重传、旧令牌拒绝、删除期间迟到的索引回执不能恢复 ready、持久来源不可复用。通过。
- `TestKnowledgeDocumentMembershipCheckedBeforeQueuedUpload`：排队上传者被移出资料库后不启动推送，已获准清理由可信任务继续。通过。
- `TestLibraryOriginalIsolationAndDurableDeletionFence`：跨库 / 工作区文件引用拒绝，关闭重开文件存储后迟到上传仍拒绝。通过。
- `TestKnowledgeDocumentsIdentityHTTPAndPersistentOriginal`：真实 Identity 登录、独立操作权限、Origin / 额外 ACL 参数拒绝、实际上传 / 索引、下载字节与响应头、关闭重开完整宿主后原件保留。通过。
- `TestKnowledgeDocumentsSaaSLargeOriginalAndUnknownPut`：3.6 MB 原文件通过 SDK / SaaS 往返；个人隔离、Identity 撤权、列表及删除；远端已接受上传却返回 503 时保留待核查，随后核实和清理，全程只推送一次。正常和丢失确认两种情况均通过。
- Agent `go test ./...`、SDK `go test ./...`、Agent / SDK `go vet ./...` 和相关持久化 / Provider / Web / 集成的 race 检查通过。迁移 SQL 在 SQLite / PostgreSQL / MySQL 方言均生成成功；实际存储验收使用 SQLite，没有声称完成 PostgreSQL / MySQL 实例验收。

日志：`/tmp/domainry-managed-documents-agent-full.log`、`/tmp/domainry-managed-documents-sdk-full.log`、`/tmp/domainry-managed-documents-race.log`、`/tmp/domainry-managed-documents-storage-race.log`、`/tmp/domainry-managed-documents-sdk-vet.log`、`/tmp/domainry-managed-documents-agent-vet.log`、`/tmp/domainry-managed-documents-http.log`、`/tmp/domainry-managed-documents-saas.log`。

## 剩余范围

后续批次已完成资料库窗口内的上传、状态、下载、删除交互及实际浏览器验收，见[文档网页验收](testing-2026-09-10-library-documents-web.md)。仍待完成：使用独立真实远端 KB 的完整新链路验收；会话附件另存个人 / 共享库已在[后续批次](testing-2026-09-10-attachment-library-copy.md)完成；其余待完成范围为：跨库共享 / 移动；未知推送长期不出现和未知删除长期不消失的人工核查入口；自动建立远端数据源。更新原文当前需新文档，不能原地覆盖已有文档 ID。

前一批真实 Verdent 文档协议验收仍见 [协议记录](testing-2026-09-10-knowledge-document-protocol.md)，不把它算作本批应用生命周期的真实外部验收。K05 / K07 整项保持未勾选。
