# 会话附件另存个人／共享资料库验收

日期：2026-09-10。本批推进 K07，将已私有保存的会话附件明确另存为资料库独立副本。原附件不改变可见性，不自动共享，也不原地改写所属库。

## 接口与存储

新增可选 SDK `KnowledgeAttachmentImportService.ImportConversationAttachment`，Module HTTP 和 SaaS 复用同一应用服务：

```http
POST /agent/knowledge-libraries/{libraryID}/documents/from-attachment
Content-Type: application/json

{"client_id":"stable-request-id","conversation_id":"conv_...","attachment_id":"att_...","expected_revision":2}
```

输入只有来源引用、源附件版本和重试编号。原文件由后端按当前身份从私有附件存储读取，浏览器不重新传文件，也不能指定服务器路径、远端 KB、ACL 或其他用户 ID。

- [应用服务](../internal/application/knowledge_attachment_import.go) 分别检查 `documents_import_attachment`、`documents_upload` 和 `attachments_download`。目标需当前 editor / manager、未归档，并有支持文档管理的宿主绑定；源附件需属于当前用户的指定会话。
- 复用不可变原件下载校验、资料库原件存储、文档登记和持久索引任务。另存成功先返回 queued 等实际状态，只有 INDEXED 后才可检索。
- [持久化](../internal/infrastructure/persistence/database/agent/knowledge_attachment_import_store.go) 在预留和提交目标文档的事务内复核源附件归属、版本、正文摘要、大小及删除状态。源删除先于目标提交时，目标不能进入队列。
- 私有来源保存在既有文档 `payload_json.attachment_origin`，不会返回给共享库读者或进入模型引用。新增可选字段不改变普通上传的既有请求摘要；无需新表或变更已发布迁移。
- 另存使用独立于普通上传的幂等命名空间。同一个用户／目标库／client_id 改变来源或版本会冲突；响应丢失后复用原请求即可恢复，不重复创建文档或推送知识源。
- 目标提交完成就是独立副本。删除原附件或撤销后续附件下载权限，不撤销已经明确共享的副本；目标副本继续按目标库当前成员及 Identity 权限管理。已完成另存的回执可在来源已删除后恢复，尚未完成的另存仍必须复核来源。
- 仅在目标文件暂存后失去来源权限时，目标保留未完成上传记录且不入库，可由有权编辑者清理；不假报成功，不自动创建新请求重试。

## 网页流程

[AttachmentLibrarySave](../frontend/src/AttachmentLibrarySave.tsx) 位于附件详情的“另存到资料库”。用户查看目标库及其可见性后，点击“确认另存副本”；未连接入库源、只有阅读角色或已归档的库不可选择。资料库列表支持分页，另存后可直接前往资料库查看索引进度。

页面明确说明原附件仍私有、目标是独立副本，以及删除原附件不删除副本。浏览器按登录身份、源会话和附件保存有界重试回执，不保存正文。刷新可继续原另存；有未确认回执时锁定原目标，结束重试不删除已保存副本。另存期间其他附件写入按钮互斥，关闭窗口会取消浏览器请求但保留重试回执。

## 已验证

- `TestKnowledgeAttachmentImportAtomicSourceCheckAndIndependentCopy`：目标库编辑者不能导入他人的私有附件；伪造摘要拒绝；源删除与目标提交的竞争不会发布副本；普通上传编号不与另存冲突；重建 Repository 后已提交回执仍在；变更来源版本冲突；共享列表不暴露来源会话、附件和存储引用。
- `TestKnowledgeDocumentsSaaSLargeOriginalAndUnknownPut/import_lost_acknowledgment`：通过 SaaS 上传 3.6 MB 私有附件后另存；撤销附件下载权会拒绝新另存；源删除及撤权后已完成回执仍可恢复；副本下载字节一致。真实 Connector 协议夹具已受理推送但返回 503 时保存待核查，核实后清理，推送和删除各仅一次。
- `TestKnowledgeDocumentsIdentityHTTPAndPersistentOriginal` 的实际浏览器模式：真实 Identity 登录、文件存储、SQLite、HTTP、索引与清理工作进程，覆盖另存动作与源读取动作各自撤权、断响应后刷新续办、相同请求恢复、共享副本在源删除及完整宿主重启后可下载、跨会话工具查询与本地文档引用、共享副本独立删除。
- 初次新增另存浏览器整段 11 组场景通过，宿主测试 88.67 秒，race 包进程 91.673 秒。已查看另存结果及引用截图；新来源的私有会话／附件编号未进入引用。
- Agent / SDK 全量 Go 测试、两库 `go vet`、相关存储与 SaaS `-race`、前端 24 项状态测试、类型检查和构建通过。首次 Agent 全量测试仅暴露新增动作后的四处精确计数未更新；已按实际增加一个路由／动作更新预期并重新通过，未削弱清单校验。

日志：`/tmp/domainry-attachment-import-store-saas.log`、`/tmp/domainry-attachment-import-race.log`、`/tmp/domainry-attachment-import-agent-full-2.log`、`/tmp/domainry-attachment-import-sdk-full.log`、`/tmp/domainry-attachment-import-agent-vet.log`、`/tmp/domainry-attachment-import-sdk-vet.log`、`/tmp/domainry-attachment-import-ui-unit.log`、`/tmp/domainry-attachment-import-ui-check.log`、`/tmp/domainry-attachment-import-ui-build.log`、`/tmp/domainry-attachment-import-browser.log`、`/tmp/domainry-attachment-import-browser-host.log`。

## 原附件上传／下载补验

最终加强版完整通过 12 组网页流程，宿主测试 104.18 秒，race 包进程 106.055 秒，浏览器无 JavaScript 运行错误。新增验证如下：

- 原附件已被后端受理、浏览器响应丢失后刷新，重新选择原文件使用同一编号恢复；选错文件内容不发送第二次请求，列表只保留一个附件。
- 实际点击文本预览，并把私有附件下载到测试输出目录；文件字节与上传原文一致。
- 390×844 手机布局中的另存目标和共享确认无横向溢出，确认具体文件与目标库后才执行。
- “前往资料库查看进度”实际打开资料库窗口；回到源附件删除后，独立副本仍能在宿主重启后下载并被另一段会话引用，最后由用户独立删除。

四份实际下载（原资料库文档重启前／后、私有原附件、源删除并重启后的另存副本）SHA-256 均为 `4b5fa5281b048daf2433c79850949e01303f97cba1fe4bc0de1f6cdcaf17b49d`。报告、下载文件和已目视检查的桌面／手机截图在 `/tmp/domainry-attachment-import-browser-3/`；最终日志为 `/tmp/domainry-attachment-import-browser-3.log`、`/tmp/domainry-attachment-import-browser-host-3.log`。

加强版第二次运行因脚本在 React 处理窄屏切换前点击旧桌面导航而失败，测试宿主也因未完成清理审计返回失败，未计为通过；脚本已等待实际手机导航出现再操作。第三次使用完整加强断言通过，所有远端协议夹具中的测试文档推送／删除各一次并清理完成。

## 外部服务与剩余边界

本批使用独立 headless Chrome 测试配置及临时 8092 宿主，不使用用户浏览器 Cookie。知识服务与模型是协议夹具，未新增真实 Verdent 文档。2026-09-10 实际读取 Verdent 知识库管理列表仍返回 401；现有配置只有一处已使用的远端 KB，未将其改为新个人／共享库，也未绕过锁定桌面读取登录信息。真实独立多库验收仍需专用测试 KB 或控制台管理接口。

此处完成的是“另存副本”。跨资料库移动、删除来源与撤销共享的联动、会话私有索引、PDF / Word / Excel 解析提取、远端知识源配置入口及真实多库验收仍未完成，K05 / K07 整项不勾选。个人目标库使用同一服务与权限规则；当前浏览器选择的是配置完成的共享库，未绑定的个人库入口拒绝另存。
