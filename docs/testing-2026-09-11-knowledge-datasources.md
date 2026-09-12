# K05：资料库知识源与产品上传验收

2026-09-11。K05 已完成：个人资料和共享资料库、成员角色、知识源绑定、文件上传、索引状态、原件下载和删除已接通。解析、切块和索引继续由 Verdent 处理。代码与此前成员管理、文档网页、私有上传的证据合并覆盖本项；不代表 K06 提取或 K07 跨库移动完成。

## 代码和存储

- SDK 的 `knowledge_datasource.go` 定义宿主目录 / 来源端口以及查询 / 绑定服务；HTTP 增加 `libraries_sources`、`libraries_bind_source`，均复用 Identity Action 权限。Module 和 SaaS 使用同一份契约。
- `module/knowledge_datasources.go` 装配宿主批准的知识源目录。用户只提交目录键和资料库版本；地址、team、KB、密钥和 ACL 不接受网页输入。默认读取 / 写入使用按 Runtime / Workspace / Library 生成的私有标识。
- `internal/application/knowledge_datasource.go` 检查实时成员管理角色与 Identity。`knowledge_library_source.go` 将持久绑定解析到公共来源端口；上传、后台 worker、检索和历史引用继续复用原有权限链。
- ORM 第 11 个迁移建立 `_agent_knowledge_datasource_bindings`。事务同时检查资料库版本、登记远端来源独占关系、写入目录键 / 物理来源摘要 / 私有策略摘要 / 操作者 / 时间，并递增资料库版本。失败不能留下来源占用。相同请求恢复原结果，仍需当前授权。
- `frontend/src/KnowledgeSourcesPanel.tsx` 在资料库详情显示知识源选择、连接状态、分页与重试。绑定成功直接使用响应更新 UI；丢失响应后可读取实际绑定恢复。浏览器只保存目录键和提交时版本，不保存密钥或文档内容。

绑定成功立即允许上传，无需重启。移除目录、改变 KB / 权限配置或尝试用启动绑定替代持久绑定时，知识源停用，已有原文件仍按资料库权限保存和下载。恢复原配置后恢复读取。当前 API 不支持换绑、释放物理 KB 或迁移已有文档；这些操作需要 K07 的迁移与清理流程。

## 验收结果

| 范围 | 结果 |
| --- | --- |
| 存储 | 两个并发请求竞争同一 KB 仅一个成功；失败事务无残留；重复请求不递增版本；阅读 / 编辑角色不能绑定；撤销成员后重试拒绝；归档库仍保留来源占用；工作区隔离通过 |
| Module / HTTP | 绑定后立即上传；目录分页不含凭证；未知 / 重复查询参数与多余 body 字段拒绝；Identity 撤权与恢复；未知源、过期版本、换绑拒绝；完整宿主重启、移除 / 改变 / 恢复配置通过 |
| SaaS | 同一 SDK 经签名 RPC 查询 / 绑定 / 恢复；实时成员和 Identity 拒绝通过 |
| 网页 | 桌面与 390 × 844 手机：未绑定、来源占用、分页、Identity 拒绝、响应丢失后刷新恢复、立即上传、配置移除后重启下载、恢复连接、删除和共享库绑定通过；成功绑定只发一次 PUT，不自动重新查询目录 |
| 视觉 | 首轮发现状态及说明文字小于 12px，已修正；第二次定向补验桌面 / 手机字体、控制名称、分页和溢出检查通过，无页面脚本错误 |
| 回归 | Agent / SDK 全量、相关 race、Go vet、前端 33 项测试与构建通过；最终启动别名停用场景单独补验通过 |

真实 Verdent 验收共 **102.35 秒**，每个场景使用新建的独立 Agent 验收宿主、真实 Identity / HTTP / SQLite / worker 和一个已批准的远端 KB。两组个人库、一组共享库均由产品 API 绑定后立即上传，无启动绑定。模型使用确定性工具 / 引用驱动，不宣称此轮使用真实模型或用户浏览器。

| 场景 | 独立远端 KB | 秒 | 完成内容 |
| --- | --- | ---: | --- |
| 个人 A | `kb-3bbd8f1d3249` | 43.16 | 私有上传、索引、三种 ACL 范围、配置移除 / 恢复与重启、原件、对话引用、清理 |
| 个人 B | `kb-148740163350` | 29.11 | 同上 |
| 共享库 | `kb-1f90b90e69a8` | 30.09 | 同上 |

每份合成文档只有一次 POST 和一次 DELETE，均取得 HTTP 200 回执；清理核对持久删除状态、原件引用清空、远端不可读取及搜索不再包含该文档。三份已全部清理，没有操作已有业务资料。同宿主三库跨用户 / 成员隔离的真实检索证据沿用 [K04](testing-2026-09-11-library-permissions.md)，不把本轮三组独立宿主上传说成同宿主并发上传测试。

## 证据与复现

- [机器汇总与三份远端写入清单](evidence/2026-09-11-k05-datasources.json)
- [网页完整流程原始报告](evidence/2026-09-11-k05-datasources/browser-functional.json)：功能通过，保留当时字体问题，未改写成全绿。
- [视觉修复后的定向报告](evidence/2026-09-11-k05-datasources/browser-visual-final.json)
- [桌面截图](evidence/2026-09-11-k05-datasources/desktop-typography-fixed.png)、[手机截图](evidence/2026-09-11-k05-datasources/mobile-typography-fixed.png)

本地协议 / 权限测试：`go test ./module ./internal/infrastructure/persistence/database/agent ./integration ./internal/assembly/web -run KnowledgeDatasource -count=1`。网页测试使用 `TestKnowledgeDatasourcesIdentityHTTPAndPersistentOriginal` 的显式临时 8092 夹具与 `frontend/tests/knowledge-datasources.browser.mjs`，不会接入用户运行中的宿主。

真实复现：`python3 scripts/test-agent-managed-document-live.py --live --datasources`。仅适用于此前核实的 Verdent team 与上述三个 KB；读取本机私有凭证配置，在持久临时目录保存每次远端写入的精确所有权与 SQLite 恢复状态。此命令会创建并删除合成文档，不能作为无副作用的健康检查。

当前验证数据库为 SQLite；PostgreSQL / MySQL、多实例及独立发布构建仍分别在 H08 / H04，不因本项完成而勾选。
