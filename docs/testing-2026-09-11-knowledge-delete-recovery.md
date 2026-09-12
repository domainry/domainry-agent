# K07 删除响应丢失后的恢复

2026-09-11。本批完成资料库与会话附件的幂等删除恢复，并完成真实 Verdent 私有合成文档的协议验收。K07 继续不勾选：附件专用 KB 的产品验收和没有确定结果的上传核查仍未完成。源文件解析保持删除。

## 上游证据与契约修正

此前 Connector 将上传和删除都声明为不可幂等重试。实际 `kb-search-api` 提交 `dbf61406e55b8a446f026cde941ce7f9a9f33bda` 的 `handler/push.go:220` 明确说明单文档删除支持幂等清理；`pkg/clients/lambda_client.go:72` 同步调用 `purge_doc`，只有算子返回布尔 `ok:true` 才成功。进行中、清理未完成及来源冲突仍是失败。源代码节选随[机器证据](evidence/2026-09-11-k07-delete-recovery.json)保存。

官方 Connector `knowledge_base/http_api` revision 1.3.0 将 DELETE 改为自然幂等，PUT 保持非幂等。`delete_document` 的新契约摘要为 `e4bec5d6d992035b91bf019b55a609307dc56f401c89ef78709f74f57e8e8aa8`；旧摘要在网络调用前拒绝。Catalog 与目录定义已同步，其他操作的形状不变。Provider 每次只执行一次请求，重试调度属于宿主。

## 实现边界

- 公共 Agent SDK 增加可选 `KnowledgeDocumentDeleteRecoverySource`。返回成功必须代表可信删除回执，或经验证的幂等删除结果；仅凭权限范围内查不到文档不满足契约。
- [应用层共用处理](../internal/application/knowledge_delete_recovery.go)只依赖公共 SDK。资料库和附件 worker 在已持久标记删除、来源身份和权限摘要一致、租约仍有效时调用该端口。恢复成功先通过原有带租约校验的事务保存回执，下一次核实远端不可见后才清理原件。
- [Provider](../internal/infrastructure/provider/knowledge_documents.go)依据官方 Connector 的可靠性契约实现恢复。附件来源包装保留原用户、Runtime、Workspace 授权检查，不能经恢复端口绕过文档管理开关或改变固定文档目标。
- 失败继续使用原任务退避，保留原件和不可变请求信息。旧来源不实现恢复端口时仍保持待核查，不推断其支持重试。本批没有新增数据库迁移、模型工具、浏览器操作或源文件解析。

## 验收结果

| 范围 | 实际覆盖 | 结果 |
| --- | --- | --- |
| 应用层与 Provider | 不支持恢复的来源、取消上下文、到期租约不发送；关闭文档管理、非法 ID、跨用户 / Runtime / Workspace 和未知身份拒绝；一次失败只有一次网络请求，下一次使用同一目标 | 通过 |
| 资料库持久工作 | 已标记删除后恢复；成功保存回执再删除原件；重试仍失败则保留原件并退避；改变 ACL 不发请求，替换物理来源在服务绑定时拒绝；旧来源遇到隐藏文档仍不确认删除 | 7 个场景通过 |
| SaaS 附件链路 | 第一次 DELETE 实际完成但返回 503，原件和未知状态持久保留；服务与 SaaS 绑定关闭重建，按当前版本核对后完成同一删除 | 新场景 0.38 秒通过，PUT 1 次 / DELETE 2 次，最终持久回执和物理原件清理均核实 |
| 真实 Verdent | 一份新建私有合成文档，原始上传、实际索引、正文读取和 search 命中；第一次真实删除回执在宿主测试 Transport 中丢弃，恢复调用再次发送同一个 DELETE，随后核查 fetch / search 不可见 | 33.76 秒通过，文档已清理 |
| 回归 | Agent 全量、SDK 全量、Connector 包与 Catalog、Provider 发布验证、Catalog 生成一致性、针对恢复的 race、vet、4 项合成文件辅助脚本测试 | 全部通过 |

真实删除统计直接核对原始 JSON：第一次删除为 `docs=1 / rows=4 / s3_objects=2 / vectors=1`；第二次为 `ok:true`、`reason:duplicate_event`，四项删除数均为 0。两次 URL 完全一致，持久测试清单记录 `delete_response_lost`、`delete_acknowledged`、`delete_recovery_verified`、`cleanup_verified` 均为 true。

真实调用复用了既有获准 KB 内唯一合成私有文档，只验证底层删除协议，没有将该 KB 配成会话附件来源。本批未调用模型、未重跑整轮网页，也未重新部署用户的 Web 服务。附件产品的 Identity / Module / HTTP 和网页已有验收维持原范围，不把本次协议测试当作附件真实产品验收。

首次专项测试暴露的是测试装配问题：旧来源包装缺少只读接口、附件来源缺少必要映射，以及换库实际在服务启动时就被拒绝。已修正测试装配与对应断言，保留最初失败日志。最终专项与 race 通过；全量 integration 83.583 秒、web 55.314 秒，架构检查通过。

## 复现与证据

```sh
go test ./...
go vet ./...
go test -race ./internal/application ./internal/infrastructure/provider ./integration -run 'TestKnowledgeDeleteRecovery|TestManagedDocumentPolicyChangesAndHiddenDeletionRemainRecoverable|TestPrivateAttachmentIndexSaaSConnectorLifecycleAndResponseLoss/(normal|lost-delete-recovered)$' -count=1 -v
python3 scripts/test-agent-knowledge-documents-live.py --live --delete-recovery
```

最后一条需本地知识服务配置与凭证，会生成一份私有合成文档及持久清单；只在明确执行真实验收时使用。Connector 仓库的 `go run ./scripts/generate_catalog --check` 与 `go run ./scripts/verify_provider_release --provider knowledge_base/http_api --mode deterministic` 已通过。详细日志、两次真实响应、清理清单及文件哈希见[机器证据](evidence/2026-09-11-k07-delete-recovery.json)。
