# H04 依赖发布与脱离工作区验收（2026-09-13）

H04 已完成。SDK、owner 模块、Runtime、Agent 及两个产品宿主已经按依赖方向发布；最终消费者只使用远端版本，在 `GOWORK=off` 且正式 `go.mod` 没有 `replace` 的条件下完成构建、测试和组合验收。

## 最终发布链

| 层级 | 最终版本 |
| --- | --- |
| 公共契约 | Agent SDK `v0.1.12`、Identity SDK `v0.1.10`、Lifecycle SDK `v0.1.10`、Scheduler SDK `v0.1.7` |
| 连接与身份 | Connectors `v0.1.3`、Identity Bridge `v0.1.2`、Identity `v0.2.11`、Integration `v0.1.13` |
| 生命周期与执行 owner | Lifecycle `v0.1.11`、Scheduler `v0.1.10`、Notification `v0.1.3`、Audit `v0.1.10` |
| 已拆模块 | Tools `v0.1.1`、Todo `v0.1.1`、Knowledge `v0.1.2` |
| 组合与产品 | Agent `v0.1.20`、Runtime `v0.1.43`、PM `v0.1.3`、Work `v0.1.3` |

使用全新的 `GOMODCACHE`、`GOPROXY=direct` 和 `GOSUMDB=off` 从 Git 远端下载上述 19 个精确标签，结果为 19 / 19 成功。每项都返回 `Origin.Ref=refs/tags/<version>`、远端提交哈希、模块校验和及 `go.mod` 校验和；完整结果见[远端下载清单](evidence/2026-09-13-h04-releases/release-downloads.json)。

Runtime `v0.1.43` 的模块锁使用 Agent `v0.1.20`、Agent SDK `v0.1.12`、Identity `v0.2.11`、Identity SDK `v0.1.10`、Lifecycle `v0.1.11`、Lifecycle SDK `v0.1.10`、Scheduler `v0.1.10`、Scheduler SDK `v0.1.7`、Notification `v0.1.3` 和 Audit `v0.1.10`。11 个 owner Binding 的能力摘要保持稳定，最终模块集合哈希为 `0c224caafa2cd50ceae499ba6e546e346fd21448beff365bc37cc3bdc9b12578`。

## 公共契约兼容与边界

- Identity SDK 将 Module 请求的应用范围改为从可信 context 解析；浏览器绑定继续从已认证会话形成范围。最终 `v0.1.10` 删除了请求正文中的旧 `Application` 字段，owner 不再相信调用正文提供的应用身份；Identity Bridge、Identity、Runtime、Agent 和产品宿主均升级到新契约，SDK 的外部消费者编译测试覆盖全部公开 Go package。仍使用旧字段的源码需要迁移，这项变化没有伪装成向后源码兼容。
- Agent 的容量、生命周期、计划和审计能力仍经可选 SDK 端口进入；Runtime 在 bootstrap 组合 owner，Agent application 不导入 Identity、Runtime、Scheduler、Lifecycle、Notification、Audit 的实现或存储。
- Runtime 的 11-Binding 门禁从发布模块重新计算能力摘要，并用外部临时宿主编译公共 Module／SaaS 入口。正式 `go.mod` 扫描未发现本地 `replace`。
- Integration `v0.1.13` 与 Connectors `v0.1.3` 继续拥有公开网页连接和传输。Web Search／Web Fetch 只消费 llm-proxy 已有的 `POST /tool/web_search` 与 `POST /tool/web_fetch_jina`；Agent 模型 Provider 不经过 llm-proxy，本批没有修改 llm-proxy。

具体检查结果见[架构审计](evidence/2026-09-13-h04-releases/architecture-audit.json)。

## 脱离 go.work 的验证

- Runtime：`GOWORK=off ./scripts/ci/verify_runtime_composition.sh pr` 通过，覆盖模块下载校验、11-Binding 精确版本组合、外部 runtimehost 编译、跨模块 golden chain，以及 action／notification／transaction／workflow 持久化和重启。
- Runtime：`GOWORK=off go test ./...` 通过；关键路径 `-race` 覆盖 `pkg/runtimeext`、`pkg/runtimehost`、action、upload、runtime bootstrap、workflow 数据库和 HTTP uploads；`go vet ./...`、`go build ./...` 通过。
- Agent：`GOWORK=off go mod verify`、`go test ./...`、`go vet ./...`、`go build ./...` 通过。真实 Identity HTTP／Agent HTTP／SQLite／宿主重启的四场景专项 `-race` 共运行 702.537 秒，验证批准后恢复、一次执行后拒绝、批准前撤权和未知原回执恢复；外部副作用次数分别为预期值。
- Agent 的外部工具超时专项在普通 `-count=10` 和 `-race -count=3` 下通过；Integration 同一专项覆盖 llm-proxy 两个精确工具路径。测试只增加协议与超时证据，没有改变 Integration 或 llm-proxy 的生产调用边界。
- PM 与 Work：最终各自固定 Agent `v0.1.20`，在 `GOWORK=off` 下完成 `go mod verify`、整库测试、架构／装配测试、vet 和 build。
- Connectors `v0.1.3`、Todo `v0.1.1`、Knowledge `v0.1.2`、Tools `v0.1.1`：从对应发布标签脱离工作区完成模块校验、整库测试、vet 和 build。

Runtime 计划跟进 E2E 曾在重启后的第二个窗口遇到可重试 SQLite 写竞争。数据库回执表明回调被正确标成 `failed_retryable`，测试宿主此前没有模拟 Scheduler 的同键重试。验收驱动改为使用相同签名正文和幂等键最多重试五次，持久失败仍立即失败；该场景普通 `-count=25` 和 `-race -count=5` 通过，随后 Runtime 全量与组合门禁通过。该修正只在测试宿主补齐真实调度语义，没有放宽生产幂等规则。

## 限制

H04 证明发布标签、公共契约和脱离工作区的组合可用。实际 Google Workspace／Microsoft 365 账号没有配置，本项不把协议夹具解释为厂商 OAuth 验收；该外部条件留在 H08。PostgreSQL／MySQL 和多实例部署同样留在 H08。H06 的崩溃、未知结果、重复确认、取消、SSE 重连整体验收不由本项替代。

全部结构化结果见[机器清单](evidence/2026-09-13-h04-releases.json)。
