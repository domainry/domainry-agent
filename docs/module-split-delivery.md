# Agent 拆分与 PM / Work 集成交付

本次在既有工作区完成实现，保留开始时的未提交修改，没有创建分支或 worktree。

## 模块职责

| 模块 | 实际职责 |
| --- | --- |
| `domainry-agent` | Agent / Skill 定义解析、工具选装约束、模型循环、会话记忆、执行账本、确认与恢复；旧 API 保留兼容转发 |
| `domainry-tools` | 工具注册、冻结选装、组合、当前授权、输入 / 输出验证、计算与时间实现、结构化文档工具适配 |
| `domainry-todo` | 独立待办模型、CRUD、日期 / 时区、批次、版本和原子幂等回执 |
| `domainry-knowledge` | 资料库及成员、文档、文件存储、抽取、索引、成果版本、导出，以及产品结构化文档存储 |
| `domainry-pm` | 产品经理 SaaS：需求分析、PRD、RICE 优先级、验收与证据，独立服务和前端入口 |
| `domainry-work` | 办公 SaaS：会议记录、决策与行动项、备忘录 / 报告 / 周报，独立服务和前端入口 |

另有中立契约模块 `domainry-tools-sdk`。旧 Agent SDK 类型兼容连接这些契约；Knowledge 的已发布 DTO 名称暂保留，避免破坏原调用方。Todo、Knowledge、Tools 实现均不导入 Agent 实现。

## 已完成

- [x] Agent 角色由各 SaaS 的 `internal/infrastructure/config/defaults/agent.json`、`internal/infrastructure/config/defaults/skills.json` 定义；可通过环境变量外置配置。
- [x] Agent 工具白名单约束展示、执行和恢复；Skill 无法扩大工具授权。
- [x] Tools 注册拒绝未知键选装、重复注册、非法 schema 和缺少恢复实现的写工具；调用时重新授权并校验输入 / 输出。
- [x] Todo 领域和数据库操作迁出，原执行账本与业务效果同事务的兼容路径保留。
- [x] Knowledge 文件、抽取、远程适配、应用服务、后台任务、业务表和迁移实现迁出；通过只读来源接口连接 Agent。
- [x] PM 页面与 Agent 工具共用领域校验、版本、CAS 和回执；验收需要明确结果和证据。
- [x] Work 页面与 Agent 工具共用会议、办公文档服务；Agent 可以继续调用独立 Todo 创建跟进事项。
- [x] 两个 SaaS 使用独立数据库、运行标识、应用身份和 Cookie 命名空间；默认端口分别为 8092 / 8093。
- [x] 管理员通过 Identity authoring 初始化产品权限，保留已有权限；启动不覆盖后续权限撤回。
- [x] 前端真实接口、搜索 / 分页、编辑 / 历史版本、失败请求幂等重试、键盘焦点管理，未保存修改时禁用转交 Agent。
- [x] 两个产品的前端与二进制构建完成，提供 README、Makefile、环境配置示例。

## 架构统一性复核

以既有 Agent / Identity / Connectors 代码中的 `internal` 实现隔离、薄公开入口、领域与应用不得依赖适配器为准。

| 边界 | 当前实现与验证 |
| --- | --- |
| 底层公开入口 | Knowledge、Todo、Tools 均为 `module/module.go`；应用服务、SQL、文件存储、HTTP 适配均迁至 `internal` |
| Agent 与 Knowledge | Agent 应用层仅依赖 Knowledge 契约，`internal/assembly/conversation` 注入 Factory；Knowledge 的校验、来源装配和生命周期由其 `internal/assembly/module` 负责，已去除架构检查中的兼容例外 |
| 记录契约 | Knowledge `contract/record.go` 定义记录、版本、校验和仓库端口；Tools 适配器和产品应用层不依赖数据库实现 |
| 产品分层 | 两个 SaaS 统一为 `cmd`、`internal/assembly/saas`、`internal/application/<领域>`、`internal/domain/<领域>/{model,service}`、`internal/infrastructure/config`、`frontend` |
| 文件命名 | Go 文件使用小写 snake_case；产品规则以 `_domain_service.go` 命名，应用转换以 `_application_service.go` 命名，数据库文件使用所属领域及 `_store.go` / `_schema.go` |
| 通用宿主 | Agent `webhost/host.go` 仅保留公开转发，产品装配位于 `internal/assembly/product`，HTTP 路由位于 `internal/transport/http/product` |
| 实例隔离 | 同一产品定义重启时重新创建 Binding，工具实例不会累积或引用旧数据库；PM、Work 集成测试复用同一产品定义验证 |
| 防止回退 | 新模块均有 `internal/architecture/layout_test.go`，检查公开实现泄漏、领域/应用反向依赖、跨模块 internal 引用及 Go 文件命名 |

成果格式、计算、时间与抽取属于明确开放的确定性库，可以直接复用；数据库、网络与应用实现保持私有。旧表名和公开 wire 字段为兼容数据而保留，不随文件重命名改变。

## 兼容与部署说明

旧 `_agent_*` 业务表名称及原迁移 SQL 保留，已有迁移账本不改写。Agent 数据方法和 HTTP 路由保留兼容入口，应用层经注入的契约转发；涉及执行 lease / fence / confirmation 的事务适配仍在 Agent 宿主。独立 Todo / Knowledge 数据测试不创建 Agent 会话表。

两个产品支持 SQLite、MySQL、PostgreSQL，默认 SQLite；宿主提供统一连接池、三库方言和唯一迁移账本。业务记录按 runtime / workspace / user 隔离，共享资料库通过成员权限控制。默认文件存储仍按单主机持久化；对象存储、多实例文件共享和收费订阅不属于本轮功能范围。真实模型、远程知识源及生产身份服务需要部署配置，凭据不会写入源码或前端。

新模块尚未发布远程标签，源码构建使用现有 `go.work`，两个产品的 `make` 命令已设置该工作区。已构建的独立二进制与各自 `frontend/dist/` 可直接部署。

## 验证结果

- 目录重整的完整 Go 回归通过：Agent、Agent SDK、Tools / Tools SDK、Todo、Knowledge、PM、Work；记录见 `docs/verification/module-unified-go-tests.txt`。
- 最终契约注入后的完整 Go 回归与两个服务重建通过，二进制校验值见 `docs/verification/module-unified-builds.json`。
- 契约注入验收：未装配 Knowledge 的 Agent 核心完成真实会话；未装配的知识接口返回明确错误。PM 需求、Work 会议和办公文档均覆盖模型写入、创建 Todo、版本、权限及同一定义重启，记录见 `docs/verification/module-unified-contract-tests.txt`。
- Cookie 命名空间修复后补验 PM、Work 和原 Identity 浏览器会话 / 重启测试通过。
- PM / Work 补充验收通过：流式模型先保存业务记录，再根据真实回执调用 Todo 创建跟进事项；原请求重试不产生重复业务记录。
- 前端类型检查通过，42 项现有前端状态测试通过，两个产品 production build 完成。
- 隔离浏览器验证：PM 保存需求及 PRD；Work 保存会议决策、行动项及办公文档，均显示真实版本回执。
- 模型集成使用本地流式协议夹具，未调用付费模型。浏览器验证使用隔离数据库，未修改原 Agent 用户数据。

详细记录见 `docs/verification/module-split-*.txt` 及 `docs/verification/module-unified-*.txt`。两个产品的启动说明分别位于相邻 `domainry-pm/README.md`、`domainry-work/README.md`。

三数据库补充交付见 [module-three-database-delivery.md](module-three-database-delivery.md)，包括宿主配置、独立模块与产品的真实三库验收、MySQL 启动链兼容修复。
