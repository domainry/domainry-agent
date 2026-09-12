# K01：真实私有权限矩阵与文档对照

日期：2026-09-11。K01 已完成：真实私有权限矩阵 7 个场景、26 次请求通过，公开／私有合成资料均已删除并复核不可检索。结合已有[真实空结果与业务错误记录](testing-2026-09-10-knowledge-business-errors.md)和[真实模型引用及网页验收](testing-2026-09-10-live-knowledge.md)，覆盖 K01 的 search、fetch、空结果和无权限文档。产品库成员与 Identity 的私有检索链路另见 [K04 验收](testing-2026-09-11-library-permissions.md)。

## 当前服务事实

在系统 Chrome 的已登录 [Verdent 控制台](https://platform.verdent.ai/en/knowledge-base) 检查 bcri 详情及 API push 新建向导：

- search / fetch 的权限参数由应用服务端计算；多个 ID 按任一精确匹配生效，非空请求也包含团队可见文档。省略或传空数组仅允许团队可见文档。控制台说明同团队的有效平台 Key 可以传非空权限 ID，无需为 search / fetch 另注册 delegate。
- 上传说明仅包含 `doc_id` / `filename` 及原始文件请求体，没有设置或修改文档 ACL 的字段；API push 新建向导也没有这项配置。本次只检查向导，未提交新建知识库。
- 当前登录及既有 API Key 可用。控制台未展示私有上传字段，后续已从实际源码补齐，不需要用户再提供凭证或源码。

## 固定提交的源码契约

本机 DevOps 部署配置指向 `codeck-backend/kb-search-api` 和 `codeck-backend/verdent`。GitHub CLI 元数据返回 404，但现有 Git 凭证可读取；已将固定提交拉取到临时裸对象库，仅保存 FETCH_HEAD，没有新增开发分支、worktree 或修改用户 checkout。知识服务提交为 `dbf61406e55b8a446f026cde941ce7f9a9f33bda`，Verdent 提交为 `1220c52038f53c4627b1476558ebc991244b8eae`。源码清单在 `/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-K01-source-bup8lv95/manifest.json`。

已读知识服务 `docs/document-permissions.md`、`handler/push.go`、`handler/push_permissions_test.go`、`router/router.go`、`middleware/api_key_auth.go` 和权限解析实现，确认：

- 普通有效团队 Key 可推送私有文档。二进制 POST 携带 `X-KB-Permission-Ids`（ASCII 转义、紧凑 JSON 数组）和稳定 `X-KB-Request-ID`；源码校验后将两个头转发给上传网关。权限命令省略和显式空数组不同；重试同一命令必须沿用请求 ID。
- 独立文档权限 GET / PUT 另需配置 delegate；创建本次专用私有文档不使用这两个接口，也不修改业务文档。
- ID 按任一精确匹配，不继承组织或字符串层级。查询权限由应用服务计算；无权限 fetch 返回 HTTP 200 / err_code 1004，与不存在一致。权限服务不可用不能降级公开。

`scripts/knowledge-live-document.py --create --private` 在写入前保存唯一合成文档 ID、单独生成的权限 ID、稳定请求 ID 和正文；先确认随机 ID 不存在。inspect 和 cleanup 使用原始私有权限，删除必须取得成功回执，不能把首次私有 1004 当作已清理。公开模式保持原协议。本次真实响应还出现 `PARSED` 中间态，脚本已补为等待状态；沿用同一清单检查，没有重传新文档。

## 验收实现

`internal/infrastructure/provider/knowledge_permissions_live_test.go` 增加 `TestLiveKnowledgePermissionProbe` 和可在本地 HTTP 夹具运行的矩阵。通过现有 Agent Provider、官方 Connector 和受限 Transport 实际执行 search / fetch，没有新增生产协议实现或模块内部交叉依赖。测试 Transport 仅把缺省权限替换为文档说明的显式 `[]`，以区分这两种上游输入；正常 Connector 仍把空权限规范化为省略。

公开对照检查省略、显式空数组、随机错误权限 ID 三种请求。私有矩阵要求：正确 ID 可搜索且读取到指定文档的唯一标识；无权限、空数组、错误 ID、相似但不完全一致的 ID 不可获得该文档；混合错误与正确 ID 仍可读取。每一组都先要求相同请求权限下的公开对照文档可读，防止把失效 Key、服务故障或全局拒绝算成文档隔离成功。另检查保存的私有来源在当前权限变化后得到明确的不可见／拒绝结果；普通网络失败不算撤权通过。

成功搜索必须符合已观察的 `data.hits` 数组，并同时匹配文档 ID 与片段标识；缺少数组不能被当作空结果。读取必须在 `data.chunks[].content` 中找到标识。被拒绝读取没有可进入模型的 Data / Citations。报告只保存场景、参数形式、HTTP 状态、数值业务码、响应字节数和断言结论；不保存凭证、请求头或其他命中文档原文。

启动脚本 `scripts/test-agent-knowledge-permissions-live.py` 复用现有私有凭证加载器，核对公开清单、私有夹具与当前 origin / team / kb 一致。显式区分 `--public-control-only` 和 `--private-fixture`；公开模式即使通过，也明确标记 `scope=public_control_only`，不能替代 K01 私有验收。脚本及 Go probe 只读取文档，不设置权限、不推送、不删除。

## 真实结果

使用既有合成文档生命周期脚本创建本批唯一对照文档 `domainry-agent-acceptance-20260910-7ae031d370b4`。写入前确认该随机 ID 不存在；内容只有合成标识与测试条款，未涉及真实业务。实际观察 PENDING → CHUNKED → INDEXED，取得 4 个片段。

| 权限输入 | search | fetch | 实际断言 |
| --- | --- | --- | --- |
| 省略 | HTTP 200 / err_code 0，3476 bytes | HTTP 200 / err_code 0，2744 bytes | 命中指定公开文档及其唯一标识 |
| 显式 `[]` | HTTP 200 / err_code 0，3476 bytes | HTTP 200 / err_code 0，2744 bytes | 同一公开文档可读 |
| 一个随机无关 ID | HTTP 200 / err_code 0，3476 bytes | HTTP 200 / err_code 0，2744 bytes | 仍能读取公开文档，不能把非空权限参数当作排除所有公开资料的过滤器 |

真实 probe 通过，耗时 5.095 秒。报告 `/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-knowledge-permission-probe-ref253pl/permission-report.json`，日志 `/tmp/domainry-K01-public-probe.log`。公开文件的准备日志 `/tmp/domainry-K01-public-create.log`；清单 `/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-knowledge-live-doc-dfi15gnu/manifest.json`。

验收后只删除该清单记录的合成文档，删除返回 HTTP 200 / err_code 0，后续 fetch 为 err_code 1004，清单 `cleanup_verified=true`。额外用原标识搜索，仍有 1 个其他命中，但不包含已删除的测试文档；清单目录的 `search-after-cleanup-check.json` 保存了该结论，不能把它报告成全库空结果。清理日志 `/tmp/domainry-K01-public-cleanup.log`。这些观测只证明这份公开对照，不推断其他数据源的上传缺省 ACL，也不证明私有文档规则。

## 检查与重现

- 7 个矩阵反向场景通过：正常隔离、搜索泄露、读取泄露、接口整体拒绝、错误响应格式、公开对照不可读、来源复核网络错误；后六项必须被验收器判为失败。相关 race 通过，日志 `/tmp/domainry-K01-probe-race.log`。
- 脚本两项测试通过，覆盖来源错配、未索引／已删除公开对照、缺少私有参数及公开模式不声称私有验收：`/tmp/domainry-K01-probe-script-test.log`。
- Knowledge 相关 Provider、Transport 与 Integration 回归通过，日志 `/tmp/domainry-K01-knowledge-regression.log`。Provider `go vet` 和 diff 检查通过，日志 `/tmp/domainry-K01-probe-vet.log`。
- 私有生命周期和验收配置共 6 项脚本测试通过，日志 `/tmp/domainry-K01-private-scripts.log`。覆盖 PENDING / PARSED / CHUNKED 等待、上传前持久请求 ID、私有读取范围、清理前 1004 仍执行删除、失败删除不误报完成、重复清理、拒绝业务 ID／损坏权限清单，以及公开／私有对照错配。

## 真实私有矩阵与清理

固定同一 team / KB，以两个新建合成文档验证：私有 `domainry-agent-acceptance-20260910-8bd5e52f6e76`，公开 `domainry-agent-acceptance-20260910-7023e1eca588`。两者都在写入前确认不存在，上传 HTTP 200 / err_code 0，索引后各取得 4 个片段。私有文档只绑定本次随机生成的一个权限 ID。

| 请求权限 | 私有 search | 私有 fetch | 同范围公开对照 |
| --- | --- | --- | --- |
| 正确 ID | 命中文档及唯一正文标识 | 200 / 0，实际片段 | search / fetch 可读 |
| 省略 | 不包含私有文档或标识 | 200 / 1004，无 Data / Citations | search / fetch 可读 |
| 显式 `[]` | 不包含私有文档或标识 | 200 / 1004，无 Data / Citations | search / fetch 可读 |
| 随机错误 ID | 不包含私有文档或标识 | 200 / 1004，无 Data / Citations | search / fetch 可读 |
| 正确 ID ＋错误 ID | 命中文档及唯一正文标识 | 200 / 0，实际片段 | search / fetch 可读 |
| 相似但非完全一致 ID | 不包含私有文档或标识 | 200 / 1004，无 Data / Citations | search / fetch 可读 |

最后先按正确权限保存来源，再将可信宿主回调改为错误权限；`RevalidateKnowledge` 取得真实 1004 并拒绝旧来源。7 个场景全部通过，实际测试 17.15 秒，Go 包总计 17.866 秒。此项改变的是测试宿主计算的请求权限，不是修改真实 Identity 成员或远端文档 ACL；K04 不因此勾选。

仓库内保留无凭证的[机器证据](evidence/2026-09-11-k01-private-permissions.json)，包含全部 26 次请求的元数据、场景断言及删除后检索结论。原报告在 `/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-knowledge-permission-probe-1spoxrwy/permission-report.json`，日志 `/tmp/domainry-K01-private-matrix.log`。

| 资料 | 清单 | 准备与清理日志 |
| --- | --- | --- |
| 私有 | `/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-knowledge-live-doc-185ai0kw/manifest.json` | `/tmp/domainry-K01-private-create.log`、`/tmp/domainry-K01-private-inspect.log`、`/tmp/domainry-K01-private-cleanup.log` |
| 公开 | `/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-knowledge-live-doc-c62y592t/manifest.json` | `/tmp/domainry-K01-matrix-public-create.log`、`/tmp/domainry-K01-matrix-public-cleanup.log` |

两次 DELETE 均为 HTTP 200 / err_code 0，随后原权限下 fetch 为 1004，清单 `delete_acknowledged=true`、`cleanup_verified=true`。再分别用原权限和唯一标识搜索，各返回 1 个其他命中，均不含已删除的合成文档及标识；没有删除那个已有文档，也不将此描述为全库空结果。私有文档首次准备因脚本漏列 PARSED 状态提前退出；修正后只 inspect 同一文档并通过，没有重复上传。

使用以下命令创建新的两份对照；原清单均已清理，不能直接重跑：

```sh
python3 scripts/knowledge-live-document.py --create
python3 scripts/knowledge-live-document.py --create --private
python3 scripts/test-agent-knowledge-permissions-live.py /path/to/public-manifest.json --private-fixture /path/to/private-manifest.json
python3 scripts/knowledge-live-document.py --cleanup /path/to/private-manifest.json
python3 scripts/knowledge-live-document.py --cleanup /path/to/public-manifest.json
```

尚未就绪时对同一 manifest 使用 `--inspect`，不要重复上传新文档替代未完成任务。矩阵本身只读；生命周期脚本仅创建和清理自己的合成文档，不操作既有业务资料。产品成员撤销与 Identity 私有检索已在 K04 验收；生产 Connector 的私有上传、共享／移动及远端 ACL 更新继续按 K05 / K07 交付。
