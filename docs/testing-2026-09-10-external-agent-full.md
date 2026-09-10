# Agent 与外部账号完整验收（2026-09-10）

本轮对象为当前已实现的 Agent 能力、实际外部账号接入，以及 Runtime 已交付的业务查询、关联与动作。个人 Workspace、真实模型、知识库和网页均实际运行。核心回归通过；整体不能标为全部通过：当前配置模型在业务关联查询中产生错误参数，虽然经目录重查后完成正确查询，严格的无错误调用验收仍失败。

本轮按代码和当次执行记录判断，不以历史文档的 PASS 代替当前测试。其他任务正在同一源码目录开发工作流；尚未交付的新增流程能力不计入本轮通过项。清单快照含 81 个编号：37 个已勾选、44 个未勾选；未勾选项包含部分实现，不能一律当成未开发，也不能当成已验证完成。

本机完整证据保存在 `~/Library/Application Support/domainry-agent/external/verification-2026-09-10/`，包含通过及失败日志、JSONL、关联查询公共 Run、已清理的合成文档 manifest 和清单快照。复制前确认日志中没有当前服务凭证值。源目录 `/tmp/domainry-agent-full-verification/` 为临时工作记录。

## 环境与凭证

- 当前入口：`https://domainry.verdent.ai:8443/`，本机仅一个持续运行的 Agent 实例。
- 外部账号通过现有 Passport token 校验协议验证 Cookie；账号服务域名、Cookie 名、登录 URL、请求与响应映射均来自私有部署配置。
- 每个外部用户一个个人 Workspace；安装目录作用域不代替用户 Workspace；外部宿主没有创建本地 Identity 账号表。
- 实际模型为私有服务配置指定的 `glm-5.3-flash-free` / `chat_completions`。本轮未为获得 PASS 更换模型。
- 模型凭证存放在本机私有 `web-credentials.json`，权限 `0600`。启动及验收脚本支持复用，显式进程环境优先；凭证不进入仓库或验收报告。
- 知识库使用专门创建并确认原先不存在的合成文档；验收后仅清理该文档，并验证远端返回不存在。

## 当前回归结果

| 检查 | 当次结果 | 证据文件 |
| --- | --- | --- |
| Agent 全量 `go test -race -p 2 -tags external_identity ./...` | 382 个测试/子测试通过，4 个真实服务测试在普通回归中跳过；跳过项已另行全部实际执行通过 | `agent-tests-verified.jsonl` |
| Agent SDK 全量 race | 22 个测试/子测试通过 | `agent-sdk-tests.jsonl` |
| Bridge 完整检查 | Go race、vet、3 种配置检查、3 项浏览器客户端测试通过 | `bridge-check.log` |
| Bridge 与实际 Runtime | 实际模块、持久化及外部身份专项通过 | `bridge-runtime.log` |
| Plane 交付集成 | 代码生成、源码收尾、交付、绑定四包通过 | `bridge-plane.log` |
| Agent 静态检查与构建 | vet、外部身份构建与普通入口构建通过 | `agent-vet.log` |
| 前端 | 20 项状态测试及生产构建通过；构建仍有现有包体积提示 | `frontend-tests.log`、`frontend-build.log` |
| 本地凭证/合成资料脚本 | 3 项测试通过，含文件权限、环境优先、拒绝非法配置、索引等待及重复清理 | `python-tests.log` |

测试/子测试计数包含 Go 子测试，不代表独立产品功能数。普通回归中四项真实服务测试跳过的原因是显式 opt-in；以下真实服务证据补齐了这些检查，不能把跳过本身称为通过。

## 功能覆盖

| 功能组 | 当前验证 | 结果与边界 |
| --- | --- | --- |
| 外部账号与个人 Workspace | 真实浏览器 Cookie 登录、刷新、宿主重启；两个夹具账号的数据隔离、伪造 Workspace、旧 scope、禁止本地刷新、SSE、后台工具授权 | 通过。真实账号的首次创建和重启复用已核对持久化数据；双用户负向覆盖使用真实 Bridge + TLS 校验夹具 |
| 会话与执行 | 创建、列表、搜索、改名、归档、删除、分页；幂等、租约、fencing、取消、恢复、模型失败、已完成写入不重复 | 全量自动化通过；真实网页覆盖创建、刷新及具体确认恢复。未在真实账号上执行不可恢复的删除来替代夹具验证 |
| 工具与确认 | 参数校验、目录授权、执行账本、步骤与事件原子落库、ask_user、确认/拒绝、撤权、恢复时重查、大结果引用 | 当前实现的自动化通过；具体资源授权范围、全部外部副作用取消语义仍按 TODO 保留 |
| 时间、计算、待办 | 真实模型创建有序待办、修改第二项期限、继续处理同一事项；真实外部账号查询上海日期、计算 200.00 元、确认新建、完成事项 | 通过；真实页面刷新后仍保留待确认参数，列表中核对实际结果 |
| 历史、记忆、长会话 | 真实模型历史检索、保存格式、完整重启续办；17 次回复、11 次摘要，默认 64 KiB 预算下保留更正、事实与未完事项，原始 34 条消息保留 | 通过。证据：`live-daily-work.log`、`live-model-long.log` |
| 成果 | 创建、读取、编辑、版本、确认、导出、所有者隔离、并发冲突、损坏内容拒绝；真实模型创建报告、编辑并下载原始版本字节 | 通过，真实日常工作验收 190.68 秒。完整跨知识/任务关联仍按 TODO 保留 |
| 知识 search/read、引用和历史保护 | 真实模型检索与读取合成文档、原文事实、引用、SSE、重启、工具撤权/恢复；实际外部账号打开引用原文；删除文档后刷新隐藏失效回复 | 已实现路径通过。真实私有文档 ACL 未覆盖，文档权限与工具权限不能混同。证据：`live-knowledge.log`、合成资料生命周期日志 |
| Runtime 业务目录/查询 | 实际 Identity + Runtime + Agent HTTP、三页游标、详情、字段与读取撤权、重启；真实模型再次复核 | 通过复核。初次真实模型运行发生 `business_request_invalid`，复核运行无失败调用，失败记录保留 |
| Runtime 关联查询 | 真实 Runtime 的客户→订单→项目→客户、逐页、行/字段权限、重启；真实模型 2 次严格验收 | 确定性夹具通过；真实模型严格验收失败，详见下节 |
| Runtime 业务动作 | 实际模块确认/重启/重复确认/冲突/撤权专项；真实模型创建、改名、停用同一合成客户 | 通过。真实模型验收 200.85 秒；当中一次编译因其他任务尚未完成的源码引用失败，源码修正后重新编译并实际跑完 |

关键自动化用例包括 `TestExternalAccountAgentOwnershipAndRestart`、`TestConversationExecutionFreezesEachStepAndNeverReplaysCompletedWrites`、`TestConversationInteractionWaitFencesWorkerAndResponseIsDurableAndIdempotent`、`TestPersonalTodoBatchEffectReceiptAndEventAreAtomic`、`TestArtifactsSaaSRoundTripVersionsReceiptsAndDownloadAuthority`、`TestKnowledgeProvenanceProtectsDerivedRepliesSSEAndHistoryAcrossRestart`，以及 Runtime 的三组实际 Web 组合用例。完整测试名称与结果保存在 JSONL 中。

## 发现与修正

1. **外部个人 Workspace 的知识访问被固定安装 Workspace 拒绝。** 增加由可信宿主提供的当前 Workspace 归属校验；每次调用重新解析实际用户和 Workspace，再生成文档权限。普通固定 Workspace 模式保持原有约束。补测两位所有者、跨 Workspace 伪造、错误 Runtime、未知/撤销用户，随后在真实外部账号网页完成检索、原文引用和源删除验证。
2. **真实服务凭证不能自动复用。** 增加私有配置加载，限制字段、所有者和文件权限，环境优先；连接现有模型和知识服务。未把凭证写进源码。
3. **race 下观察窗口过短。** 个人记忆和知识夹具原有 5/8 秒观察窗口在真实授权目录与来源复核完成前结束。失败状态显示写入/工具已完成，Run 尚在生成或提交收尾；调整测试观察窗口为 20 秒，正确性与生产超时不变。最终全量 race 通过。
4. **合成资料脚本提前结束索引等待。** 远端真实出现 `CHUNKED` 中间状态，加入等待集合并覆盖该转换；原文随后成功索引，没有重复上传。
5. **失败的真实业务运行没有保存完整公共证据。** 将合成 Run 投影保存提前到断言前，失败时记录工具名、参数与错误码，保留原来的严格断言。

## 未通过项：真实模型的关联查询稳定性

诊断运行最终状态为 `completed`，确实返回两条订单（100、200）、Delivery Project 和 Acme；但其中包含两次错误调用：

- `query_records` 把目录的运算符改成 SQL 符号 `=`，返回 `business_request_invalid`。
- `query_related_records` 猜测 `reverse:project:customer`，返回 `business_access_denied`；随后重查项目目录，使用实际发布的 `forward:customer` 完成查询。

服务端没有放宽权限，也没有返回被拒绝的业务内容。模型能够在同一 Run 内恢复，但当前严格用例要求没有失败调用，因此仍记为失败。本轮没有为得到绿色结果放宽断言或更换默认模型。证据为 `live-business-relations-diagnostic.log` 及对应 `business-relations-run.json`。

## 尚未完成的产品范围

清单未勾选编号：A03、A08、B06、C03、C04、E05、K01、K04–K07、F01–F06、J05、N01–N03、R03、L01–L03、G01–G06、H01–H04、H06–H09、X01–X05。

这些项包括目录/权限的完整覆盖、私有文档 ACL、附件入口、外部账号工具、网页检索、流程启动与查询、报表分析、后台任务、提醒调度、配额与清理、发布部署和可选扩展。部分已有基础实现；本轮不把未交付部分虚报为验证通过，也不把独立 Agent 页面宣称为已接入全部 Runtime 业务能力。

当前独立页面开放个人、成果和已配置知识工具。Runtime 业务能力通过实际 Runtime 组合宿主独立验收；当前这个独立网页没有业务数据宿主，不能直接查改 Runtime 的真实业务记录。

真实网页额外完成了「个人记忆与成果验收」：补充确认、保存明确标记为合成验收的记忆、创建 Markdown 成果。中途页面检测到登录状态变化，通过配置的账号中心入口重新进入后，原 Workspace、会话和待确认内容仍保留；未重新创建本地账号或 Workspace。

最后从「我的成果」实际下载版本 1，浏览器显示下载完成；本机下载的 85 字节文件 SHA-256 与持久化导出回执一致。数据库最终为 1 个 Workspace、3 个会话、3 个完成的 Run，以及各 1 条合成记忆、已完成待办和成果；所有者范围一致，本地 Identity 账号表为 0。核查结果保存于 `external-browser-acceptance.json`。
