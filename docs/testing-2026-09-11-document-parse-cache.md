# K06 持久解析缓存增量验收

> 已废弃：本页记录历史实现及当时的验收。原文件解析、正文缓存、解析任务和相关 SDK 端口已于 2026-09-11 按用户要求删除；不再继续开发。升级保留原件并清理旧派生数据，详见[移除说明](document-parsing.md)。

当时状态（历史）：2026-09-11，缓存增量通过；K06 整项仍未完成。私有附件模型入口与原件按页读取继续在 K06 内推进。

## 实现边界

- `domainry-agent-sdk/document_parse_storage.go`：资源范围和 `DocumentParseStorage`；`persistence/document_parse.go`：持久任务 / 产物元数据端口。资源身份由宿主生成，模型不能选择用户、租户或存储路径。
- `internal/application/document_parse.go`：原件下载授权、入队、等待结果、worker、当前身份复核、产物哈希与解析器身份校验。通过端口调用存储 / parser，不导入具体文件引擎或 ORM。
- `internal/infrastructure/persistence/database/agent/document_parse_*`：第 **12** 个宿主 ORM 迁移 `_agent_document_parses`。旧迁移内容不变；保存范围、原件 / parser 版本、状态、租约、错误和私有产物引用，不保存正文。数据库状态变更均使用 ORM。
- `internal/infrastructure/parsestorage`：独立目录下的私有 JSON，复用不可变文件、进程锁及删除墓碑。Web 宿主创建 `<database>.parses` 并负责关闭；其他宿主可注入公共端口。

缓存键包含 Runtime / Workspace / 资源种类 / 资源及容器 ID / 附件所有者、原件 SHA / 引用 / 大小 / 文件名和解析器身份。共享资料库可复用同一产物，但每次访问仍检查当前库成员与 Identity 下载动作。附件资源范围独立，不能仅凭相同 SHA 跨用户读取。

状态为 queued → parsing → ready / failed；删除另有 deleting → deleted。首个请求持久入队，重复请求复用任务。3 秒租约每秒续租，操作上限 45 秒，续租不改变发布版本；失去租约即取消工作。宿主停止留下可接管任务，超时记为明确失败。原件进入删除状态时在**同一事务**撤销各版本解析、递增发布版本并安排清理；迟到提交及墓碑后的迟到写入均拒绝。

## 代码与进程验收

- `TestParseQueueLeaseTakeoverIsolationAndAtomicParentDeletion`：并发入队只生成一项、跨用户 / Workspace / Runtime 拒绝、parser 版本隔离、活动租约排他、续租保留发布版本、接管后旧 worker 不能提交 / 续租、父会话删除同时撤销所有版本及清理任务。
- `TestParseArtifactsStayPrivateAcrossReopenAndLateWrites`：文件存储关闭重开后保持字节，跨资源范围拒绝读取，删除墓碑阻止迟到写入。
- `TestLocalSpreadsheetConversationAuthorizationAndRestart`：真实 XLSX / ORM / 原件及缓存文件 / 工具循环；远端夹具只有摘要。重启恢复、下载动作 / 成员撤权后隐藏、恢复后复核均只实际解析 **1 次**；原件删除后派生 JSON 被清理。解析中成员撤销 / 原件删除不把内容送入模型。
- 同测试 `crash_during_parse`：先持久入队，在原件解析中停止服务，确认未发布半成品；重建服务在同一记录上接管，约 **3.41 秒**完成，合计两次尝试，原任务和新发布版本均可核对。
- 最终 Agent 全量、SDK 全量、相关 race 与 `go vet ./...` 通过。全量和 race 包含最后加入的短租约 / 续租实现；不是只运行缓存查询测试。

## 真实模型与网页

使用同一份合成 XLSX，真实 Identity / HTTP / SQLite / Verdent / `glm-5.3-flash-free`，定向运行：

```sh
python3 scripts/test-agent-managed-document-live.py --live --extract-formats --formats xlsx
```

**60.85 秒通过。** 保留前批字段 / 单元格校验，新增 ORM 检查同一原件仅有一份 ready 缓存，实际私有 JSON 可读；结束时确认缓存 deleted 且产物引用已清除。报告 `parse_cache_ready=true`、`parse_cache_deleted=true`、`cleanup_verified=true`、`absent_from_search=true`。合成资料一次 POST / DELETE，未改业务文件。恢复目录为 `domainry-managed-private-live-k9tzv5nd/xlsx`。

网页使用实际 XLSX 本地解析、真实 Identity / HTTP / 数据库，模型与远端知识接口为明确夹具。上传 / 下载字节、字段与表格、来源工作表与行号、完整宿主重启、提取权限撤销 / 恢复、手机删除及旧回复隐藏通过。宿主 **65.89 秒**结束，额外核实只生成一份持久解析且私有产物清理完成。桌面回复 / 来源窗口 / 手机回复三个基线均无质量错误和整页横向溢出。

上述外部与浏览器流程验证缓存复用 / 清理；其后缩短租约并加入续租，另以最终全量与 race 中的在途停止 / 接管测试验证。没有为这项内部时序调整再次上传已通过的外部合成文件。

## 未完成范围

缓存产物已经独立保存，但现有知识工具回执仍内嵌有界正文；原件按页读取与轻量来源凭据未完成。持久缓存已建模附件范围、权限检查和父会话删除联动，**私有附件尚未接入模型的发现 / 读取 / 提取入口**。DOCX / PDF 当前沿用已验证远端解析，本地适配器与进程隔离另行推进。K06 不勾选。

本批日志、合成结果与网页报告见[机器证据](evidence/2026-09-11-k06-parse-cache.json)，与前批[解析验收](testing-2026-09-11-knowledge-extraction.md)分开保存。
