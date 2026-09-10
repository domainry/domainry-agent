# 个人资料库与共享资料库管理验收

日期：2026-09-10。此批完成资料库实体、成员角色及管理网页，属于 K04 / K05 增量。文档归库、远端绑定、检索库选择、私有 ACL 和索引同步仍未完成；不勾选整个 K04 / K05。

## 实现

- SDK 的 `KnowledgeLibraryService` 是可选扩展，挂在现有 Conversation Binding 上，与旧 Interactive / Task 不混用；公共契约、HTTP manifest 与可信 SaaS RPC 共用同一应用服务。自定义 Module / SaaS 宿主须配置 `ConversationOptions.LibraryAuthorizer`；Web 宿主默认装配 Identity 实现，权限只注册、不自动授予。
- 第 9 个宿主迁移使用 ORM 建立 `_agent_knowledge_libraries` 和 `_agent_knowledge_library_members`；作用域为可信 Runtime / Workspace 的无歧义摘要，成员索引按用户与库排列。个人库 ID 按所有者稳定生成；共享库按所有者和请求标识生成，与个人库使用不同命名空间。
- 个人库每用户唯一且不能添加成员；共享库创建者初始为 manager，其他用户必须显式加入。reader / editor 可以读取库元数据；只有 manager 可以设置名称、说明、归档状态和成员。文档尚未归库，reader / editor 的文档能力仍待接入。
- 更新设置和成员都要求期望库修订号，在同一可重试串行化事务中重查当前成员、检查最后一位 manager、增加修订并完成写入。并发移除两个 manager 最多成功一个。读者不能枚举成员名册；新成员需经过宿主验证为当前工作区有效主体，移除停用用户不要求其仍有效。
- Web Identity 逐次解析主体与当前操作权限，以应用加载的库事实授权；存储独立限制成员范围。管理动作使用 `agent.conversations.libraries_*`。测试的 Identity 全工作区动作许可不替代库成员关系；未加入的用户仍不可访问库。当前列表接口要求集合级动作许可，精细的逐库 Identity 列表策略尚未专项验收。
- 网页工作导航增加“资料库”，可以打开个人资料、创建共享库、查看列表和详情、改名／说明、归档／恢复、管理成员、切换角色和确认移除。刷新或重新打开后读取服务端当前状态；失去读取权限时清空详情，恢复后重新读取。成员输入目前使用当前工作区用户 ID，按姓名搜索选人尚未提供。
- 库创建具有服务端幂等和客户端同窗口重试标识；创建结果不明时不允许在同一窗口改参数复用标识。刷新后的创建请求恢复、跨标签编辑冲突 UI 尚待专项补验。单库成员及单工作区库数量当前各限 1,000，每页最多 50。

## 接口

| 方法与路径 | 行为 |
| --- | --- |
| POST /agent/knowledge-libraries | 创建共享库，或确保当前用户个人库存在 |
| GET /agent/knowledge-libraries | 当前用户成员库分页，参数 after / limit |
| GET /agent/knowledge-libraries/{libraryID} | 读取当前可见的库和调用者角色 |
| PATCH /agent/knowledge-libraries/{libraryID} | 按期望修订保存名称、说明与归档状态 |
| GET /agent/knowledge-libraries/{libraryID}/members | manager 分页读取成员及当前库修订 |
| PUT /agent/knowledge-libraries/{libraryID}/members/{userID} | manager 按期望修订新增／修改角色 |
| DELETE /agent/knowledge-libraries/{libraryID}/members/{userID} | manager 按 expected_revision 移除成员 |

前端与模型都不能指定所有者、工作区、远端 team / kb / permission_ids；新增接口不是模型工具。创建空库不会发送文档、邀请消息或通知，也不会修改现有 Verdent 知识源配置。

## 自动化验证

| 检查 | 结果 |
| --- | --- |
| Agent 全量 `go test ./...` | 通过；`/tmp/domainry-agent-libraries-full-1.log` |
| SDK 全量 `go test ./...` | 通过；`/tmp/domainry-agent-sdk-libraries-full-1.log` |
| Agent / SDK `go vet ./...` | 均通过；`/tmp/domainry-agent-libraries-vet.log`、`/tmp/domainry-agent-sdk-libraries-vet.log` |
| 新增库存储、实际 Identity HTTP、SaaS 的 race | 通过；`/tmp/domainry-agent-libraries-race-1.log` |
| 前端类型检查及构建 | 通过；`/tmp/domainry-agent-libraries-frontend-build-2.log` |
| 原有前端状态回归 | 20 项通过；`/tmp/domainry-agent-libraries-frontend-test.log`，不等同于新库 UI 的故障恢复覆盖 |

存储测试覆盖创建去重、不同内容同标识冲突、个人库唯一与私有、跨用户／工作区／Runtime 隔离、成员角色、分页、最后一位 manager 与并发移除、修订冲突和重新打开存储。SaaS 测试遍历七个操作、两页成员、实时动作撤销、成员移除及伪造 Runtime 拒绝。

真实 Identity / SQLite / HTTP 验证登录边界、不自动授予动作、浏览器伪造所有者拒绝、两个实际主体的非成员拒绝、reader / editor 无管理权、无效成员拒绝、个人库不可分享、动作撤权／恢复及完整宿主关闭重开后角色保留。首次核心检查只有能力目录“projection”计数断言失败；实际 62 个 HTTP 路由、15 个既有投影，新增库分类不新增数据投影。已修正断言并由逐条路由映射检查及最终全量证明。

## 真实网页操作

使用 8092 隔离临时实例和编译后的产品页面，模型为本地 testModel，未调用 Verdent。实际完成：登录、工作导航、创建“网页协作资料验收”、保存合成说明、添加测试阅读成员、调整为编辑、刷新后重新打开并核对角色、归档／恢复、阻止最后一位管理者移除、确认移除测试成员及刷新、Identity 读取动作撤销后详情隐藏／恢复，以及个人库不显示成员共享入口。窄屏布局已目视检查。

核对该临时实例实际数据库：只创建一个目标共享库，最终 revision 7、未归档、说明准确，成员仅剩 admin / manager；失败的最后一位 manager 移除没有增加修订，个人库仍只有一份。证据：`/tmp/domainry-agent-libraries-browser-evidence.json`。

手动进程收到完成信号后正常通过并退出，日志 `/tmp/domainry-agent-libraries-browser-1.log`。标签页与临时监听器已关闭。本次不意味着完整知识资料链路已验收，尤其不证明模型能读取新库内文档或远端 ACL 已生效。
