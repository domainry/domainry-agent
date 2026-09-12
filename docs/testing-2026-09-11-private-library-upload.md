# K05 私有上传增量验收

本批完成产品私有上传链路，K05 整项仍未完成。远端数据源的网页配置入口、独立多库的产品上传验收仍待交付；不提前开始 K06。

## 实现与边界

- Connector `knowledge_base/http_api` revision 1.2.0 接受可信连接配置 `document_permission_ids`，用紧凑 ASCII JSON 写入 `X-KB-Permission-Ids`，稳定命令身份来自已有 SDK `RequestRef`，不增加模型或浏览器的权限参数。省略与显式空数组保持不同语义；Agent 资料库绑定默认生成非空私有标识。
- `module/library_knowledge.go` 从 Runtime / Workspace / Library 的无歧义 JSON 元组生成 `scope:agent:library:<sha256>`。显式 `PermissionIDs` 经校验、去重后作为固定读写范围；同库阅读者、编辑者和管理者共用阅读范围，实际动作仍由实时成员和 Identity 授权。关闭上传时继续使用同一阅读标识，不继承组织树。
- Provider 通过 Agent SDK 公共可选端口提供权限摘要与大小限制。物理来源身份仍只识别实际 origin / team / KB，变更权限不能绕过持久来源登记。
- 应用预留文档时保存权限摘要和稳定 `PutRequestID`，二者与原件、来源一起进入现有 JSON 记录。启动上传前的持久标记与旧记录请求 ID 补齐在同一事务内完成；未知上传仍仅核查，不自动重复 POST。权限配置变化后停止旧任务，原件与待处理状态保留。
- ready 文档白名单、再次读取和历史来源复核同时比较权限摘要。已有旧文档不会因修改配置而自动获得新 ACL；原件仍按当前库授权下载，远端权限迁移留待 K07。
- 私有 DELETE 必须取得并持久保存受理回执，随后核对当前固定权限下的缺失，再清理原件。缺失回执时，1004 不能单独把任务置为 deleted。
- 知识服务固定源码提交 `dbf61406e55b8a446f026cde941ce7f9a9f33bda` 的 `handler/push.go` 明确限制 10 MiB 和 ASCII 文件名。Connector 与应用提前拒绝过大文件，库接口向网页返回 `document_max_bytes`。中文文件保留本地原名，Provider 生成稳定 ASCII 传输名称并保留解析后缀。解析、切块和索引继续由 Verdent 处理。

## 真实产品链路

入口：[验收脚本](../scripts/test-agent-managed-document-live.py) 与 [真实 HTTP 测试](../internal/assembly/web/knowledge_document_permissions_live_test.go)。采用真实 Identity 登录、HTTP Handler、SQLite、Module、原件目录、持久 worker 和官方 Connector；使用真实 Verdent bcri KB 的新建合成文档。模型为确定性查询夹具，不把本次算作真实大模型或新浏览器验收。

最终一轮 32.04 秒通过：

1. 通过产品 HTTP 上传中文文件；重复提交返回同一文档，远端只收到一次 POST，上传携带默认私有 ACL 和持久请求 ID。
2. 观察到实际 INDEXED 后开放检索；省略权限和错误权限均看不到文档，匹配权限可以读取。
3. 完整关闭并重开宿主，同时关闭上传配置；原字节、中文原名和已有文档检索仍可用。
4. 对话实际搜索并读取该上传文档，回复带可核对的正文和本地文档引用。
5. 再次启用上传配置并重开宿主，提交删除；持久记录包含 DELETE 回执、deleted 状态和已清空原件引用。相同权限下 fetch 返回 1004，search 不再命中该文档，远端只收到一次 DELETE。

最终日志：`/tmp/domainry-K05-managed-private-readonly-live.log`；原始证据目录：`/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-managed-private-live-tne9t279`。本项汇总见[机器证据](evidence/2026-09-11-k05-private-upload.json)。

首轮 41.51 秒时测试对已删除文档错误要求 HTTP 200，产品正确返回 `document_not_found` 404，故该轮记录为失败；没有将其改记成功。随后独立核对其持久删除回执、空原件引用、相同权限 fetch 1004 和 search 不命中，合成资料已清理。修正断言后的第一轮 32.74 秒通过；再补上关闭上传后的读取与真实 search 清理验证，形成上述最终一轮。三轮各自的文档 ID、回执与清理证据均独立保存。

## 本地验证

- Connector：私有 / 空数组 / 省略 ACL 区别、Unicode 转 ASCII JSON、大小和个数限制、畸形配置与请求 ID 在网络前拒绝、伪造请求头不能覆盖权限、DELETE 不带 ACL 修改、不宣称内容写入幂等。
- Provider / Module：同一私有范围用于上传、搜索、读取、状态查询；配置副本不可被调用者修改；不同 Runtime / Workspace / Library 的默认 ID 隔离；关闭上传不改变阅读范围；修改 ACL 不改变物理来源身份。
- 存储 / 应用：请求 ID 与权限版本持久化、重复上传权限冲突、租约接管不重传、私有删除缺少回执不完成；排队 / 不确定上传遇到权限变化不发起网络请求；私有删除遇到隐藏文档保留原件和待核查状态。
- 实际 Connector / SQLite 集成：过大文件不留下预留记录、成员和 Identity 撤权、配置变更后模型与历史过滤、重启、原件下载、完整删除与默认来源降级拒绝。
- Agent 全量、相关 race、静态检查；SDK 全量；Connectors 全量及最终 Catalog / Provider 检查；前端状态测试和构建。各检查的实际退出结果记录在机器证据中。首轮新增夹具的目录权限、轮询下限和 ToolHost 装配错误已修正，失败日志保留，最终检查以对应 final 日志为准。

没有新增跨仓库内部依赖、分支或 worktree。凭证仍只从本机私有配置读取，证据不保存 API Key。
