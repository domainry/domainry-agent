# F06 Tools 写入适配与 Agent 确认端口

本批完成 F06 顺序中的 Tools 与 Agent 通用确认端口。F06 仍未勾选，整体仍为 54 / 81；接下来产品装配、具体内容预览、真实 Identity 与网页整段验收。未开始 N01。本批未改 llm-proxy 服务，Web 仍只消费已有搜索／抓取接口，见 [F04 范围纠正](testing-2026-09-12-f04-web-scope.md)。

## 实现与边界

Tools 的薄 `module` facade 新增 CalendarWrite / MailWrite 两个独立适配器与定义集，共七个工具：日历写账号发现、事件完整检查、创建、修改，以及邮件写账号发现、发送、回复。原日历五个、邮件四个、Web 两个只读定义没有扩权或改名；十八个工具同时注册、选装通过。

`internal/adapter/accounttools/write.go` 与 `write_discovery.go` 承担中立账号发现、实时 Subject、写权限、修订绑定、宿主执行身份和原回执读取。日历／邮件适配器各自处理 Connector SDK 类型化参数与回执，不互相导入；共享账号机制不导入日历、邮件或 Web 协议。Tools 不导入 Agent、Integration、Provider 的实现层，不保存确认或厂商调用账本。数据及并发领取仍归前一批 Integration owner。

模型仅提供 `account_key`、发现得到的 `account_updated_at` 与类型化 `request`；修订用于把确认内容固定到原账号状态，不授予权限。宿主从当前 Identity 投影明确的 `integration.connection_accounts.write` Subject，owner 返回完整 Source。请求 ID 从 Runtime 和原执行幂等键派生，不以账号、操作或正文生成新 ID，使 owner 能拒绝同一执行身份改目标或改内容。Reconcile 只调用原回执端口，未知／失败均不触发新的写入。

Tools SDK 新增可选 `ConfirmationVerifier`，Agent SDK 保持别名。Agent 启动时向 `AssembleTools` 的 base 提供它；Tools 必须显式取得并装配这个端口，缺失时拒绝注册。验证器核对持久批准记录、真实主体、运行、步骤、调用、定义和参数摘要，以及当前 worker 租约。批准整个列表时仍复用原有事务生成的逐调用批准记录；未增加确认表或对外批准接口。包装保留原宿主的结果权限与交互授权能力。已完成结果的读取只复查当前权限和来源，不要求过期的执行租约。

日历保留完整参与者、实际版本、单次／系列范围、DST 偏移和 PATCH 省略／清空；事件检查复用只读 owner。邮件保留全部显式 To／CC／BCC 与纯文本正文，发送结果为 accepted／delivery unknown，允许 Graph 202 没有 Message ID。回执须匹配原来源、目标、owner 调用引用及语义；不合法或来源不符的响应返回 uncertain，不泄露原始错误或正文。

## 检查结果

所有命令使用当前源码工作区 `GOWORK=/Users/tiger/Projects/domainry-agent/go.work`，日志在 [本批证据目录](evidence/2026-09-12-f06-tools-write/)。

| 检查 | 结果与证据 |
| --- | --- |
| Tools 新适配专项 race | 修复后八项函数及子场景通过，2.658 秒；随后新增分页测试随全量 race 通过。[专项日志](evidence/2026-09-12-f06-tools-write/tools-race.log) |
| Tools 全量 race | 14 包通过／编译，Module 6.855 秒，包含本批九项函数与原日历／邮件／Web、注册／策略／存储和架构检查。[全量日志](evidence/2026-09-12-f06-tools-write/tools-full-race.log) |
| Agent 持久确认专项 race | 两项函数，四种流程，6.492 秒；批准后服务重启、原列表两项与新调用、仅批准单项、拒绝和跨用户。每次实际执行中另验证二十种身份／参数／定义／租约篡改。[确认日志](evidence/2026-09-12-f06-tools-write/agent-confirmation-race-first.log) |
| Agent 全量 | 全部通过／编译；Integration 80.336 秒，Web 118.488 秒，包含原有装配与结果复查回归。[全量日志](evidence/2026-09-12-f06-tools-write/agent-full.log) |
| Tools SDK / Agent SDK 全量 race | 全部通过／编译，包含原接口与 businessrpc 回归。[Tools SDK](evidence/2026-09-12-f06-tools-write/tools-sdk-full-race.log)、[Agent SDK](evidence/2026-09-12-f06-tools-write/agent-sdk-full-race.log) |
| 四库 vet | Tools、Tools SDK、Agent、Agent SDK 均退出 0；对应 `*-vet.log` 保留 |

Tools 专项经公开 facade 调用真实 Registry／Selection／adapter，Integration 与确认 owner 使用可观察端口夹具。覆盖四种写入精确 payload、稳定请求身份与原回执；十三种身份／策略／修订拒绝和两种缺少确认端口；Schema／语义错误、控制参数注入；七类不可信／未知回执；响应期间撤权与历史复查；发现分页的空页／结束、操作和修订绑定；可用性与发现没有调用读写执行端口；事件检查不缩短完整目标。

Agent 专项使用实际 SQLite 会话／确认持久层、真实 worker 与用户 Respond 流程，模型和外部副作用为确定性夹具。重启为服务重建，数据库继续使用同一个持久库；本批没有重新声明 Integration 四组合的磁盘重开测试。

## 首次失败及修复

第一次 Tools race 的 `calendar_null_attendees` 失败：PATCH 的指针解码把 `null` 视为省略，其他修改字段使类型语义校验仍成功，导致在 Registry 拒绝 Schema 前多做了一次批准验证。没有进入 owner 写入。现将完整 Schema 校验前移到确认所用参数准备路径，再进行业务类型校验，执行与历史复查共用该准备路径。首次失败日志原样保留在 [tools-race-first.log](evidence/2026-09-12-f06-tools-write/tools-race-first.log)，没有放松测试预期或 SDK 契约。

审查还发现新增启动包装会遮住原宿主可选结果策略的风险，已显式转发并加入实际装配测试；没有绕过已有旧结果撤权规则。

## 当前验收边界与后续

这是 Tools / Agent 端口增量，不是整个 F06 产品验收。Google／Microsoft 实际 Provider、OAuth HTTP、owner 回执与磁盘重开仍对应 [前一批 Integration 证据](testing-2026-09-12-f06-integration-write.md)。本批没有发出真实邮件、创建真实厂商日程，也没有新增浏览器写入测试或发布 SDK tag。

下一步按当前代码在产品宿主选装两类写工具，取得 Agent 的验证器端口，投影和声明当前 write 权限；Agent／Work／PM 默认 Agent 与 Skill 白名单、工具开关、准确目标／内容预览及整段网页继续在 F06 内完成。借用 Identity 时仍由原 owner 发布权限。本批产品选装代码和页面未修改。

源码、未变文件、依赖与证据 SHA-256 见 [机器清单](evidence/2026-09-12-f06-tools-write.json)。
