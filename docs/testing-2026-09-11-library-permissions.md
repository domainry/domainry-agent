# K04：个人资料与共享资料库的私有检索权限

日期：2026-09-11。K04 已完成检索授权验收。最终使用真实 Identity Module、临时 SQLite、产品 HTTP Handler / 认证 Cookie、官方 Connector 和三个独立 Verdent KB，17 个场景、95 次知识请求通过；在途撤权前后的模型调用计数同为 24。模型采用确定性权限攻击夹具，以稳定验证伪造参数及撤权时序；本批不声称完成新的真实 LLM 或浏览器验收。

## 产品行为与架构

个人库只允许所属用户读取；共享库维护 reader / editor / manager 成员，三种角色获得相同库内阅读范围，管理动作另查成员角色及 Identity。组织树不参与继承。库成员退出或 Identity 阅读动作撤销后，新请求在访问远端前被拒，旧消息、运行和来源重新校验；恢复权限后重新读取原来源，不改写原历史快照。

新增 `module.KnowledgeLibraryConfig.PermissionIDs`，对应启动 JSON `permission_ids`。它是管理员配置的上游库阅读标识，经过现有 `Knowledge.PermissionIDs` 回调写入官方 Connector 的连接策略；不是工具参数。启动校验最多 100 个 ID、每个 128 UTF-8 字节，拒绝空值、前后空白、控制字符及非法 UTF-8，排序去重并复制以防调用者改写。不能和动态回调重复配置。独立 KB 限制保留，不以不同权限 ID 为由允许多个本地库或默认源复用同一远端范围。

代码边界：Module 负责可信启动配置；Application 复核资料库成员、归档状态和授权端口；Web / SaaS 宿主经 Identity SDK 评估动作；Provider 和官方 Connector 负责实际检索。没有引入跨仓库 internal 依赖，也没有为权限验证建立第二套检索协议。入口见 [库配置](../module/library_knowledge.go)、[资料库读取与复核](../internal/application/knowledge_library_source.go)、[Identity 授权](../internal/assembly/web/knowledge_libraries.go)。配置方法见[知识源配置](knowledge.md)。

本项仅配置和验证检索权限，不写入或修改文档 ACL。非空 `permission_ids` 与 `manage_documents=true` 混用会启动失败，防止私有文件按现有公开上传协议发出。生产私有上传及稳定请求身份在 K05 接入；跨库共享／移动和远端权限同步仍在 K07。成员变动不需要逐篇重写上游 ACL，服务端停止向无权主体提供库阅读标识即可。

## 验证范围

| 要求 | 实际证据 |
| --- | --- |
| 个人 A / B 隔离 | 两个真实 Identity 用户各自创建个人库，拥有者可搜索、读取及取得引用；互访被拒且没有远端请求。本地协议夹具特意复用同一个 doc_id，验证不能以全局文档 ID 绕过库范围 |
| 共享成员资格 | 无成员时请求被拒；加入 reader 后可读；移除后旧消息与运行的引用隐藏，新请求被拒，均无远端调用 |
| 三种角色 | reader / editor 可读但不能修改库设置；manager 可修改；降回 reader 后仍可读且管理操作再次被拒 |
| Identity 动作与成员独立 | 成员仍在库中，撤销 libraries_get 后旧结果不可读且不请求远端；恢复该动作后原来源可用 |
| 不能扩大请求范围 | 模型传入额外 permission_ids 被参数校验拒绝，未请求上游；在共享库指定个人库文档 ID 也不能取得个人资料；全部实际请求只携带对应 KB 的可信权限 ID |
| 重启与历史 | 完整关闭并重建宿主，重新登录后原引用仍可读取；后续成员或动作撤权立即影响旧回复和运行 |
| 在途撤权与模型输入 | 在真实 fetch 返回后、交给 Provider 前暂停回执，移除成员后放行。运行以 knowledge_access_denied 失败，原搜索证据也失效；模型调用次数 24 → 24，受限内容没有进入下一次模型调用 |

跨工作区与未知主体、个人库不能加成员、最后一名管理员保护、SaaS 实时授权以及冻结步骤恢复沿用已有测试，随本轮全量回归通过；库 / HTTP / SaaS 授权和冻结恢复另通过专项 race。代码分别见 [Module 配置测试](../module/library_knowledge_test.go)、[真实／夹具权限流程](../internal/assembly/web/library_permissions_test.go)、[SaaS 资料库授权](../integration/knowledge_libraries_integration_test.go)、[冻结步骤恢复](../integration/library_knowledge_integration_test.go)。既有资料库界面与带库引用的浏览器验证见[按库检索验收](testing-2026-09-10-library-knowledge.md)和[资料库管理验收](testing-2026-09-10-knowledge-libraries.md)，其模型和知识源为夹具，不冒充本批真实 Verdent 验收。

## 真实记录与清理

固定既有团队中的三个独立 API push KB。每次只创建具有随机 ID 的合成文档，写入前确认不存在；没有创建／删除 KB，没有改动已有业务文档或用户数据库。每份资料均使用独立的私有权限 ID，并在索引后取得 4 个实际片段。

最终日志 `/tmp/domainry-K04-private-libraries-final.log`：测试 55.13 秒，Go 包总计 55.842 秒。原报告在 `/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-library-permissions-live-8wnospy2/library-permission-report.json`。仓库内的[机器证据](evidence/2026-09-11-k04-private-libraries.json)保存 17 个场景、10 个运行 ID、95 次请求的元数据、模型计数及清理记录，不含 API Key 或业务正文。

| 资料 | 合成资料清单目录（均位于系统临时目录） | 清理结果 |
| --- | --- | --- |
| A 个人库首次对照 | `domainry-knowledge-live-doc-73216_6b` | DELETE 200 / 0，fetch 1004，按原权限搜索 hits 为 0 |
| A 个人库最终对照 | `domainry-knowledge-live-doc-c8uvjhd2` | DELETE 200 / 0，fetch 1004，按原权限搜索 hits 为 0 |
| B 个人库对照 | `domainry-knowledge-live-doc-g8sk_06b` | DELETE 200 / 0，fetch 1004，按原权限搜索 hits 为 0 |
| 共享库对照 | `domainry-knowledge-live-doc-gh6xnpux` | DELETE 200 / 0，fetch 1004，原标识搜索仍有 1 个其他文档，未出现已删除文档或标识 |

完整清单根目录为 `/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T`，各目录保留 `manifest.json` 与 `search-after-cleanup-check.json`。四份清单均为 `delete_acknowledged=true`、`cleanup_verified=true`。清理日志为 `/tmp/domainry-K04-personal-a-cleanup.log`、`/tmp/domainry-K04-personal-a-final-cleanup.log`、`/tmp/domainry-K04-personal-b-cleanup.log`、`/tmp/domainry-K04-shared-cleanup.log`。

首轮真实测试已通过 17 场景、94 请求（`/tmp/domainry-K04-private-libraries-live.log`）。A 的首份对照已清理后，为补强模型调用计数重新创建 A 对照；没有将旧文档重复上传或修改既有 ACL。创建最终 A 对照时观察到 PARSING 中间态，原脚本提前停止；已改为在有界循环内等待非终态并要求真实片段含唯一标识，随后只 inspect 同一清单。准备与补查日志为 `/tmp/domainry-K04-personal-a-model-boundary-create.log`、`/tmp/domainry-K04-personal-a-model-boundary-inspect.log`。

本地在途撤权断言最初错误地要求运行仍完成；实际代码更早以 knowledge_access_denied 终止，阻止复用已撤权的搜索证据。已改为接受这一明确拒绝状态，并新增无后续模型调用的断言；没有放宽生产授权或把任意网络失败作为成功。

## 检查与重现

- Agent 全量 `go test ./...` 通过：`/tmp/domainry-K04-agent-full.log`。
- 库配置、Identity / HTTP、成员管理、SaaS 和冻结恢复专项 race 通过：`/tmp/domainry-K04-permissions-race.log`。
- 补强后的在途撤权及模型调用边界 race 通过，53.044 秒：`/tmp/domainry-K04-model-boundary-race.log`。
- `go vet ./module ./internal/assembly/web` 通过：`/tmp/domainry-K04-vet.log`；diff 检查通过。未修改前端，无须重建前端来验证此配置变化。
- 生命周期与两种权限验收脚本共 8 项测试通过：`/tmp/domainry-K04-scripts-final.log`。覆盖中间态、私有读取与清理、失败删除、损坏清单、错误团队／来源、未就绪或已删除文件、重复 KB 及权限不匹配。

为三个不同 KB 分别创建私有合成文件，KB ID 来自实际控制台；凭证继续由现有私有配置加载器提供，不放入命令行或 JSON：

```sh
AGENT_KNOWLEDGE_KB_ID=personal-a-kb python3 scripts/knowledge-live-document.py --create --private
AGENT_KNOWLEDGE_KB_ID=personal-b-kb python3 scripts/knowledge-live-document.py --create --private
AGENT_KNOWLEDGE_KB_ID=shared-kb python3 scripts/knowledge-live-document.py --create --private
python3 scripts/test-agent-library-permissions-live.py /path/to/personal-a/manifest.json /path/to/personal-b/manifest.json /path/to/shared/manifest.json
```

未索引时只对原清单 `--inspect`。验收后分别用同一 KB 配置执行 `--cleanup /path/to/manifest.json`；所有本次清单已删除，不能直接重跑。私有上传、远端数据源管理入口和跨库移动仍按 K05 / K07 继续，K04 勾选不表示这些功能已经交付。
