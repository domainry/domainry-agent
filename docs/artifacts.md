# 会话成果：格式、版本与存储

当前已完成成果的格式 / 存储、应用服务、动作授权、来源复核及 Module / SaaS 公共接口，并通过真实 Identity / HTTP / SQLite 与远程接口测试。
六个成果工具已接入会话执行宿主；“我的成果”、操作确认预览与执行结果版本入口已接入网页。周报创建、修改第二节、下载旧版、刷新恢复及撤权 / 恢复已完成实际浏览器验证，详见 [成果网页验收](testing-2026-09-10-artifacts-web.md)。成果详情现可返回来源会话和来源消息／处理记录；来源绑定持久会话 Run，因此后台 worker 生成的成果可以回看原消息、工具步骤和保存回复。Markdown、表格和声明式图表的安全渲染及恶意内容 DOM 隔离见 [R03 验收](testing-2026-09-12-r03-artifact-web.md)。此外，真实 Verdent `gpt-5.6-sol` / Responses 已完成按实际待办状态生成周报、修改同一成果和下载原始版本的整段流程，见 [真实模型验收](testing-2026-09-10-live-daily-work.md)。通用后台任务的创建与查询继续由 L01–L03 交付。

## 已实现的契约

Agent SDK 新增 `ConversationArtifactService` 及其输入输出类型，覆盖成果列表、指定版本读取、版本列表、创建、编辑、导出和下载。
`ConversationArtifactStorage` 为宿主提供大正文的写入 / 读取接口；持久层记录中的正文引用及来源元数据不属于模型或浏览器输入。

首批内容采用三种结构：

- Markdown：保存 UTF-8 原文。局部编辑提供 `find` / `replace`，每次匹配必须恰好一次，多个编辑按顺序应用；找不到或存在歧义时拒绝整次修改。
- 表格：列声明稳定键、名称和 `text` / `number` / `date` 类型；单元格保存字符串或 null。金额与大整数不会先转换为浮点数，日期必须有效。局部修改按原始行号和列键定位，重复目标被拒绝。
- 图表：使用表格数据，加 `bar` / `line`、横轴列及数值系列列；不接收任意绘图脚本或 HTML。网页通过受控 SVG 渲染趋势，并提供保留原始精度的完整表格分页。

局部修改的 JSON 必须显式提供 `find` / `replace`，或 `row` / `column` / `value`。清空文字用空替换字符串，清空单元格用 `null`；漏填属性会被拒绝，不会隐式删除内容。

正文编码后的 JSON 最大 1 MiB；表格最多 64 列、10,000 行，图表最多 500 行、8 个数值系列。
这些是当前服务资源边界。更大数据集应由宿主分析 / 存储能力承接，不能将截断内容冒充完整成果。

## 持久化

迁移 6 `agent_conversation_artifacts` 追加以下表，原迁移内容保持不变：

| 表 | 用途 |
| --- | --- |
| `_agent_artifacts` | 用户范围内的稳定成果 ID、最新版本及查询元数据 |
| `_agent_artifact_versions` | 按成果 ID 和版本保存不可变正文、来源与元数据 |
| `_agent_artifact_mutations` | 客户端幂等键、输入摘要及结果回执 |
| `_agent_artifact_exports` | 导出所对应的版本、格式、内容哈希、到期时间和下载记录 |

用户范围由运行时、工作区和用户共同确定。创建与编辑在同一事务内保存最新版本指针、不可变修订和回执；编辑通过期望版本比较并更新。
旧创建请求重放会返回原始回执，不会回退当前版本。读取具体旧版本不会受后续编辑影响。
应用层按原始逻辑命令计算请求摘要；不同的局部修改即使产生相同正文，也不能共用一个幂等键。重试绑定原请求，不受宿主的导出有效期配置变化影响。
单用户当前限制为 1,000 个成果、每个成果 1,000 个修订，避免无限增长；保留与清理策略仍属于 H02 的未完成范围。

32 KiB 以内的正文内联保存。更大正文由应用层先写入宿主存储，数据库仅保存不可变引用、字节数及哈希。独立 Web 宿主已默认装配 `internal/infrastructure/artifactstorage`，目录为数据库绝对路径加 `.artifacts`；关闭 Web 宿主时关闭自有存储。宿主注入的存储由注入方管理生命周期。
该实现使用私有目录、用户范围子目录和内容哈希，原子发布完整文件。读取重新核对哈希并限制字节数；调用方只能提供不透明引用，不能指定文件路径。
崩溃留下的未引用对象和临时文件仍需接入保留与清理策略。

每个版本保存由可信应用层提供的 `ConversationSources`。创建接口的来源会话与运行 ID 必须同时提供或同时省略；来源绑定明确的 Run，不能在重试时改为会话最新运行。应用层核验该 Run 的知识与历史依赖，仓储检查来源属于当前用户；编辑保留原版本的来源依赖。

预览、编辑、导出和下载均复核当前来源访问权限，列表及版本列表也先检查再返回标题等元数据。受限项不返回，分页标记 `omitted`；来源恢复授权后可再次读取。来源策略复用现有知识工具的当前查询验证，尚未替代待完成的真实知识服务逐文档 ACL / 版本契约。

## 导出

已实现 Markdown 原文及表格 / 图表数据的 CSV 编码。CSV 保留十进制原文，正确处理逗号及引号；可能被电子表格解释为公式的文本加前导单引号，并在导出记录标明 `formula_guarded`，不修改原成果。

导出必须指定实际版本，保存文件类型、稳定文件名、字节数及内容哈希。默认到期时间为一小时，宿主可配置 1 秒至 24 小时；同一幂等请求不会重新延长有效期。
下载记录原子检查到期时间，累计次数并保存最近时间。应用层先核对当前动作权限、来源权限和指定版本的实际字节，再记录下载；被拒绝的请求不会增加下载次数。下载返回附件字节，使用 `private, no-store` 和 `nosniff`，不暴露宿主文件路径或正文存储引用。

## 已接通的公共接口

| 方法与路径 | 行为 |
| --- | --- |
| `GET /agent/artifacts` | 按标题、来源会话查询；支持 cursor / limit |
| `POST /agent/artifacts` | 创建成果；需要 client_id、title、content |
| `GET /agent/artifacts/{artifactID}?version=1` | 读取指定版本；省略或 0 表示最新 |
| `GET /agent/artifacts/{artifactID}/versions?before=3&limit=20` | 按版本倒序分页 |
| `PATCH /agent/artifacts/{artifactID}` | 提交 client_id、expected_version 与 patch |
| `POST /agent/artifacts/{artifactID}/exports` | 按 client_id、明确的 version 与 format 生成导出记录 |
| `GET /agent/artifact-exports/{exportID}/download` | 重新授权后下载绑定版本的原始文件 |

Module 从真实请求身份取得所有者。SaaS 客户端复用同一应用服务，通过受信任的服务协议传递身份；下载字节在 RPC 中使用 JSON 的字节编码，Module 的公共下载接口输出原始附件。

权限按 `artifact_list` / `artifact_read` / `artifact_versions` / `artifact_create` / `artifact_edit` / `artifact_export` 分开注册在 `agent.conversation_tools` 下，不自动授予账号。编辑、导出与下载还要求 `artifact_read`；导出和下载都要求 `artifact_export`。嵌入或自建 SaaS 服务必须注入当前授权器，大正文还需注入存储；不能因为有服务 API Key 就绕过这些要求。当前仓库的独立 SaaS 命令尚未装配个人工具授权宿主，这些成果接口在该命令中仍不可用。

创建 / 编辑 HTTP 请求最多 2 MiB，解码后的成果正文仍受 1 MiB 编码大小限制。SDK 的六个可选成果工具已接入 `PersonalConversationHost`；仅在授权器、成果正文存储与相应持久化能力齐备时开放。创建、修改和导出使用独立的 `personal_artifacts` 操作范围或具体操作确认，不能由个人记忆或待办范围授权。

工具写入与执行结果、完成事件在同一数据库事务保存。重试使用冻结参数与幂等键；来源引用记录生成步骤之前的边界，避免后续读取的私有资料追溯污染较早版本。公共创建接口指定来源时，来源 Run 必须已经完成。历史工具结果、成果预览和下载仍按当前权限复核。

## 测试证据

- [内容处理测试](../internal/artifact/content_test.go)：指定段落与单元格编辑、模糊目标拒绝、原文不变、十进制精度、格式 / 大小限制、CSV 公式防护及 UTF-8 校验。
- [版本存储测试](../internal/infrastructure/persistence/database/agent/conversation_artifact_store_test.go)：幂等回执、并发编辑只成功一个、版本分页、用户 / 工作区 / 运行时隔离、版本和回执失败回滚、来源不能跨用户或被编辑丢弃、指定版本导出、并发下载计数及到期拒绝。
- [私有正文存储测试](../internal/infrastructure/artifactstorage/files_test.go)：并发写入同一对象、重开存储后读取、所有者隔离、路径输入拒绝、内容损坏检测和跨所有者目录的符号链接拒绝。
- [应用与 SaaS 测试](../integration/conversation_artifact_integration_test.go)：大正文远程读写、指定第二节编辑、旧版本不变、原命令幂等、不同命令同结果仍冲突、导出期限重试稳定、用户 / 工作区 / 运行时隔离、读取与导出独立授权，以及来源撤权后列表 / 编辑 / 下载限制、恢复授权和宿主正文损坏。
- [真实 Identity HTTP 测试](../internal/assembly/web/conversation_artifact_test.go)：登录后显式授予权限、大正文创建、段落修改、二进制下载、撤销读取 / 导出权限、完整关闭并重开 Web 宿主后读取，以及 CSV 公式防护与十进制精度。这是 HTTP 集成测试，尚非成果网页操作验收。
- [工具 Schema 检查](../internal/application/conversation_artifact_test.go)：契约可编译，模型不能提供所有者 / 来源元数据 / 存储引用，数值单元格必须使用精确字符串。

公共接口阶段已通过：Agent / SDK 全量 `go test ./...`、`go vet ./...`、Go 格式检查，以及以下并发检查。这些记录早于本次会话工具与前端增量：

```sh
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent ./integration ./internal/assembly/web -run 'Artifact' -count=1
```

最新会话工具增量通过应用层、持久层与集成定向测试：

- [会话工具集成测试](../integration/conversation_artifact_tools_integration_test.go)：创建周报、读取、修改第二节、导出原版本、下一轮查找并修改同一成果、重启后确认及来源撤权。使用确定性模型夹具。
- [原子保存测试](../internal/infrastructure/persistence/database/agent/conversation_artifact_tool_store_test.go)：完成事件写入失败时成果与执行结果一起回滚；重试与重建仓储后读取原结果不重复创建。

本次前端与工具增量也已通过 Agent / SDK 全量测试、Go 静态检查、18 项前端状态测试与构建，以及成果相关的 race 检查。真实 Identity HTTP 工具测试覆盖完整宿主重启后的确认恢复、原版本导出和创建结果的当前读取权限；race 下的等待窗口调整及实际浏览器过程见 [验收记录](testing-2026-09-10-artifacts-web.md)。

网页提供按标题 / 来源会话查找、确定版本预览、Markdown 正文或表格单元格修改、版本冲突提示及 Markdown / CSV 下载；失败重试保留原目标、版本与请求身份。下载后核对字节数与哈希再交给浏览器保存。Markdown 不执行原始 HTML，脚本链接被拦截，外部图片仅显示占位文字。

R01–R04 已完成。R03 补验确认来源绑定成果可回到原会话和持久 Run，当前来源权限撤销后列表即时隐藏；安全预览不会执行原始 HTML、远程图片或脚本链接。通用后台任务工具和独立任务目录仍属于 L01–L03。

真实模型联调还修复了写入边界：在 `prepareArtifactTool` 阶段发现不存在的 ID、无效编辑或无法读取来源时，尚未提交成果版本或导出记录，应返回明确失败，让模型查询实际对象后纠正；不能误判为外部写入结果不明。进入实际事务后的不确定错误继续保留核查机制。错误 ID 后纠正的编辑 / 导出流程已有集成回归，确认失败调用不生成版本。
