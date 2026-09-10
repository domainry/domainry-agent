# 知识文档推送、索引状态与删除协议验收

日期：2026-09-10。本批完成 K05 / K07 所需的外部文档生命周期接口，并通过真实 Verdent 验收。产品内文档归库、上传入库队列、索引状态网页、跨库共享与私有 ACL 仍未完成，不能据此勾选整个条目。

## 已实现

- Connectors 的 `knowledge_base/http_api` revision 1.1.0 新增 `put_document`、`document_status`、`delete_document`，公共定义与生成的 Catalog 一起更新。原 search / fetch 的契约摘要不变；没有在 Agent 内另写一套 HTTP 文档协议。
- 推送复用已验证的 `POST /v1/kb/kbs/{kb_id}/documents?doc_id=…&filename=…`，原文件通过 `application/octet-stream` 发送。SDK 输入为字节，序列化时是 Base64，本地限制 16 MiB；路径、目标库、权限 ID 及凭证不属于操作输入。
- 索引状态复用 fetch 的 metadata 模式，读取实际 `/data/status`，保留未知状态。不存在时只按明确业务 `1004` 返回当前权限范围内不可见；网络错误、授权失败和无效响应不能当成不存在，也不生成就绪状态。
- 删除使用固定目标 KB 与经过 URL 编码的文档 ID。推送和删除声明写操作，不宣称已验证幂等、上游 fencing、自动核查或补偿；Connector 内不重试不确定写入。上游受理成功与索引完成 / 删除确认分别处理。
- SDK 新增可选 `KnowledgeDocumentSource` 宿主端口。Agent `knowledge_documents.go` 经现有 Gateway 复用同一官方 Connector，并检查可信身份与工作区。原始文件不会进入模型工具参数或回复。
- `KnowledgeConfig.DocumentManagement` 是 Go 宿主配置，默认关闭；不新增环境开关、网页上传入库接口或模型写工具。启用时独立 Transport 仅增加指定 KB 的文档 POST / DELETE，限制路径、查询参数、请求 / 响应体大小并禁止重定向。普通检索 Transport 只允许原 search / fetch 路径。

代码入口：[Agent 文档端口实现](../internal/infrastructure/provider/knowledge_documents.go)、[受限 Transport](../internal/infrastructure/connectortransport/http.go)、[真实生命周期测试](../internal/infrastructure/provider/knowledge_documents_live_test.go)。公共端口在相邻 SDK 的 `knowledge_document_source.go`；协议在 Connectors 的 `providers/knowledge_base/http_api/documents.go`。

## 确定性与回归检查

| 检查 | 结果 / 日志 |
| --- | --- |
| Connector SDK 契约、Catalog、请求翻译和权限输入专项 | 通过；`/tmp/domainry-knowledge-documents-connector-targeted.log` |
| Agent Provider / Transport / Module 专项 | 通过；`/tmp/domainry-knowledge-documents-agent-targeted.log` |
| Agent 全量 `go test ./...` | 通过；`/tmp/domainry-knowledge-documents-agent-full.log` |
| Agent SDK 全量 `go test ./...` | 通过；`/tmp/domainry-knowledge-documents-sdk-full.log` |
| Connectors 全量 `go test ./...` | 通过；`/tmp/domainry-knowledge-documents-connectors-full.log`，使用当前 Agent go.work |
| Agent Provider / Transport 与 Connector 专项 race | 通过；`/tmp/domainry-knowledge-documents-agent-race.log`、`/tmp/domainry-knowledge-documents-connector-race.log` |
| Agent / SDK / Connectors `go vet ./...` | 均通过；对应 `/tmp/domainry-knowledge-documents-*-vet.log` |
| 生成 Catalog 最终检查及 Catalog / Registry 补验 | 通过；`/tmp/domainry-knowledge-documents-connectors-catalog-final.log`、`/tmp/domainry-knowledge-documents-catalog-tests-final.log` |

测试覆盖非 UTF-8 二进制原样发送、中文文件名和保留字符编码、跨工作区拒绝、禁止输入目标 KB / permission_ids、非法文件名、超大 / 空内容、普通检索配置不能写文档、仅目标 KB 可以写、重定向不转发文件和凭证、状态查询沿用权限、未知状态不推断成功、缺失响应与错误分类，以及一次网络失败不自动重复写入。

Connectors 格式与边界检查通过。Catalog 检查首次指出生成清单过期；已按官方生成器更新，仅知识 Provider 条目发生变化，并重新检查生成结果。没有新增依赖或更改其他 Provider。

## 真实服务结果

通过本机已授权服务配置与私有凭证文件运行：

```sh
python3 scripts/test-agent-knowledge-documents-live.py --live
```

本测试默认不随普通 go test 执行；显式启用后生成新随机文档 ID，先查询确认该 ID 当前不存在，再把测试清单写入磁盘并同步后请求推送。全程使用实际 Agent Provider、Connector SDK、官方 Connector 和受限 Transport；没有调用模型。

本次测试文档 `domainry-agent-acceptance-20260910-69e72a393d33` 只包含明确标注的合成验收资料。依次完成：

1. 初始状态查询不可见，登记范围、文档 ID 和写入意图。
2. 推送原始 Markdown 内容，上游确认受理。
3. 实际观察 `PENDING → CHUNKED → INDEXED`。
4. fetch 返回 1 个真实片段，其中包含该文件唯一标识；search 以该标识查询命中同一文档 ID。
5. 删除请求获上游确认，再查询文档不可见，并确认相同标识检索中没有该文档。

测试通过，耗时 30.36 秒。日志 `/tmp/domainry-knowledge-documents-live-1.log`；测试清单位于 `/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-knowledge-document-lifecycle-7o_sq434/manifest.json`。已核对 `upload_acknowledged`、`content_readable`、`search_matched`、`delete_acknowledged`、`cleanup_verified` 均为 true，索引状态 INDEXED。清单不含凭证；测试文档已清理。

若以后测试中断，先检查同一 manifest 和远端状态，不要重新生成文件；既有 `scripts/knowledge-live-document.py --cleanup <manifest>` 能对该合成 ID 执行恢复清理。受限文档可能被上游隐藏成不存在，因此 `Exists=false` 不是任意权限下的全局删除证明。本次使用固定来源与凭证，并在删除前已读取同一合成文档，结果只覆盖这条实际链路。

## 剩余产品链路

接下来必须补文档登记和库归属、独立原文件引用、文档 / 索引代次、持久任务与不确定结果处理，之后再接网页上传和状态。个人资料与共享库需按当前成员 / 动作授权；会话附件须保留来源会话边界，不得因调用推送接口变成团队可见。

删除需要本地立即禁止该文档进入检索结果、引用、历史和模型输入，并持续处理远端清理及迟到上传 / 索引竞争。当前按库检索仍没有这层逐文档登记与过滤，所以不能直接把已实现的原文件上传接口串到远端推送。跨库共享 / 移动还需目标写入确认与源清理，不等同于更改本地标签。

本次没有新增前端能力。开始检查浏览器时 CUA 报告 Mac 锁定；本轮没有进行网页验收，也没有启动新的网页服务。真实协议验收成功不能代替后续产品端到端验收或私有 ACL 验证。
