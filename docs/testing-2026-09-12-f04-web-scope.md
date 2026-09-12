# F04 两个 Web 接口的范围纠正

用户明确要求接入 llm-proxy 的 websearch／webfetch 接口。本项交付范围为 Connectors 消费两个已有 HTTP 接口，再由 Integration、Tools 和产品装配使用。先前对 llm-proxy 服务本身的修改超出要求，现已撤回。

| Domainry 工具 | llm-proxy 现有接口 | 协议适配归属 |
| --- | --- | --- |
| `web_search` | `POST /tool/web_search` | Connectors `providers/web/llm_proxy`；搜索返回直接的 `search_id/results` |
| `web_fetch` | `POST /tool/web_fetch_jina` | 同一 Web Provider；页面返回 `code/data` |

当前服务路由没有 `/tool/web_fetch`；工具名由 Domainry 对外保持为 `web_fetch`。Provider 的 Connector key 为 `web`、Provider key 为 `llm_proxy`，只声明这两个只读操作。这里的 Provider 命名表示 Web 接口实现来源，不代表接入其模型／聊天或其他服务能力。

## 撤回及代码边界

撤回前逐文件核对 [F04 产品历史清单](evidence/2026-09-11-f04-web-product.json)中的 SHA-256，确认 6 个已跟踪文件和 6 个新增文件全部与本轮写入的快照一致，无额外暂存、未暂存或未跟踪变更。只恢复 `handler/web_fetch.go`、`handler/web_search.go`、`middleware/auth.go`、`middleware/logger.go`、`service/jina/jina.go`、`service/web_search/web_search_service.go`，删除本轮新增的 `library/httpmeta`、`library/webio` 内四个文件及两个服务测试文件。记录见[撤回前核对](evidence/2026-09-12-f04-web-scope/restore-plan.json)及[撤回结果](evidence/2026-09-12-f04-web-scope/restoration.json)。

`/Users/tiger/Projects/anti/llm-proxy` 已恢复提交 `a32407a678ea7f96d70b7557765e2a01262184d4`，Git 工作区干净。旧服务端请求／响应限额、context、日志和认证中间件修改不再属于交付，相关旧测试日志仅保留历史。

Connectors 继续经宿主提供的 Transport 适配上述两个协议；Integration 拥有连接、凭证与固定原点／路径的 HTTP 策略；Tools 通过公开 SDK 提供工具；Agent 执行层使用通用工具端口。Web Provider 的 190 项 Go 依赖中，没有 llm-proxy 服务实现或其他 Provider 实现，未引入跨库实现依赖。[范围审计](evidence/2026-09-12-f04-web-scope/scope-audit.json)还确认 F04 原清单中的 69 个 Domainry 非文档源码文件、三套产品共 1,713 个构建文件哈希均未变化。

## 针对性复验

- `GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test -race ./providers/web/llm_proxy -count=1 -v`：在 Connectors 仓库执行，七项测试通过，包 2.090 秒。覆盖两操作身份、请求映射、搜索／页面两种响应、真实本地 HTTP、取消、错误、来源策略及无自动重试。见 [Provider 日志](evidence/2026-09-12-f04-web-scope/provider-race.log)。
- `GOWORK=/Users/tiger/Projects/domainry-agent/go.work go test -race ./internal/infrastructure/connectortransport -run '^TestPublicWeb' -count=1 -v`：在 Integration 仓库执行，三项测试通过，包 1.700 秒。覆盖精确原点／路径、限额、重定向、取消及实际 Provider 经生产 Transport 调用两接口。见[宿主日志](evidence/2026-09-12-f04-web-scope/host-race.log)。

测试的上游为隔离 HTTP 协议服务器，未声称调用已部署 llm-proxy 或真实搜索／抓取厂商。产品源码和构建未变，本次不重复浏览器全量；既有八场景网页、来源报告编辑／下载、重启和撤权证据仍见[产品验收](testing-2026-09-11-f04-web-product.md)。

纠正已直接标注于 F04 下方，F04 保持已完成，当前总进度 53 / 81；F05 仍未勾选。本次[机器清单](evidence/2026-09-12-f04-web-scope.json)记录当前证据及保留代码的哈希，原阶段清单保持不变；其中服务端修改部分由本次纠正取代。
