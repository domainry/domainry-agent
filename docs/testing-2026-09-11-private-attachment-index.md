# K07 会话附件私有远端索引：后端增量

2026-09-11。本批已接通原件 → 私有远端索引任务 → 状态 → 删除清理，以及 Module / Identity HTTP / SaaS 调用。**K07 仍未完成**：当前尚无会话私有检索 / 引用工具、网页索引与失败核对入口，也未做这一链路的真实 Verdent 验收。本批 HTTP 知识服务为合成协议夹具。

## 代码边界

- 公共 SDK `ConversationAttachmentKnowledge` 提供按当前用户 / 会话解析私有来源的宿主端口；`ConversationAttachmentIndexRepository` 保存持久任务。共享的 `KnowledgeSourceRegistry` 只检查物理 KB 归属，附件不依赖资料库生命周期接口。
- [应用服务](../internal/application/conversation_attachment_index.go)验证当前索引、下载权限与所属会话，再保存不可变的远端文档 ID、请求 ID、来源与权限摘要。[后台任务](../internal/application/conversation_attachment_index_worker.go)通过 SDK 来源和原件存储端口工作，不导入数据库、HTTP Provider 或其他服务的实现。
- [Provider 适配](../internal/infrastructure/provider/attachment_knowledge.go)复用官方 Connector 文档能力，按 Runtime / Workspace / User / Conversation 生成私有标识。上传、查询状态和读取使用相同范围；已解析的来源对象不能被另一用户复用。**原文件字节原样交给 Connector，Agent 不做格式解析、切块、向量索引或全文缓存。**
- [Module 配置](../module/attachment_knowledge.go)连接专用 KB，拒绝默认知识源、资料库、已配置知识源目录及另一工作区重复使用该物理 KB。持久归属记录在移除配置、重启或删除最后一个附件后仍保留；默认知识读取继续受归属检查约束。
- 第 14 个宿主 ORM 迁移保存来源归属和远端任务。索引完成时保留任务的递增编号并停止调度，删除时重新激活；不会因为删行后重建而重新接受旧执行的结果。旧原文件解析迁移与清理逻辑维持原样。

## 调用方式

先沿用原来的二进制上传 API 保存附件；上传本身仍不自动入库。显式调用：

```http
POST /agent/conversations/{conversationID}/attachments/{attachmentID}/index?expected_revision=2
```

请求不带正文，只接受一个 `expected_revision` 查询参数。调用要求 `agent.conversations.attachments_index` 和当前附件下载权限；用户不能提交远端 KB、权限标识或所有者。HTTP 层拒绝这些额外参数和正文。SaaS 通过公共 SDK `ConversationAttachmentIndexService.IndexAttachment` 调用同一应用服务。

相同索引请求可以用初始版本恢复已有回执，返回当前附件状态，不重置工作或重复上传。普通附件元数据新增 `index_status`，不暴露上游文档 ID、私有标识、存储引用或请求 ID。

宿主可注入 `ConversationOptions.AttachmentKnowledge`，或配置 `module.Options.AttachmentKnowledge`。独立启动接受 `AGENT_ATTACHMENT_KNOWLEDGE_BINDINGS` JSON；本地启动脚本也读取服务配置中的同名字段。每个条目包括 `workspace_id`、`base_url`、`team_id`、`kb_id`、`api_key_env`、可选 `top_k` 和既有 `response_mapping`。密钥只通过环境变量引用；私有 ACL 由宿主生成，不开放配置。该 KB 应专供会话附件使用。

## 删除与重启

父会话或附件进入删除状态后立即失去用户可见性。若尚未开始远端上传，可直接清理原件；若已开始，必须保留来源及任务，等待确认索引、删除回执和远端不可见，再删除本地原件。单独的“不可见”可能是权限拒绝，不能证明私有远端文件已删除。

全量回归首次暴露了既有资料库工作中的正常关闭间隙：记录 `PutStarted` 后、发出 HTTP 前被取消，会留下没有实际请求的“不确定上传”。[共同使用的关闭处理](../internal/application/knowledge_write_context.go)现在让已选定的上传 / 删除在原有时间上限内执行完，并在租约内保存回执；宿主等待这段操作完成。附件与资料库任务均使用这一处理。

这不承诺进程强制终止或网络断开下的恰好一次执行。本批当时对所有不确定写入保留记录；后续已补齐[幂等删除恢复](testing-2026-09-11-knowledge-delete-recovery.md)，同一删除可在来源明确支持时安全重试。上传仍不盲目重发，缺乏确定结果的上传核查仍属 K07 未完成范围。

## 验证与证据

- 持久层四组测试覆盖 KB 争用与两个注册顺序、所有者隔离、幂等恢复、上传中父会话删除、迟到结果、删除回执门槛、数据库关闭重开、执行接管，以及索引完成后再删除不会接受旧执行。
- SaaS → 应用服务 → 官方 Connector → HTTP 夹具覆盖正常、上传响应丢失、删除响应丢失、检查阶段撤权四种情况。原件字节、权限标识、持久请求 ID 和发送次数均校验。专项 **30.95 秒**通过；其中约 30 秒用于实际等待不确定删除的下一次检查。
- Identity / HTTP / Module 专项核对独立权限、拒绝额外权限参数及正文、重复回执、下载撤权 / 恢复、完整宿主关闭重开、父会话删除、数据库终态和 `.bin` 原件物理删除。最终普通专项 **0.84 秒**通过；扩充请求校验后的 race 专项 **7.41 秒**通过。
- 专门在写入标记已提交、HTTP 尚未发送时关闭宿主，分别验证上传和删除。宿主等待发送及回执落盘，重启后继续索引 / 清理，各只发送一次；race 专项 **0.35 秒**通过。既有跨库迁移专项修复后 **8.84 秒**通过。
- Agent 工作区全量 `go test ./...` 最终通过（integration 78.259 秒、web 40.969 秒）；SDK 全量通过。相关存储 / SaaS / Module race 通过，`go vet ./...` 通过，启动脚本语法与 `git diff --check` 通过。

失败证据也保留：初轮 HTTP 测试发现额外查询参数被忽略，已增加明确拒绝；随后测试循环重复反序列化对象，误保留了已省略的 `body_ref`，已修正测试读取；初轮全量的迁移 / 路由数量断言与关闭间隙问题已修正。最终通过日志不覆盖这些初轮记录。

[机器证据与全部日志](evidence/2026-09-11-k07-private-attachment-index.json)。本批未重新跑前端网页，也未向真实 Verdent 写入附件。
