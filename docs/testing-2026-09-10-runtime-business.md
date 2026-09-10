# Runtime 会话业务查询验收记录

日期：2026-09-10。范围是业务源、模块启动绑定和真实记录读取边界。后续目录及 [业务网页验收](testing-2026-09-10-business-web.md) 已补齐 J01 / J02 的剩余验证；H04 发布依赖仍未完成。

## 实现与行为证据

Agent 模块为 Runtime 增加可选的延迟会话绑定。存储和旧 Agent 定义先建立；会话服务、worker 及 HTTP 适配器在当前授权和业务宿主绑定后创建。普通独立 Web 启动保持不变。测试预先保存了排队运行，并检查绑定前其状态和尝试次数不变；绑定后恢复目录与记录读取，重复绑定保留原会话服务。

Runtime 会话业务源已经进入真实启动装配，调用 `RecordApplicationService`。专项测试使用真实记录应用服务、查询策略、字段策略、ORM 和临时 SQLite。Identity 主体解析和 SDK AccessBundle 为测试夹具，不能称作真实 Identity 管理操作验收。

验证了所有者 / 工作区隔离、隐藏对象和字段、脱敏字段不可过滤 / 排序、指定字段投影、无效条件在查询前拒绝、当前主体失效后拒绝历史复用。目录输入契约在后续增量中补齐，见 [目录契约验收](testing-2026-09-10-business-catalog.md)；网页和真实模型整段验收见上方后续记录。

Runtime 原有记录接口要求游标翻页，不能按裸页码调用。SDK 已补 `cursor` / `next_cursor`；Runtime 按排序字段和 ID 继续查询，保留当前权限限制。覆盖升降序、排序同值、NULL、空字符串、未选择的排序字段、游标与原查询绑定；后续页不返回一个误称全量总数的剩余计数。Agent 工具循环也验证了模型不填页码、只传回游标的续查。

实际测试暴露 Runtime 存储和查询参数把整数转换为浮点数的问题，已修复两处转换。`9007199254740993` 经过真实写入、条件筛选与返回仍精确。排序 SQL 通过 SQLite / PostgreSQL / MySQL 的 ORM 构造验证，实际数据库执行只覆盖 SQLite。

## 测试记录

- `/tmp/domainry-agent-deferred-conversations.log`：模块延迟绑定及模型游标续查专项通过。
- `/tmp/domainry-runtime-conversation-business-tests.log`：真实记录服务 / SQLite 与游标排序专项通过。
- `/tmp/domainry-agent-runtime-business-full.log`：Agent 全量通过。
- `/tmp/domainry-agent-sdk-runtime-business-full.log`：SDK 全量通过。
- `/tmp/domainry-runtime-business-full.log`：Runtime 应用宿主、Transport、记录服务、记录存储通过。查询参数测试仍期待旧浮点类型，已修正，复查见下一条。
- `/tmp/domainry-runtime-business-recheck.log`：真实记录应用服务路径及查询包复查通过。
- `/tmp/domainry-agent-runtime-business-race.log`：Agent Module / 业务 / 延迟绑定专项 race 检查通过。
- `/tmp/domainry-runtime-business-race.log`：Runtime 真实记录服务业务专项 race 检查通过。
- `/tmp/domainry-agent-runtime-business-vet.log`、`/tmp/domainry-runtime-business-vet.log`：Agent 全量及 Runtime 受影响包静态检查通过。

Runtime 全量受影响包检查还出现两个发布校验失败：`TestPinnedModuleSetLockMatchesGoMod` 与 `TestPinnedModuleSetComposesAllElevenBindingsOnOneHostDatabase`。前者报告 Identity 实现 / SDK、Notification 的锁版本与 go.mod 不一致；后者报告 Agent / Integration 的能力摘要不一致。当前联调用临时 workspace `/tmp/domainry-agent-runtime-integration.go.work` 连接 Agent、SDK、Identity、Connectors、Runtime 本地源码，未改写发布锁掩盖差异。

## 仍待完成

- 关联记录查询与后续业务操作；目录的动作和流程输入契约已在后续增量补齐。
- 独立 Web 的外部业务服务连接，以及 H04 依赖发布、版本锁和脱离 go.work 的构建验证。
