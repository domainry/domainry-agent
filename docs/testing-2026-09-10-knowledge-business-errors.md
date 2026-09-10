# 真实知识服务业务错误与会话回归

日期：2026-09-10。范围：K01 的空结果 / 不存在文档，以及失败从 Connector 到 Agent 的传播。本记录不代表真实非空文档、上传和私有文档权限验收完成。

真实服务使用既有配置和获准凭证，密钥经终端隐藏输入，不写入代码或记录。三个只读查询 `domainry`、`文档`、`test` 均返回 HTTP 200、`err_code: 0`、`data.hits: []`。专用不存在 ID `domainry-acceptance-nonexistent-20260910` 的 fetch 返回 HTTP 200、`err_code: 1004`、`err_msg: "document not found"`。因此不能仅以 HTTP 2xx 判定成功。

原 Connector 会将该业务失败保存为成功数据。修复后，存在的整数 `err_code` 必须为 0 才能成功；1004 映射为 not_found，其他非零状态为 failed，无效类型为 response_invalid。上游错误正文和随附 data 均不作为结果返回。Agent 保留 `knowledge_not_found` 分类，网页显示重新检索的提示；已保存知识凭据再次读取到该错误时不可复用。

验证已通过：

- Connectors `go test ./providers/knowledge_base/http_api`：业务错误、非法状态类型、错误正文隐藏与两种操作的成功 JSON 原样保留。
- Agent `go test ./internal/infrastructure/provider ./integration -run 'TestKnowledge' -count=1`：通过真实本地 HTTP Transport 调用 Connector，验证 search / fetch 的失败不产生来源，旧来源复核失败；会话按“读取失败 → 重新搜索空结果 → 明确回答未找到”完成，账本保留失败，模型输入、公开历史和事件不出现错误正文。

第二项的模型与知识服务为协议夹具，持久化使用真实 SQLite；它验证错误传播，不冒充真实模型对资料的理解。真实请求记录与回归代码分别证明上游行为和本地处理。

后续状态更正：最初新开的应用内控制台要求登录，管理详情探测返回 401、文档列表 / OpenAPI 探测返回 404；随后按用户要求打开系统 Chrome，已有有效登录。已从 bcri 详情页确认文档推送 / 删除接口，并用原有平台凭证成功推送合成文档，等待 `INDEXED` 后 search / fetch 均取得 4 个片段。先前 GET 探测不能推断文档推送权限不可用。[真实模型引用与删除复核](testing-2026-09-10-live-knowledge.md) 随后通过，测试文档已清理；控制台当前展示了查询 `permission_ids` 的规则，尚未确认设置私有文档 ACL 的管理契约。
