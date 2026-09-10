# 会话附件元数据开发与验证

日期：2026-09-10。范围为附件持久化基础；未进行产品上传、原文件存取、真实知识服务私有 ACL 或浏览器附件验收。K05–K07 保持未完成，编号清单仍为 38 / 81。

本文保留最初存储基础的验证范围；后续原文件、接口、网页及清理队列进展见 [附件网页验证](testing-2026-09-10-attachments-web.md)。

## 已实现

- Agent SDK 新增 `ConversationAttachment` 公共元数据和可选 `ConversationAttachmentRepository` 宿主持久化契约。未发布可供浏览器上传的服务接口。
- Agent 新增第 7 个宿主迁移 `_agent_conversation_attachments`，记录所有者、来源会话、大小、状态、修订、文件引用及远端来源。迁移复用宿主账本，DDL / DML 使用 ORM。
- 所有者以 Runtime / Workspace / User 组合隔离；附件固定 `conversation_private`。同一会话的同一上传请求重复提交返回当前记录，不重置索引状态；同键不同内容元数据冲突。
- 状态支持 uploading / stored / indexing / ready / failed / deleting / deleted。修订号阻止旧工作结果覆盖新状态；stored 需要文件引用，indexing / ready 需要私有来源标识。ready 的实际索引与 ACL 验证仍须后续可信工作进程完成，本次没有该工作进程。
- 删除会话与标记附件待删除位于同一事务；公开列表隐藏 deleting / deleted。可信存储接口保留文件与远端来源引用，允许在父会话消失后继续清理。
- 限制单文件 16 MiB、每会话 50 条活动记录、每用户作用域 256 MiB 未清理容量和 1000 条元数据。待上传及待清理记录计入预算；目前没有配额管理页面或墓碑归档策略。
- 文件名拒绝路径和控制字符；公开列表不包含内部文件引用或权限标识。失败记录要求稳定错误码格式，宿主仍必须将上游错误分类，不能传入原始错误正文。
- 会话存储 Ready 检查包含附件表，缺少迁移时不能宣称就绪。

## 验证结果

| 验证 | 结果 |
| --- | --- |
| Agent `go test ./...` | 通过，含实际模块测试；日志 `/tmp/domainry-agent-attachments-foundation-full.log` |
| Agent SDK `go test ./...` | 通过；日志 `/tmp/domainry-agent-sdk-attachments-foundation-full.log` |
| 附件专项 `go test -race ./internal/infrastructure/persistence/database/agent -run 'TestAttachment' -count=1` | 通过，5.664 秒；日志 `/tmp/domainry-agent-attachments-foundation-race.log` |
| SQLite / PostgreSQL / MySQL 迁移生成 | 随 schema 测试通过；仅 SQLite 实际执行，不代表另两个数据库已实机验收 |

附件专项验证：并发重复预留仅一条记录；跨用户 / 工作区 / Runtime 拒绝；旧版本写入冲突；缺少私有来源不能进入 indexing；失败重试不重置来源；公开列表不泄漏内部引用；删除后重试不恢复；分页；父会话删除故障回滚；未完成上传计入配额。

另用磁盘 SQLite 完成真实关闭 / 重开：删除父会话后仍可读取待清理记录及文件 / 文档 / 权限引用；关闭前的旧索引任务不能将其标记 ready；清理状态可在重开后推进为 deleted。测试中的 deleted 仅验证状态迁移，不代表已执行物理文件或远程删除。

初次 foundation 过滤命令的 `TestFactory` 没有匹配模块测试名，输出为 `[no tests to run]`；最终上述 Agent 全量测试实际运行 module 包并通过，不以先前空匹配作为验证依据。

## 仍需完成

原始文件私有存储、上传 / 读取 / 下载 / 删除应用服务、Identity 动作授权、HTTP / SDK 服务契约、网页入口、持久索引与清理工作进程，以及远端 ACL 设置和更新均未接通。当前元数据迁移不能单独让用户上传文件，也没有将私人附件推送到默认团队可见索引。

用户提出的个人、组织及全员可见语义，连同文档改权限后的同步设计，记录在 [知识文档权限与同步](knowledge-permissions.md)。当前 Source 单一权限标识用于会话附件的持久化基础，不代表通用文档权限策略已实现。
