# 按资料库检索与历史权限复核验收

日期：2026-09-10。本批在已完成的个人 / 共享资料库管理上接入只读知识源，属于 K04 / K05 增量。没有实现文档上传入库、跨库共享 / 移动或远端 ACL 写入；这些条目仍不勾选。

## 实现范围

- SDK 新增可选 `ConversationLibraryKnowledgeSource`。配置资料库绑定的工具宿主使用 v2 `knowledge_search` / `knowledge_read`，接受可选 `library_id`，并发布 v1 `knowledge_libraries` 目录工具。未配置绑定的宿主保持原两个 v1 工具和纯文本兼容路径。
- 库 ID 只选择可信宿主连接。`module.Options.KnowledgeLibraries` 或 `AGENT_KNOWLEDGE_LIBRARY_BINDINGS` 将工作区和本地库 ID 绑定到实际 Connector；模型不能填写 origin、team、kb、密钥或 permission_ids。JSON 配置只接受密钥环境变量名，拒绝内联密钥、未知字段和尾随 JSON。
- 每个库绑定独立远端 KB。Module 检查同一 origin / team / kb 不被多个库或默认知识源复用，规范化域名大小写、默认端口和末尾斜线。自定义源或地址别名的实际隔离仍由可信宿主保证。
- `internal/application/knowledge_library_source.go` 在远端请求前后、旧结果复用时检查实时成员、归档状态及 Identity `libraries_get` 动作。文档 ID 必须在指定库内读取。成员资格不代替全局工具或资料库操作权限。
- 目录仅返回当前可读、未归档且已配置来源的库；沿用分页，过滤后可能出现空页但仍有 next_after。目录结果限制字节数，并保存当前用户范围内的规范化 JSON 摘要。历史名称与角色保持原快照，复用时检查现在是否还能访问，不以库修订变化永久阻止恢复。
- 检索凭据与引用保存 `library_id`。撤销成员、归档或撤销 Identity 读取动作后，历史消息 / 运行读取及冻结输入恢复均不再提供受限内容；恢复访问后重新复核原始结果。旧的库凭据不能退回无资料库授权的默认来源复用。
- 网页资料库详情显示是否已配置知识源，执行记录显示“查看可读资料库”，引用窗口显示实际资料库编号。该状态只表示有启动配置，不代表远端在线或文档已索引。

代码入口：[资料库来源与复核](../internal/application/knowledge_library_source.go)、[工具装配](../internal/application/conversation_knowledge_tools.go)、[宿主连接配置](../module/library_knowledge.go)、[Identity 资料库授权](../internal/assembly/web/knowledge_libraries.go)。配置步骤见 [知识检索](knowledge.md)。

## 自动化验证

| 检查 | 结果与日志 |
| --- | --- |
| Agent 全量 `go test ./...` | 通过；`/tmp/domainry-agent-library-knowledge-full-1.log` |
| SDK 全量 `go test ./...` | 通过；`/tmp/domainry-agent-sdk-library-knowledge-full-1.log` |
| Agent / SDK `go vet ./...` | 通过；`/tmp/domainry-agent-library-knowledge-vet.log`、`/tmp/domainry-agent-sdk-library-knowledge-vet.log` |
| 资料库集成、实际 Identity HTTP、Provider 凭据与 Module 配置专项 race | 通过；`/tmp/domainry-agent-library-knowledge-race.log` |
| 补充 Identity 资料库读取动作撤销 / 恢复专项 race | 通过；`/tmp/domainry-agent-library-knowledge-identity-race-2.log` |
| 前端类型检查与构建 | 通过；最终标签文案构建为 `/tmp/domainry-agent-library-knowledge-frontend-build-2.log`，仍有既有大 chunk 提示 |
| 既有前端状态测试 | 20 项通过；`/tmp/domainry-agent-library-knowledge-frontend-tests.log`，不等于新增资料库 UI 自动化覆盖 |

`TestLibraryKnowledgeToolsMembershipAndFrozenResume` 使用实际库持久化、HTTP Connector 与模型协议夹具。验证 B 的目录不含 A 私有库、伪造 A 库 ID 被拒且不请求其远端 KB、共享检索与读取的库标识保留、模型中断后重建服务、成员移除后无远端 / 模型请求，以及恢复阅读资格后继续完全相同的冻结步骤。两个远端 KB 均使用 `policy` 文档 ID，避免把文档 ID 当成全局授权。

`TestLibraryKnowledgeIdentityMembershipHTTPAndRestart` 使用两个实际 Identity 用户、HTTP 与临时 SQLite，验证无成员时没有远端请求、加入 reader 后目录 / search / read / 回答四步完成、个人库文档未查询、完整宿主重启保留来源，以及移除成员和归档后历史正文 / 引用隐藏、恢复后可读。随后补充“成员仍存在但 Identity libraries_get 被撤销”的独立分支，撤权时不请求远端，恢复动作后原答案可读；补充结果记录在 `/tmp/domainry-agent-library-knowledge-identity-race-2.log`。

开发中的两次定向检查曾失败：首次等待函数用了 A 身份读取 B 会话，修正测试身份；第二次目录序列化前后摘要不一致，修正为规范化 JSON 摘要。最后定向检查、全量与 race 均通过。Provider 另有带 library_id 的凭据不可直接在默认源复用的回归。

## 网页验收

8092 隔离实例使用编译后的产品页面，实际完成登录、查看共享库已配置知识源状态、进入“资料库检索网页验收”会话、发送查询、查看三次成功工具调用、读取 `123.45 元` 答案、打开带库编号 / 文档编号 / 原始片段的引用，以及刷新后保留答案。归档后刷新，原答案、工具内容与引用隐藏；恢复库后刷新，原答案与引用重新出现。补上目录工具中文名称后已重新构建并刷新确认。

构建替换静态资源期间一次刷新显示空页，构建结束后重新载入恢复；没有把该次空页算作持久化验证成功。尝试额外导出数据库证据时，脚本把 BLOB 当字符串处理而失败；该导出不作为证明。上述自动化断言和浏览器实际操作分别提供证据。

手动测试收到完成信号后正常通过：265.79 秒，日志 `/tmp/domainry-agent-library-knowledge-browser.log`；标签页、测试进程及 8092 监听器已关闭。8091 本批没有启动。

本批没有请求真实 Verdent；知识内容和模型选择步骤来自本地夹具。不能用本结果替代真实多 KB 配置、远端私有文档 ACL、文件入库 / 索引恢复、同用户不同会话附件、跨库移动及派生副本治理验收。
