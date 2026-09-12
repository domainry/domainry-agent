# 远程知识库文档检索（bcri）

> 2026-09-11 范围更正：本地原文件解析 / 持久正文缓存 / 私有附件模型提取已按用户要求移除；下文相关历史验收不代表当前能力。检索继续由 Connector 提供，原文件在线预览已完成 K06 验收。详见 [移除与升级说明](document-parsing.md)及 [K06 当前 TODO](agent-capabilities-todo.md)。

依据接入时读取的知识库控制台中 bcri 详情页接入。实际地址保存在仓库外的服务配置中。

## 代码归属

- `domainry-connectors/providers/knowledge_base/http_api`：唯一的知识库 HTTP 协议实现，提供 `search` / `fetch`、原文件推送、索引状态和删除，包含配置校验、来源保留和上游错误分类。通过 Connector SDK 接收宿主 Transport；自身不创建 HTTP Client、不读取环境密钥。
- `domainry-agent/internal/application/conversation_knowledge.go`：决定何时检索、如何纳入上下文预算和如何随模型输入冻结结果。仅依赖检索接口。
- Agent 的 `internal/infrastructure/provider/knowledge.go`：将当前认证身份、工作区和服务端权限绑定到同一个官方 Connector，通过 SDK 类型化调用复用 Provider。
- `internal/application/knowledge_library_source.go`：根据当前成员关系选择宿主配置的资料库来源，检查当前 Identity 动作、归档状态和历史访问；远端连接仍复用上述 Provider。
- Agent 独立部署通过 `internal/infrastructure/connectortransport` 提供限定目标地址、超时和响应大小的 HTTP Transport；Runtime 嵌入时可通过 `module.Options.Knowledge.Transport` 注入 Runtime 已有的受治理 Transport。无需为浏览器对话启动完整 Runtime。

本地 `go.work` 已包含相邻的 `domainry-connectors` 源码，CI 同步检出该仓库。新 Provider 尚未发布到已有的 `domainry-connectors v0.1.0`；脱离本地源码工作区独立发布 Agent 前，需要先发布包含本 Provider 的 Connectors 版本并更新依赖。

## 接口

- `POST /v1/kb/search`：`team_id`、`kb_id`、`query`、`top_k`，可选 `permission_ids`、`result_content: "metadata"`。
- `POST /v1/kb/fetch`：`team_id`、`kb_id`、`doc_id`，使用与检索相同的权限。
- 文档生命周期另有 `put_document` / `document_status` / `delete_document`，见文末；这些是宿主接口，尚未公开为网页入库能力或模型写工具。
- 两者均为 `Authorization: Bearer <platform API key>`，不是模型 API 使用的 `x-api-key`。
- 不提供 `permission_ids` 或提供空数组时，只返回团队可见文档。非空权限必须由应用服务端依据用户真实权限计算；不是模型生成，也不是浏览器提交。

控制台接入示例没有完整响应字段契约。因此 Connector 校验并保留上游 JSON，增加 `provider`、`kb_id`、`result` 外层。2026-09-10 已验证成功空结果、合成文档推送及索引后的非空 search / fetch，取得 4 个实际片段；Agent 的引用映射依据这次真实响应配置，见下文。私有文档 ACL 尚未验收。

同日真实 fetch 确认：不存在文档返回 HTTP 200、`err_code: 1004`、`err_msg: "document not found"`。Connector 已识别业务状态，Agent 将其映射为 `knowledge_not_found`；错误响应不生成知识凭据和引用。模型收到失败结果后可以重新搜索，历史 / SSE 保存具体失败状态但不包含上游错误正文。成功空结果仍作为成功检索处理。已有来源在复核时返回该错误，也不能继续使用旧依据。相关 Provider 和持久会话集成回归已通过，见 [真实知识服务错误验收](testing-2026-09-10-knowledge-business-errors.md)。

## 启用 bcri

通用服务默认关闭检索；仅配置模型密钥不会开启检索。在已有模型配置上增加：

```sh
export AGENT_KNOWLEDGE_BASE_URL='https://knowledge.example.com' # 替换为实际知识服务地址
export AGENT_KNOWLEDGE_TEAM_ID='1470194374940573696'
export AGENT_KNOWLEDGE_KB_ID='kb-3bbd8f1d3249'
# Identity web 默认工作区；若修改了 AGENT_WEB_WORKSPACE_ID，这里必须一致。
export AGENT_KNOWLEDGE_WORKSPACE_ID='agent-workspace'
export AGENT_KNOWLEDGE_TOP_K='5'
# 密钥在服务进程环境中设置，不写入仓库或前端。
# 配置 AGENT_KNOWLEDGE_API_KEY；也可使用服务端共享 AGENT_PROVIDER_API_KEY。
python3 scripts/run-agent-web.py
```

Playground 的工作区为 `playground-workspace`。独立 SaaS 则填写接入方认证身份中的真实 `workspace_id`，并照常设置 `AGENT_SAAS_RUNTIME_ID`。工作区不匹配时，检索在发出网络请求前拒绝访问。不要把 Agent 工作区 ID 与 知识服务的数字 `team_id` 混用。

| 配置 | 行为 |
| --- | --- |
| `AGENT_KNOWLEDGE_TEAM_ID` | 知识服务团队 ID，启用时必填 |
| `AGENT_KNOWLEDGE_KB_ID` | 远程知识库 ID，启用时必填 |
| `AGENT_KNOWLEDGE_WORKSPACE_ID` | 允许检索的 Agent 工作区，启用时必填 |
| `AGENT_KNOWLEDGE_API_KEY` | 知识库独立密钥；可回退到 `AGENT_PROVIDER_API_KEY` |
| `AGENT_KNOWLEDGE_BASE_URL` | 服务根地址，启用时必填 |
| `AGENT_KNOWLEDGE_TOP_K` | 默认 5，本地限制 1–20；不代表上游的最大值 |
| `AGENT_KNOWLEDGE_RESPONSE_MAPPING` | 可选 JSON，显式声明 search / fetch 返回字段到结构化引用的映射；未配置时保留原始结果而不生成引用 |

模块宿主通过 `module.Options.Knowledge` 配置同一适配器；`PermissionIDs` 回调接收认证后的 `ConversationAuthority`，可供已有权限系统返回精确权限 ID。结果由宿主写入本次 Connector Connection 的 `permission_ids_by_user` 策略，不进入可由用户控制的 operation payload。没有映射时只查团队可见文档。`module.ConversationOptions.KnowledgeBytes` 控制纯文本路径固定检索消息的 JSON 字节预算，默认总上下文预算的四分之一；工具路径通过工具结果大小限制、执行上下文预算和 `tool_result_read` 控制输入。

## Web Agent 的自主工具与当前权限

配置知识服务、支持工具的模型以及宿主授权器后，Conversation 装配 `knowledge_search` / `knowledge_read`，由模型按需调用；此模式不再在每轮回复前额外固定搜索。现有个人工具、具体操作确认和交互授权通过宿主组合保留。只有旧 `Search` 接口而没有结果复核能力的适配器不能装配到工具模式，启动时明确报错；原纯文本路径保持兼容。

默认知识源的两个 v1 工具只接受 `query` 或 `doc_id`。启用下文的资料库绑定后切换到 v2，额外接受可选的 `library_id`。服务端固定 origin、team、kb 和权限范围，拒绝模型传入 `permission_ids` 等额外参数。成功结果保存 `provider`、`kb_id`、操作、原查询 / 文档 ID、来源范围摘要及上游 `data`；按库检索还保存 `library_id`。不把任意 JSON 字段臆造为标题、页码或完整文档。结果进入原有工具账本，支持恢复、压缩预览和完整结果读取。

独立 Identity Web 宿主支持以下服务端配置：

```sh
export AGENT_WEB_KNOWLEDGE_PERMISSIONS='{"finance":"dept:finance","engineering":"dept:engineering"}'
```

这是示例映射，必须换成知识服务中实际存在的权限 ID。宿主注册 `agent.knowledge_documents.finance` / `agent.knowledge_documents.engineering` 等 Identity 权限；管理员分别授予相应文档组权限和 `agent.conversation_tools.knowledge_search` / `knowledge_read` 工具权限。注册不会自动授予权限。每次请求重新解析当前 Identity 主体，只有当前获准的映射值进入 Connector。两个本地配置键映射到同一个上游 ID 时去重。自定义宿主仍可提供 `PermissionIDs` 回调，不能同时配置两种解析来源。

文档组映射是本应用明确配置的授权能力，不能自动把任意业务行级权限翻译为文档权限。上游负责其实际文档 ACL；需要更细的范围时应配置对应的上游权限 ID 或提供宿主映射实现。

用户已确认采用“个人资料＋共享资料库”，文档默认继承资料库成员权限，不做组织树继承。角色、标识与同步方案见 [个人资料与共享资料库权限](knowledge-permissions.md)。资料库与成员管理已接公共接口、Identity 和网页，并通过 [管理验收](testing-2026-09-10-knowledge-libraries.md)；成员范围到独立远端 KB 的检索已经接通，见 [按库检索验收](testing-2026-09-10-library-knowledge.md)。资料库默认私有标识与新文档 ACL 写入已接通，远端 ACL 更新 / 迁移仍待 K07。检索请求不填权限只查团队可见资料，不能等同于读取全部受限文档。

工具结果被用于下一次模型请求、恢复、`execution_read` 或 `tool_result_read` 时，重新检查当前动作权限和来源数据权限。现有上游文档没有可验证的 ACL / 版本凭据或响应字段契约，因此当前 Provider 用**同一请求、当前权限重新查询并比较完整规范化 JSON**。保留数值精度，字段顺序 / 空白不影响比较；来源配置或所有者不符、上游拒绝、不可用、内容变化均不能复用旧结果。不会用新结果悄悄替换冻结输入。

这会产生额外读取；结果排序、动态元数据或合法内容更新也可能使旧运行报 `knowledge_source_changed`，应重新生成。后续 K01 / K03 确认真正的来源字段、文档版本及 ACL 接口后，应以可验证的逐文档权限检查替换全响应比对。当前结果复核契约允许替换该实现，不需要改变执行账本。原先工具模式中固定注入、没有复核凭据的旧知识输入不能直接恢复，应新建运行。

历史回复、摘要和普通后续回复现在保留服务端来源引用；Run / 消息 / SSE 读取与继续执行前检查当前来源权限。来源不可读取时隐藏相关内容，摘要从可读取的原始消息重建；旧历史工具结果缺少 Run ID 时按原始消息定位来源。原始账本不被读取过滤改写，权限恢复后重新检查。

网页权限最初使用本地模型与知识服务协议夹具、真实 Identity / HTTP / SQLite 验证，见 [来源权限与浏览器验收](testing-2026-09-10-sources-and-browser.md) 和 [知识引用验收](testing-2026-09-10-knowledge-citations.md)。随后已通过真实 Verdent 文档命中、读取、模型引用、宿主重启、Identity 动作撤权 / 恢复及远端文档删除后的隐藏，见 [真实知识库验收](testing-2026-09-10-live-knowledge.md)。远端私有检索已通过 K01 / K04；新上传私有链路见 [K05 增量验收](testing-2026-09-11-private-library-upload.md)。会话附件已接本地私有保存、管理接口和网页，见 [附件验证](testing-2026-09-10-attachments-web.md)；资料库文档归库、索引和删除过滤已接后端，见[应用链路验收](testing-2026-09-10-managed-documents.md)；资料库文档 UI 已通过[网页验收](testing-2026-09-10-library-documents-web.md)；数据源入口和独立多库上传已完成 [K05](testing-2026-09-11-knowledge-datasources.md)，原件在线预览已完成 [K06](testing-2026-09-11-original-preview.md)，共享移动仍在 K07。H02 仍需完成派生记忆 / 待办 / 成果的访问与保留策略，不能把读取检查等同于撤回用户已获得的副本。

## 在网页连接知识源

K05 已接网页知识源入口。宿主预先批准独立远端 KB，资料库管理者在“资料库 → 具体资料库 → 知识源”选择并连接。连接成功立即启用文档上传，不需要再为该资料库重启宿主。编辑者与阅读者不能维护绑定；管理者也必须通过当前 Identity 动作授权。

Module 使用 `module.Options.KnowledgeDatasources []module.KnowledgeDatasourceConfig`；环境使用 `AGENT_KNOWLEDGE_DATASOURCES`。两种装配方式二选一。例如：

```sh
export AGENT_KNOWLEDGE_DATASOURCES='[
  {
    "key": "project-source",
    "name": "项目资料知识源",
    "description": "供一个资料库使用",
    "workspace_id": "agent-workspace",
    "base_url": "https://knowledge.example.com",
    "team_id": "actual-team-id",
    "kb_id": "dedicated-api-push-kb-id",
    "api_key_env": "AGENT_KNOWLEDGE_API_KEY",
    "response_mapping": {
      "search": {"items":"/data/hits","many":true,"doc_id":"/doc_id","title":"/title","excerpt":"/snippet"},
      "fetch": {"items":"/data/chunks","many":true,"metadata_object":"/data","doc_id":"/doc_id","title":"/title","excerpt":"/content"}
    }
  }
]'
```

替换实际工作区、地址和远端 ID；上面的映射须按部署核对。密钥放在服务端变量中，不在 JSON 中填写。`scripts/run-agent-web.py` 也会读取私有服务配置中的同名字符串设置。宿主目录配置变更需要重启，网页中已有目录项的首次绑定不需要重启。默认不填 `permission_ids` 时会生成私有库范围，绝不表示全员公开。

`GET /agent/knowledge-libraries/{libraryID}/sources?after=…&limit=…` 返回工作区内的目录名称、是否可连接、绑定状态及分页；`PUT /agent/knowledge-libraries/{libraryID}/source` 只接受 `datasource_key` 和 `expected_revision`。可选服务契约为 `agentsdk.KnowledgeDatasourceService`，宿主来源契约为 `agentsdk.KnowledgeDatasourceCatalog`，SaaS 同样支持。

来源物理范围不得与默认知识源、启动绑定或其他目录项重复。绑定存入第 11 个 ORM 迁移建立的表；同一远端 KB 被绑定后即被保留占用，归档资料库或删除全部文档也不会释放。当前不支持网页创建远端 KB、编辑密钥、换绑或自动迁移旧 ACL。移除 / 改变配置会停用不匹配的持久绑定，原文件保留，恢复原配置可恢复读取。见 [K05 完成验收](testing-2026-09-11-knowledge-datasources.md)。

## 通过启动配置固定绑定个人 / 共享资料库

先通过资料库窗口或 `POST /agent/knowledge-libraries` 创建库，取得实际的 `lib_…` ID，再由宿主管理员配置独立远端 KB。创建空库不会自动创建远端 KB，也不会上传文档；网页目前不编辑连接凭证。

Module 宿主使用 `module.Options.KnowledgeLibraries []module.KnowledgeLibraryConfig`，每项包含 `LibraryID` 和独立的 `KnowledgeConfig`。环境配置等价形式如下，所有占位 ID 和地址须替换；`api_key_env` 填服务端已配置的密钥变量名称，JSON 中不接受原始密钥：

```sh
export AGENT_KNOWLEDGE_LIBRARY_BINDINGS='[
  {
    "library_id": "lib_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "workspace_id": "agent-workspace",
    "base_url": "https://knowledge.example.com",
    "team_id": "actual-team-id",
    "kb_id": "dedicated-kb-id",
    "api_key_env": "SHARED_LIBRARY_API_KEY",
    "permission_ids": ["actual-upstream-library-read-id"],
    "top_k": 5,
    "response_mapping": {
      "search": {"items":"/data/hits","many":true,"doc_id":"/doc_id","title":"/title","excerpt":"/snippet"},
      "fetch": {"items":"/data/chunks","many":true,"metadata_object":"/data","doc_id":"/doc_id","title":"/title","excerpt":"/content"}
    }
  }
]'
```

响应映射仍需按实际部署核对。JSON 上限 256 KiB，最多 1,000 条绑定；程序配置和 JSON 二选一。绑定会在启动时装配，修改后重启。低层自定义宿主也可用 `module.LibraryKnowledgeBinding` 设置 `ConversationOptions.LibraryKnowledge` 注入源，但必须负责实际来源隔离，并提供实时 `LibraryAuthorizer`、支持工具的模型、工具授权器及资料库存储；缺少这些能力会启动失败。

每个本地库必须独占一个远端 KB，不能与默认 `AGENT_KNOWLEDGE_*` 来源或其他库复用同一 origin / team / kb；启动检查会拒绝可识别的重复范围。原因是上游检索始终可能包含团队可见文档，单靠不同的本地 ID 或 permission_ids 无法隔离共享 KB。程序配置可为单库追加可信 PermissionIDs 回调，但不能将它用于绕开独立 KB 边界。

`KnowledgeLibraryConfig.PermissionIDs` 与 JSON `permission_ids` 现在支持按库配置已经存在的上游阅读标识。当前最多 100 个 ID，每个不超过 128 UTF-8 字节；拒绝空白边界、控制字符及非法 UTF-8，排序去重后保存独立副本。不能与 `Knowledge.PermissionIDs` 回调同时配置。每次请求仍先检查当前成员和 Identity，再由服务端写入 Connector 的用户权限策略；这些 ID 不接受模型或浏览器输入。阅读者、编辑者和管理者获得相同库内阅读范围，动作权限分别验证。成员退出时本地立即停止后续检索和旧来源复用，不需要逐文档改写远端 ACL。

固定 `permission_ids` 现在同时用于文档上传与阅读；省略时，Module 从可信 Runtime / Workspace / Library 元组生成稳定私有库标识。关闭 `manage_documents` 仍保留同一阅读范围；动态权限回调不能用于托管写入。上传以持久请求 ID 和权限摘要约束，旧任务遇到配置变化时保留原件并停止远端处理；不会自动修改既有文档 ACL。见 [K05 私有上传增量](testing-2026-09-11-private-library-upload.md)。数据源网页入口已完成 [K05](testing-2026-09-11-knowledge-datasources.md)，跨库移动 / ACL 更新在 K07。

使用流程为 `knowledge_libraries` → 带返回库 ID 的 `knowledge_search` → 同一库 ID 的 `knowledge_read`。目录可能返回空页且有 next_after，需要继续翻页。个人库只有所有者能访问，共享库按当前成员判断；归档库不提供检索。没有 library_id 时仅调用单独配置的默认源，未配置默认源则返回 `knowledge_library_required`，不会退回任意库。

除了库内成员身份，还需授予 `agent.conversation_tools.knowledge_libraries`、`knowledge_search`、`knowledge_read` 及 `agent.conversations.libraries_get` 对应 Identity 动作；页面管理使用原有七个资料库动作。注册权限不自动授予。目录、请求前后、历史结果和冻结输入复用都重新检查；成员移除或资料库读取动作撤销后，请求远端前即拒绝。恢复后使用原历史快照重新验证，不改写冻结内容。

来源凭据与引用携带资料库 ID，网页详情的“已配置知识源”只表示连接存在，不能证明远端在线或文档完成索引。此前已通过实际 Identity / HTTP / SQLite 与网页夹具验证；真实 Verdent 三库检索权限现已完成 K04，产品私有上传 / 索引 / 重启 / 引用 / 清理的单库链路已完成 K05 增量。数据源配置入口与独立多库产品上传现已完成 [K05](testing-2026-09-11-knowledge-datasources.md)；跨库共享 / 移动与远端 ACL 同步仍在 K07。

## 结构化引用映射

Agent SDK 的 `ConversationCitation` 保存来源 ID、知识库 / 文档 ID、标题、链接和实际取得的片段；按库检索额外保存 `library_id`。宿主通过 `module.Options.Knowledge.ResponseMapping` 或上述环境配置声明 JSON Pointer；`items` 指向结果，`many` 显式区分数组，其他指针默认相对每条文档对象解析。fetch 可用 `metadata_object` 指向响应内共用的文档对象，文档 ID / 标题 / 链接在该对象解析，片段仍在每条 item 解析；显式空指针表示响应根对象。search 必须映射 `doc_id`；fetch 未声明该字段时使用实际请求的文档 ID。标题、链接和片段可选，不补造缺失值。

2026-09-10 合成文档真实调用确认的映射如下；宿主须按实际部署的响应验证配置，不作为任意知识服务的默认字段：

```json
{
  "search": {"items": "/data/hits", "many": true, "doc_id": "/doc_id", "title": "/title", "url": "/source_url", "excerpt": "/snippet"},
  "fetch": {"items": "/data/chunks", "many": true, "metadata_object": "/data", "doc_id": "/doc_id", "title": "/title", "url": "/source/source_url", "excerpt": "/content"}
}
```

实际上传文档返回的 `source_url` 为 S3 地址，不是用户可打开的网页；引用中不展示该链接。`anchor_ref` 为字符串，当前没有将其解释为页码或单元格位置。fetch 的 `status: PENDING` 与空 chunks 不代表已有可读正文；本次等到 `INDEXED` 且 chunks 非空后才做引用验收。

映射缺失时原始 JSON 仍可使用；明确配置但集合 / 元数据对象 / 字段类型不匹配时返回映射错误，缺失可选文本字段保持为空。最多生成 50 条来源，片段按 UTF-8 限制为 2048 字节并注明截断，引用 JSON 总量限制为 192 KiB。来源链接仅接受不含用户信息的 HTTP(S) 地址。

引用随完成的工具结果进入账本、SSE 和历史消息。模型使用 `[[cite:ID]]` 标记；网页仅为该次已完成知识调用返回的 ID 生成有效引用按钮。打开按钮可检查实际片段，未知标记显示为未验证。读取历史资料时继续执行当前来源权限复核，引用元数据也须与当前来源匹配。

## 纯文本兼容路径的行为

1. 每个新回复用本轮用户原文检索一次；不把个人记忆、完整历史或摘要作为查询发送给检索服务。
2. 将完整检索 JSON 标为不可信来源数据，加入现有上下文预算和历史压缩流程。提示模型按返回的标题、URL 或 `doc_id` 引用来源，不编造来源，不把片段说成已读取全文。
3. 原有 `ModelInput` 存储在调用模型前冻结该输入。恢复已有输入、读取相关历史或重建摘要时，会用原查询及当前权限复核来源；复核不替换已冻结的输入，来源变化或不可读取时不继续使用旧内容。因此这些操作可能产生额外检索。
4. 空结果照常交给模型，明确不能声称存在文档依据；检索故障以独立的 `knowledge_*` 错误结束本轮，可重新生成，不伪装成搜索成功。超出检索预算时明确失败，不截断 JSON 或静默丢弃用户输入。
5. 文本和流式回复共用这条路径，三种模型协议保持兼容。API Key 留在服务端。

纯文本路径不自主选择 `doc_id`；工具模式通过 `knowledge_read` 复用 `Knowledge.Fetch`。浏览器资料库窗口已接入文档上传、状态、下载和删除；写入由用户明确操作，不是模型工具。

## 文档进入知识库

接入时控制台显示 bcri 索引文档数为 **0**。文档必须先写入并完成索引，才能检索到实际内容。控制台给出的写入接口为：

```sh
curl -X POST "${AGENT_KNOWLEDGE_BASE_URL}/v1/kb/kbs/kb-3bbd8f1d3249/documents?doc_id=my-doc-001&filename=guide.pdf" \
  -H "Authorization: Bearer ${AGENT_KNOWLEDGE_API_KEY:-$AGENT_PROVIDER_API_KEY}" \
  -H 'Content-Type: application/octet-stream' \
  --data-binary @guide.pdf
```

同一来源文档使用稳定的 `doc_id`；再次推送相同 ID 更新索引内容。控制台同时给出 `DELETE /v1/kb/kbs/{kb_id}/documents?doc_id={doc_id}`，使用相同 Bearer 认证。实际模型验收只推送专门创建的合成资料，并在验证结束后删除该测试 ID。产品内会话上传目前仅保存私有原文件，不调用远端推送；资料库文档采用单独的托管入库接口和索引任务，会话附件已可显式另存到配置完成的资料库；私有 ACL、跨库移动和真实多库仍在 K04–K07。

2026-09-10 已将推送 / 索引查询 / 删除从验收脚本所用协议补到官方 Connector，Agent 通过可选 `KnowledgeDocumentSource` 宿主端口调用。真实联调已完成新合成文档推送、PENDING → CHUNKED → INDEXED、实际正文读取 / 检索和删除后核查，见 [文档协议验收](testing-2026-09-10-knowledge-document-protocol.md)。原 search / fetch 契约保持不变。

文档推送采用原始二进制，Verdent 上限为 10 MiB；通过可选 SDK 限制端口在预留记录之前校验，网页从库接口取得具体上限。中文文件原名保留在 Agent，远端使用稳定 ASCII 文件名并保留后缀。`KnowledgeConfig.DocumentManagement` 为显式 Go 宿主配置，默认关闭；启用后受限 Transport 仅增加固定 KB 的文档 POST / DELETE，不跟随重定向。资料库启动绑定现支持 `manage_documents`，由宿主显式开启；对应五个文档接口和状态机见[应用链路验收](testing-2026-09-10-managed-documents.md)。不能仅凭该底层开关把默认源或会话附件当作已托管资料库。put / delete 只确认受理，不宣称索引完成或全部片段清理；状态来自实际上游字段，未知状态保持未知。当前权限范围下查不到文档也不能单独当作全局删除证明。

资料库文档已持久保存归属、原文件引用、不可变远端 ID 和任务状态，并在搜索 / 读取、引用及历史复用前执行逐文档过滤。已开始的推送与删除不会因结果不明而盲目重发；本地删除立即停止访问，未知远端状态继续保留清理任务。完整新链路目前通过知识服务夹具及实际浏览器验收，资料库文档 UI 已交付；真实独立远端 KB 的新应用链路验收仍待补齐。会话附件不自动调用共享库推送。用户可通过[显式另存接口与页面](testing-2026-09-10-attachment-library-copy.md)创建独立资料库副本；该副本有自己的文档登记、来源审计、原件、索引与删除生命周期。

2026-09-11 删除恢复增量：公共 SDK 可选 `KnowledgeDocumentDeleteRecoverySource` 允许可信来源取得确定删除回执，或安全重复删除同一不可变文档。官方知识 Connector 1.3.0 已按实际 `purge_doc` 契约声明 DELETE 自然幂等，上传仍不承诺幂等。资料库和会话附件 worker 只在持久删除已开始、原来源与权限摘要一致时调用恢复端口；取得回执并持久保存后，再检查状态与清理原件。恢复失败继续退避并保留原件，不支持该端口的旧来源保持待核查。真实私有合成文档已验证第一次成功响应丢失、第二次删除返回全零统计以及后续 fetch / search 不可见，详见[删除恢复验收](testing-2026-09-11-knowledge-delete-recovery.md)。

本机网页启动脚本可读取仓库外的 `~/Library/Application Support/domainry-agent/web-services.json`（通过 `AGENT_WEB_SERVICES_CONFIG` 覆盖）；环境变量优先。该文件只允许非敏感服务配置，密钥仍通过环境或终端隐藏输入提供。

## 原件在线预览与 Connector 内容提取

`knowledge_extract` 仅处理 Connector 返回的受权内容。原文件的本地解析 / 缓存 / 私有附件提取已删除；PDF、DOCX、XLSX 在线预览通过受权原件下载接口加载，不调用模型或解析服务。升级与移除范围见 [说明](document-parsing.md)，当前验收见 K06 TODO。

## 当前会话的私有附件检索

配置专用 `AGENT_ATTACHMENT_KNOWLEDGE_BINDINGS`、工具宿主及授权器后，显式索引的 ready 附件可通过 `attachment_search(query)` / `attachment_read(attachment_id)` 查询 Connector。需要分别授予 `agent.conversation_tools.attachment_search`、`agent.conversation_tools.attachment_read` 和 `agent.conversations.attachments_download`；注册工具不会自动授予权限，索引仍是独立的用户文件管理动作。

模型不能选择用户、会话、远端 KB 或 ACL。Agent 用当前会话元数据过滤命中，引用绑定本地附件 ID 和来源会话；其他会话不能借历史或工具结果入口获取这些片段。网页引用沿用原件查看器，读取原文件不会触发检索或服务端解析。实现、权限 / 重启 / 网页证据及剩余 K07 工作见[私有附件检索验收](testing-2026-09-11-private-attachment-retrieval.md)。

附件页面现已提供显式索引与同一次任务核对，权限和来源大小上限来自服务端响应。核对 API 为 `POST /agent/conversations/{conversationID}/attachments/{attachmentID}/index/check?expected_revision=N`，需要独立 `agent.conversations.attachments_check_index` 权限；它只推进既有持久任务，保留远端写入标记。断响应、源配置移除、重启、网页权限及原件清理证据和真实验收剩余项见[附件网页与恢复验收](testing-2026-09-11-private-attachment-index-ui.md)。
