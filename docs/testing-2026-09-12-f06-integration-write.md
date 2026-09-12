# F06 Integration 账号写入 owner 增量

本次完成 Integration／Integration SDK 的宿主写入授权、四个同步写入操作、一次领取、有限回执及原身份核查。F06 保持未勾选、总进度 54 / 81；下一步 Tools／Agent／产品的目标与内容确认，以及整段网页验收。此次没有改动 llm-proxy、Connector SDK 或两个 Provider，也没有新增数据库／迁移。

## 代码职责

公共 SDK 以可选 `ConnectionAccountWritesBinding` 暴露 Authorize／Write／ReadReceipt。宿主提供当前 Subject 和稳定 RequestID，冻结 owner 返回的来源修订与具体参数。Module 使用 Go 端口；SaaS 是有服务认证的三个 POST，未加入公开产品 HTTP／浏览器写入口。接收端严格解析单个有界 JSON，拒绝附带身份／scope 字段；客户端禁止重定向、Cookie、自动重试，限制响应大小并检查来源和状态。

Integration 应用层只编排内部授权、协议 codec、账本、Provider 执行四个端口。`internal/adapter/accountwrite` 仅消费公开 `calendarwrite`／`mailwrite`，白名单为创建／修改日程、发送／回复邮件及对应 SHA。原 `calendar_event_inspect` 仍走同步只读入口。原授权读取、通用调用、表定义、公开 Module HTTP 与浏览器 SDK 的源码保持未变。

账号／实际 grant 的查询归 ManagementStore；连接和凭证解析、最后修订／grant 检查、Provider 调用归执行适配；领取和回执归 OperationsStore。Provider 仅来自宿主注册表，未导入具体 Provider 实现。沿用宿主数据库生命周期和 `_integration_invocations`，没有第二份邮件／日历数据库，也不跨外部 HTTP 保持数据库事务。

## 身份与恢复

- ID 固定绑定 workspace、actor、宿主 RequestID，并使用独立 `account-write:` 命名空间；指纹绑定准确 Source 和经类型化校验／规范化的参数。同一身份改正文、目标或操作会冲突，JSON 字段顺序和空白不造成第二次发送。
- 先唯一主键 INSERT 领取，再复核当前授权和执行条件。完成 UPDATE 必须匹配原 running 状态及原领取元数据。并发输家只查记录；失败、运行中、崩溃留下的领取均不重置、不自动重发。
- 成功仅保存固定契约验证后的回执。正文、标题、邀请／收件人、凭证、原始 Provider responseRef／错误均不入账本。邮件 accepted／delivery unknown，Graph 没有消息 ID 时不生成假 ID；日程通知只记 requested。
- 普通未知错误、明确 uncertain 或无有效回执都保留 uncertain；明确 permanent／retryable 拒绝记 failed，但仍不重放。ReadReceipt 是本地 owner 查询，没有厂商 I/O。
- 回执保存使用独立的五秒取消隔离上下文。刷新凭证的条件保存单独处理；其失败只留下安全标记，不能抹掉厂商已确认成功的副作用。返回／读取旧回执仍需当前账号授权及原来源修订。
- 审查旧后台核查时发现它按失败状态扫描通用调用表。本次在 SQL 的 LIMIT 前排除账号写入身份，防止后续 Provider 声明变化时接管账号回执，也避免这些失败记录占满旧任务候选额度。

## 验证与证据

执行工作区均为 `/Users/tiger/Projects/domainry-agent/go.work`；没有分支／worktree／子代理。测试使用隔离服务器与磁盘 SQLite。

| 验证 | 结果 | 直接证据 |
| --- | --- | --- |
| owner 专项 race，9 个测试函数及子场景 | 4.981 秒，通过；16 个并发输家、身份冲突、15 类拒绝、未知／失败／非法回执、取消、撤权、领取／落盘中断、凭证保存失败、后台候选隔离 | [owner-final-race.log](evidence/2026-09-12-f06-integration-write/owner-final-race.log) |
| Google／Microsoft × Module／SaaS，生产 Provider／OAuth 协议／实际 HTTP／SQLite，四组合 race | 54.688 秒，通过；8 次 OAuth HTTP、48 次业务 HTTP；每组合 1 创建、2 PATCH（含 412）、3 邮件（含受理后断线）；各两次关闭数据库并重启，原身份不重放 | [http-initial-race.log](evidence/2026-09-12-f06-integration-write/http-initial-race.log) |
| Integration 全量 | 24 包通过／编译；包含原日历、邮件和 OAuth 等流程 | [integration-full.log](evidence/2026-09-12-f06-integration-write/integration-full.log) |
| SDK 全量 race | 根包 1.408 秒、remote 1.574 秒；4 包通过／编译，含七类远端故障、固定请求／回执路径、Cookie 隔离和请求约束 | [sdk-complete-race.log](evidence/2026-09-12-f06-integration-write/sdk-complete-race.log) |
| 新服务认证／严格 JSON 与架构专项 race | 服务 2.389 秒、架构 2.030 秒；未认证、浏览器 token、缺 Runtime、伪造字段、尾随 JSON、超限 × 三路由共 18 个 HTTP 拒绝，未进入 owner 端口 | [transport-architecture-race.log](evidence/2026-09-12-f06-integration-write/transport-architecture-race.log) |
| 两库 vet | 退出 0，无输出 | [Integration](evidence/2026-09-12-f06-integration-write/integration-vet.log)、[SDK](evidence/2026-09-12-f06-integration-write/sdk-vet.log) |
| 当前边界和历史源码哈希 | 原只读／DDL／公开浏览器路径未变，前一阶段 Provider／SDK 文件未变；应用与 codec 依赖中无 Provider、Tools、Agent、Runtime 实现；llm-proxy 服务仍干净 | [边界审计](evidence/2026-09-12-f06-integration-write/boundary-audit.json)、[依赖](evidence/2026-09-12-f06-integration-write/application-codec-dependencies.log) |

## 失败记录与限制

首次测试夹具把 `ResourceHealthReport` 写成 map，编译失败，已删除该错误字段；初次 owner race 还暴露测试误用插入型 grant helper、无命名空间的 Provider 错误码和同名 SQLite fixture 重建，均修正测试准备后通过，未放宽生产规则。[编译摘录](evidence/2026-09-12-f06-integration-write/initial-compile.log)、[首次 owner 日志](evidence/2026-09-12-f06-integration-write/owner-initial-race.log)、[修正后日志](evidence/2026-09-12-f06-integration-write/owner-second-race.log)均保留。SDK 新测试最初将 Route.Pattern 方法当作字段，分两处修正，原 [首次编译](evidence/2026-09-12-f06-integration-write/sdk-initial-race.log)和 [第二次检查](evidence/2026-09-12-f06-integration-write/sdk-final-race.log)保留。

协议服务器验证本地产品／Provider 组合及失败语义，不证明实际 Google／Microsoft 租户授权、Graph CAS、收件人送达或真实模型质量。本次没有完成 Tools／Agent 的精确内容确认、产品工具选装或浏览器完整写入流程，因此不勾选 F06、不进入 N01。未发布新 SDK tag，当前开发通过工作区消费独立库。

[机器清单](evidence/2026-09-12-f06-integration-write.json)保存源码／日志哈希与前序清单引用；证据已直接挂到 F06 下方。
