# Runtime 业务会话网页与真实模型验收

日期：2026-09-10。J01 / J02 的目录发现与只读业务查询已完成本批网页验收；关联查询、业务写操作、流程执行、报表和独立外部连接仍分别保留在 J03–J05、N、F 中。

## 本批实现

- 网页适配器由 Agent SDK `browsergateway.NewHandler` 提供；Agent 的 `web.NewHandler` 委托到同一实现，接收宿主已经装配的 Identity / Agent bindings 和静态页面，不重复打开数据库或启动模块。
- Runtime 公开 `bootstrap.ConversationWebHandler`，把现有聊天页面接到实际 Runtime 会话路由。浏览器请求先经过同源、当前 Identity 会话和用户范围检查，再进入 Runtime 原有授权、准入与审计链路。
- 转发到宿主时只使用服务端已验证的访问令牌和工作区，覆盖浏览器自带的 Authorization / 工作区头并移除 Cookie。宿主拒绝请求时不会绕回 Agent 直调入口。
- `scripts/test-agent-business.py` 可用协议夹具或真实模型启动实际 Identity / Runtime / Agent 与合成客户数据；`--browser` 服务现有前端构建。密钥仅进入进程环境，交互输入不保存到配置或验收记录。

## 实际验收

使用 Verdent `gpt-5.6-sol` / Responses、实际 Identity Module、Runtime RecordApplicationService、Agent Module、HTTP 路由和 SQLite。客户记录及角色为临时合成数据；Data Exchange 使用 Runtime 既有测试工厂，没有通过它伪造业务查询结果。

首次运行 `crun_5c8b1f5f643170e9d8a62621106b5891` 共 6 个模型步骤、7 次工具调用：3 次目录、3 次记录查询、1 次详情读取。自动断言实际客户名称和余额、排除另一所有者的数据，并验证完整模块关闭重开和两种权限撤销。日志和 JSON 记录的是这一自动阶段；后续浏览器步骤由实际 UI 操作核对，不把 JSON 中的自动断言称作浏览器断言。

浏览器会话 `conv_937801a3473cdc3d081d64959b36c8ab` 完成：

1. 登录后读取真实模型回复，目录发现客户及 `customer.rename`，每页 1 条按名称升序沿游标查到第三页，读取第一条真实 ID 的详情。结果为 Acme / 10、Beta / 20、Gamma / 30；第二页工具结果包含 Beta / 20 和后续游标。
2. 从聊天输入框重新发起查询，7 次工具调用后展示三行名称 / 余额表格；关闭后重新打开页面仍保留两轮回复，表格布局正常。
3. 通过实际 Identity 角色分配接口切换到不可读取余额字段的角色。刷新后两轮回复隐藏，第一轮的旧运行详情也隐藏工具结果与回复；保留登录和原始用户请求。
4. 恢复角色后点击旧运行“刷新记录”，原有工具结果、客户名称和余额恢复。
5. 切换到保留业务工具权限、但没有 `customer.read` 的角色，刷新后两轮回复隐藏。恢复角色后完整关闭并重开实际 Runtime 与 Identity；浏览器刷新仍保持同一登录、会话及两轮事实。
6. 重启后从网页发起第三轮查询，仍完成 3 页游标与第一条详情读取。生成期间打开第一轮处理记录，观察主会话从“正在处理”变为“已保存”；窗口中的旧时间、7 次调用和原回复保持不变。关闭窗口后第三轮最新结果已到达，无需刷新。
7. 关闭临时验收标签页并结束测试服务，测试进程以 PASS / 退出码 0 结束。

角色切换和重启通过仅存在于本地测试的 `/__acceptance/` 控制入口触发，内部调用实际服务；这些入口没有加入产品路由。模块重启覆盖数据库关闭重开和 worker 重建，浏览器阶段监听器保留；不将其描述为操作系统进程崩溃恢复。

## 复现与证据

在 Agent 仓库运行：

```sh
python3 scripts/test-agent-business.py
python3 scripts/test-agent-business.py --live --browser --model gpt-5.6-sol --protocol responses
```

浏览器模式要求先构建 `frontend/dist`；真实模式读取已有模型配置或环境变量，缺少凭证时在终端隐藏输入。脚本将相邻 Agent SDK、Connectors、Identity、Runtime 组成临时 go.work；存在相邻 Identity SDK 时一并使用，以匹配未发布的源码契约。测试输出临时网址与合成登录信息；验收后 `POST /__acceptance/finish`，监听器和临时服务关闭。测试控制入口不可部署到生产。

- 实际服务与协议夹具：`/tmp/domainry-runtime-business-web-fixture.log`，通过。
- 真实模型和本次浏览器：`/tmp/domainry-runtime-business-web-live.log`，通过，测试主体耗时 810.33 秒（包含人工浏览器操作等待）。
- 自动阶段证据目录：`/var/folders/5w/z9d657ts32zg837n0bvff0yh0000gn/T/domainry-business-agent-chn7ml0j`，包含 `business-run.json` 和 `business-acceptance.json`，无模型凭证。
- Agent 浏览器宿主转发专项：`/tmp/domainry-agent-web-host-router.log` 与 `/tmp/domainry-agent-web-host-router-race.log`，通过，覆盖伪造头覆盖及宿主 503 不被绕过。
- Agent 全量测试 / 静态检查：`/tmp/domainry-agent-business-web-full.log`、`/tmp/domainry-agent-business-web-vet.log`，通过。
- 前端构建：`/tmp/domainry-agent-business-web-frontend.log`，通过，保留已有大包体积提示。
- Runtime bootstrap 静态检查：`/tmp/domainry-runtime-business-web-vet.log`，通过；执行时的依赖状态与下面的新增 SDK 变更区分。

补充 race 首次编译发现当前 Runtime 新增 `identitysdk.ExternalWorkspaceCreate`，已发布依赖没有该契约（`/tmp/domainry-runtime-business-web-race.log`）。未修改业务实现绕过该检查；验收脚本已补齐相邻 Identity SDK 的组合。使用匹配本地 SDK 后，实际 Runtime / Identity / Agent 网页及重启测试的 race 检查通过：`/tmp/domainry-runtime-business-web-race-local-sdk.log`，63.584 秒。更新后的脚本夹具验收也通过，见 `/tmp/domainry-business-script-fixture.log`；这些结果使用本地源码，不代表发布依赖已通过。

## 交付边界

J01 / J02 的目录、字段 / 行权限、类型化过滤、游标、详情和来源复核，还结合 [目录契约专项](testing-2026-09-10-business-catalog.md) 与 [Runtime 读取专项](testing-2026-09-10-runtime-business.md) 的证据验收。真实模型与浏览器客户场景不代替每个动作 / 流程契约的专项验证。

本批提供 Module 宿主网页组合。普通 `cmd/domainry-agent-web` 仍只装配 Identity / Agent，不会自动连接任意 Runtime。独立部署的外部连接、报表、业务写操作与发布依赖仍未完成；没有改写发布锁，也没有宣称整个 Runtime 全量回归或 H04 通过。
