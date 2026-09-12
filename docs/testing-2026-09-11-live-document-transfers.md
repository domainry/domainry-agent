# K07：真实 Verdent 跨库复制／移动验收

2026-09-11。实际 Identity / Module / HTTP / SQLite、原件存储、后台任务和官方 Connector 对接真实 Verdent，通过两个用户、三个独立 KB 的复制／移动流程，耗时 **86.54 秒**（包 87.102 秒）。模型为确定性工具驱动；本轮没有使用真实模型或重跑网页。网页操作及故障恢复沿用[上一批跨库验收](testing-2026-09-11-document-transfers.md)。

## 实际流程

| 步骤 | 结果 |
| --- | --- |
| 用户 A 在个人资料上传合成 Markdown | 原件私有存储，真实知识服务索引完成；用户 B 的原件下载被拒绝 |
| 用户 A 将文件复制到共享库 | 新远端文档使用独立共享库 ACL；原个人 ACL 无法读取共享副本，正确共享 ACL 可读取 |
| 用户 B 作为共享阅读者查询、读取与下载 | 对话实际调用知识搜索／读取，取得包含唯一验收标识的引用；下载与原件字节一致 |
| 阅读者尝试把共享文档移到自己的个人库 | HTTP 403，未创建目标；当前角色缺少源删除权限 |
| 用户 A 将 B 设为共享库编辑者，B 再移动 | 目标保存到个人 B 的独立 KB；源位置立即停止下载，A 无法读取 B 的个人目标 |
| 完整 Web 宿主关闭重开后重放原移动请求 | 返回同一目标文档 ID，不重复创建／推送；目标和 A 原先保留的独立原件均可准确下载 |
| 目标索引完成后查询与权限核对 | B 的对话引用目标本地文档；省略 ACL 和提供旧共享 ACL 均不能读取目标，正确个人 B ACL 可以读取 |
| 旧共享来源检查 | 旧共享下载、旧历史回复／引用隐藏；源远端删除完成后，原共享 ACL 也无法取得源文档 |
| 清理 | 三份合成文档的远端删除均确认，当前 ACL 的 fetch 不可见，搜索均不再包含相应文档 ID；本地持久删除状态及原件引用清空，磁盘无剩余原件二进制文件 |

三份文档分别使用此前批准的 API 推送 KB：个人 A `kb-3bbd8f1d3249`、共享 `kb-1f90b90e69a8`、个人 B `kb-148740163350`。三套私有 ACL、三个远端文档 ID、三个逻辑上传请求各自独立；每份文档仅一次 POST 与一次 DELETE，六个远端写回执均为 HTTP 200。未修改已有业务文档，未改变远端 KB 的全局权限或绑定。

## 实现与可恢复性

本批新增 `internal/assembly/web/knowledge_document_transfer_live_test.go` 的协议／真实双模式验收，共用相同 Identity 和产品 API 流程。业务实现仍是公开 SDK → 应用服务 → 既有存储与 Provider / Connector；未增加服务之间的直接依赖，也未加入原文件解析。

`managedLiveTransport` 的持久写记录补上 KB ID，使跨库写入归属可以准确区分。远端请求发出前记录方法、KB、文档 ID、私有 ACL 和逻辑请求 ID，成功后记录 HTTP 状态；不保存 API Key。应用 SQLite、原件目录和验收报告放在持久恢复目录。异常时先通过产品删除入口清理本次所属合成文档；若未确认清理，保留数据库和归属证据，不创建另一批文档掩盖失败。

实际恢复目录：`/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-transfer-live-p272holr`。本次 `complete: true`、`cleanup_verified: true`、`remote_search_absent: true`，不需要远端补清理。

## 测试与证据

- 在触发真实请求前，相同验收脚本通过本地完整协议检查：40.90 秒（包 41.890 秒）。相关 race 包 69.446 秒通过；Go vet、Python／浏览器脚本语法和差异空白检查通过。
- 本次真实验收一次通过，没有重复调用真实迁移流程。
- [机器汇总](evidence/2026-09-11-k07-live-transfers.json)
- [原始产品验收报告](evidence/2026-09-11-k07-live-transfers/report.json)
- [六笔远端写入记录](evidence/2026-09-11-k07-live-transfers/writes.json)

复现命令：`python3 scripts/test-agent-managed-document-live.py --live --transfers`。脚本从本地私有配置加载已有凭证，严格限制已验证 Verdent origin、team 和上述三套 KB；创建并删除合成文档，有真实外部写入。

K07 的真实跨库验收已补齐。会话附件直接进入私有 Connector 索引仍未实现，因此 K07 整项继续不勾选；当前附件上传、预览／下载、另存到资料库的独立索引边界保持不变。
