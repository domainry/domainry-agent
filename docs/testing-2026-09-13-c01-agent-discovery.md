# C01：独立 Agent、能力定义与接收方选择

日期：2026-09-13。沿用当前 checkout；涉及 Agent 和 Agent SDK。继续按[核心 TODO](agent-core-capabilities-todo.md)顺序实现，本记录只验收 C01，不代表后续条目完成。

## 能力定义与实例

宿主通过 `ConversationOptions.AgentDefinitions` 提供有版本的 `AgentSchema`。页面创建 Agent 时可选已有能力定义，也可自定义工具、Skill 和工作要求。同一份定义可创建不同名称、模型、启停状态和并行上限的多个实例；实例 ID、配置 revision、队列、执行会话独立。定义不是运行者，不参与委派身份关系。

实例保存定义 key、版本和摘要。引用定义时，指令和工具／Skill 由服务端按已装配定义复制，不采用请求伪造的定义正文；定义与 Skills 仍不能扩大部署工具上限。定义内容或版本改变后，旧实例必须重新保存配置，旧执行快照不会自行升级。模型、定义和价格注册表在装配时复制，调用者之后修改原始配置对象不影响已启动的服务。

依据：[SDK 契约](../../domainry-agent-sdk/conversation_agent_discovery.go)、[Agent 配置](../internal/application/conversation_agents.go)、[宿主配置](../internal/application/conversation_service.go)。

## 发现、核对和选择

侧栏“Agent 协作”→“Agent 目录”展示当前接单状态、正在执行／排队／等待处理的数量、配置版本和核实时间。“按工作要求选择 Agent”允许选择必需工具、Skill、当前会话的资料来源、任务类型和预估模型费用条件。表单修改后旧匹配立即失效，用户选择具体接收方后，原匹配要求与委派一起保存。

`POST /agent/agents/matches` 是只读动作，SDK、Module、SaaS 和远端客户端共用同一契约。`agent_list` 接受同样的要求，来源会话由服务端绑定，返回至多 16 个按依据排序的候选；截取时明确 `complete=false`，可以细化要求。页面目录保留所有当前用户的实例（现有上限 64 个自定义实例，加默认 Agent）。

匹配核对当前身份、部署和 Agent 工具范围、工具实际授权、连接／设置可用性、Skill、模型与定义有效性、接单队列及执行名额，并排除来源链中会形成循环的 Agent。所需资料复用来源审计，以新隔离会话作为读取范围；私有附件不能隐式传给另一个会话。所需来源随执行快照保存，执行、提交及后续成果读取继续检查，发现时的授权不作为以后读取的凭证。

排序先比较能否执行／需要排队，再参考已有交付验收（至少 3 个样本）、排队数量、可比较的模型费用、已有耗时样本及稳定 ID。满足能力但当前满载的 Agent 显示“接单后排队”；队列已满、权限不符、模型不可用等情况不能被推荐。实际入队与领取继续使用现有事务、容量 guard 和租约；页面显示的状态不占位，接单时重新检查。任务类型用于筛选同类历史，必需工具和 Skill 是显式能力条件，业务语义仍需根据 Agent 工作说明与实际任务判断。

依据：[匹配策略](../internal/application/conversation_agent_discovery.go)、[持久负载与历史](../internal/infrastructure/persistence/database/agent/conversation_agent_discovery_store.go)、[页面](../frontend/src/AgentDiscovery.tsx)。

## 成本与实际记录

宿主通过 `ConversationOptions.AgentModelPrices` 为模型注册键配置币种、每百万输入／输出 token 价格、依据及更新时间；未配置价格时明确显示“未知”。没有 token 估计和可用历史时也显示未知。配置价格不查询外部报价，不把未知当作免费。

用户／Agent 可以提供本次委派所有模型调用的预计总输入和输出 token；否则使用当前用户最近 200 次终态运行中，同一 Agent、revision、完整配置摘要、模型身份及所选任务类型的实际用量均值。模型返回 `null`、缺少输入／输出用量或仅返回总 token 时，不伪造可定价用量。缓存输入量在独立报告时纳入输入数，按普通输入价格作保守估计。

记录分别显示运行结束和交付验收，不把 `stop` 或任务 `completed` 当作成果被接受。历史窗口截断显式标记，旧定义／Skill 配置、旧模型和其他任务类型不会混用。排序只有在可接单候选的费用都已知且币种相同时比较金额，避免混币种比较或把未知费用排序为零。

估计只涵盖模型，不包含工具费用、未来重试、修订与跨 Agent 后续工作，也不是实际扣费限制。目标级累计费用、预算分配、协调收益和无进展诊断仍属 E05／C07，未记为完成。

## 验收证据

- [应用测试](../internal/application/conversation_agent_discovery_test.go)：能力缺失、满载排队、来源循环、当前撤权、私有附件、未知成本、按实际输入／输出定价、配置摘要／模型／任务类型隔离、模型目录截取和输入契约。
- [存储测试](../internal/infrastructure/persistence/database/agent/conversation_agent_discovery_store_test.go)：接单→排队→领取→完成→验收的真实持久状态；任务与运行不重复计数，换实例读取恢复，其他用户读不到负载与历史。
- [HTTP 场景](../internal/assembly/web/conversation_collaboration_test.go)：真实 Identity 和 SQLite；同定义创建独立实例、装配对象修改隔离、真实工具执行的 500 输入／100 输出 token 汇总、发现与强制接单要求、配置变化与撤权。
- [浏览器场景](../frontend/tests/collaboration.browser.mjs)：真实构建前端完成“无匹配→调整工具与费用条件→选择接收方→委派→实时执行→验收→刷新”；桌面和 390 px 页面边界检查。模型使用确定性夹具，结果不作为真实模型质量对照。

本轮通过相关 Go 包（包含架构边界）、SDK 全量测试、前端 61 项测试及构建；新增协作场景通过 `-race`。浏览器报告及截图本次保存在 `/tmp/domainry-peer-discovery-browser`，临时文件不作为长期仓库依赖，复验使用上面的测试源码。

```sh
go test ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/architecture ./remote ./internal/transport/http/module ./internal/capability ./module ./server
go test -race ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/assembly/web -run '^TestPeer' -count=1
npm --prefix frontend test
npm --prefix frontend run build
```

SDK 在相邻目录执行 `go test ./...`。浏览器使用已有 `AGENT_PEER_BROWSER` 测试开关和 `AGENT_NODE_BINARY`、`AGENT_PLAYWRIGHT_MODULE`、`AGENT_UI_TEST_OUTPUT` 参数；命令见[协作首批记录](testing-2026-09-13-peer-collaboration.md)。
