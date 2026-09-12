# F01 账号页面、真实 Identity 与浏览器回调增量

本批完成 F01 的账号产品页面、同源回调、公开模块装配和对应端到端验收。F01 仍未完成：下一步是 Tools 所属的持久工具开关和当前连接可用性；不进入 F02。实际厂商 OAuth 应用尚无，授权／换码使用隔离协议夹具，不能据此宣称真实 Google／Microsoft 账号验收。

## 当前拆库后的实现边界

- Agent SDK 的 [browsergateway](../../domainry-agent-sdk/browsergateway/modules.go) 接受中立 `modulehttp.Adapter`，按 owner 路径、动作权限、当前 Identity、密码状态和页面 scope 挂载。每个模块请求解析当前 AccessBundle，cookie 投影不充当完整权限。SDK 没有新增 Integration 或 Provider 实现依赖。
- [产品组合](../internal/assembly/web/integration.go) 只选 Integration 公开契约中的十个账号／OAuth 动作。连接执行、旧配置和密钥管理不暴露给页面。权限定义在启动时登记；不会在启动时自动授予角色。
- 管理员点击「启用管理员账号管理」后，复用 [Identity 权限编辑](../internal/transport/http/product/setup.go) 做版本校验、审计和明确提交。目标角色／权限／范围来自宿主声明，浏览器不能指定；普通用户不能执行此初始化。已有产品工具初始化复用同一编辑过程，业务工具清单保持原产品定义。
- [独立 SaaS 组合](../internal/assembly/product/integration.go) 仅使用 Integration SDK remote 和公开 Module facade。三项环境变量必须同时配置，契约不匹配拒绝；完全未配置时保持可启动并显示未连接。独立服务仍拥有凭证、会话、刷新、迁移和密钥。
- [共享账号页面](../frontend/src/ExternalAccountsDialog.tsx) 使用 Integration 浏览器 SDK，提供应用登记／修订、个人／共享范围、明确 Scopes、授权导航、状态、连接测试和带影响说明的撤销确认。Client Secret 只输入不回显，保存或卸载后清除。原 Agent、Work、PM 复用同一组件和回调源码。
- [静态回调](../frontend/src/oauth-callback.ts) 只将 code/state 留在内存，先清除 URL，再读取实际认证模式和当前会话。原身份 scope 不一致时不提交换码；成功回执必须匹配原会话 ID。浏览器持久记录仅有 ID／身份范围／返回 hash，不存 token、code、state 或导航 URL。
- 跨站 GET 例外仅限预先配置的静态 HTML 回调页，无命令、身份反射或查询反射。命令仍要求同源、当前登录和 scope。回调使用 no-store／no-referrer；换码成功但响应丢失时，主页面按 ID 读取持久回执。
- 新逻辑在共享前端和宿主组合，未把 OAuth／凭证表／厂商协议搬到 Agent 应用层。各库仍使用原 checkout，没有分支、worktree、go.mod replace 或依赖发布。

## 验证与直接证据

| 项目 | 实际结果 |
| --- | --- |
| [真实 Identity HTTP](../internal/assembly/web/integration_accounts_test.go) | 管理员默认拒绝、显式初始化、普通用户不能初始化／读应用、个人账号与 OAuth 会话隔离、共享账号范围、固定测试、撤销、旧 scope 拒绝、跨站边界、撤权在重启后仍生效及原回调回执恢复通过。Google Provider 和 SQLite／加密持久化真实运行，issuer 为本地 HTTP。两次成功换码、一次连接探测。 |
| 网关拒绝测试 | 12 个非法／保留静态路径、没有 Permission 的模块动作、重复模块路由均拒绝。没有给服务命令添加匿名回调入口。 |
| [浏览器 8 场景](../frontend/tests/external-accounts.browser.mjs) | 构建后网页实际登录、启用权限、登记和空密钥修订应用、导航回调、双用户隔离、拒绝授权、跳转中切换身份、换码响应丢失、刷新与宿主重启、权限撤销／恢复、账号撤销及手机显示全部通过。最终场景 15.07 秒，包 16.510 秒，JavaScript 错误 0、敏感 Referer 0；换码 2 次、探测 1 次，无重复换码。 |
| [实际 SaaS 进程与产品 Identity](../internal/assembly/product/integration_test.go) | 使用单独构建的生产 Integration 可执行文件，结合实际 Identity 和产品网关。相同数据库三次启动，契约不匹配拒绝、显式权限初始化、配置保存、官方 S256 导航、重启 pending、用户拒绝、再次重启 rejected 均通过。场景 4.35 秒，包 5.235 秒，没有真实厂商换码。 |
| 回归 | Agent 和 Agent SDK 全量、Work／PM 全量、前端状态测试通过；账号 HTTP／网关 race 通过（包 34.306 秒）；相关宿主／产品／网页 vet 通过。共享前端及 Work／PM 最终前端构建通过。Agent 全量是在新增 SaaS 进程测试前运行；该进程测试随后独立通过。 |
| 视觉复核 | 人工查看实际桌面配置和手机账号截图。补齐弹窗自身边界和内容滚动宽度检查，最终手机截图内容无横向裁切。 |

[最终网页报告](evidence/2026-09-11-f01-account-browser/browser-final/report.json)、[宿主副作用审计](evidence/2026-09-11-f01-account-browser/browser-final/host-audit.json)、[手机截图](evidence/2026-09-11-f01-account-browser/browser-final/mobile-revoked.png)、[全部原始日志](evidence/2026-09-11-f01-account-browser/)、[源文件与证据哈希](evidence/2026-09-11-f01-account-browser.json)。

主要复现入口（从 Agent 根目录；已有本地 go.work）：

```sh
go test ./internal/assembly/web -run '^TestExternalAccounts(CurrentIdentity|Gateway)' -count=1 -v
go test -race ./internal/assembly/web -run '^TestExternalAccounts(CurrentIdentity|Gateway)' -count=1 -v
npm --prefix frontend run build
AGENT_ACCOUNTS_BROWSER=1 AGENT_NODE_BINARY=/absolute/path/to/node AGENT_PLAYWRIGHT_MODULE=/absolute/path/to/playwright AGENT_UI_TEST_OUTPUT=/absolute/output go test ./internal/assembly/web -run '^TestExternalAccountsBuiltBrowser$' -count=1 -v
go build -o /tmp/domainry-f01-integration-server github.com/domainry/domainry-integration/cmd/integration-server
AGENT_INTEGRATION_TEST_BINARY=/tmp/domainry-f01-integration-server go test ./internal/assembly/product -run '^TestIntegration' -count=1 -v
```

## 失败记录与范围

初始测试夹具的 Dialect 类型、必需 Trigger 端口、用户 ID、错误状态码和幂等请求头先后校正，初始日志保留。一次 SaaS 重跑遇到同一工作区 Identity MySQL 源码正在修改时的编译错误；本批未修改该库，源码恢复可编译后重新验证通过。浏览器初次选择器遗漏按钮说明文本；追加身份切换场景后，登录成功自动打开待核对窗口，更新选择器接受该实际流程。

第一次手机截图发现裁切，页面 scrollWidth 检查未覆盖 fixed 弹窗，因此早期 7 场景通过不能作为最终手机验收。已修正 grid 子项／fieldset 的最小宽度，并在视口尺寸过渡完成后核对弹窗边界及其内部滚动宽度；最终 8 场景报告和截图为有效证据。所有初始报告保留，没有覆盖为通过。

工具开关及其执行／恢复／旧结果复核仍未交付。连接测试由已选 Provider 的固定测试操作完成，真实厂商的 token/scopes/API 权限兼容仍须实际账号验证。Integration 撤销停止本服务连接使用，不代表撤销厂商后台的全部 consent。Google／Microsoft 真正登录同意、依赖 tag 发布及目标部署验收仍待对应条件具备，当前不勾选 F01。
