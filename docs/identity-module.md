# Agent 网页入口接入 Identity Module

复用外部账号、每人一个 Workspace 的模式见 [外部账号登录](external-account-login.md)。本文描述内置 Identity 模式。

`cmd/domainry-agent-web` 在同一进程中装配已发布的
`domainry-identity/module` 和本仓库 `module`。go.mod 基线为 v0.2.0；当前 go.work 使用相邻 Identity 源码，包含权限恢复发布修复，修复版本尚待发布。Identity 使用
`OpenWithDatabase`，Agent 使用 `OpenModule`，不经过 Identity SaaS 或 loopback HTTP。
原有 `cmd/domainry-agent` 仍是服务间 SaaS 入口。

## 启动

```sh
npm --prefix frontend ci
npm --prefix frontend run build
python3 scripts/run-agent-web.py
```

默认地址 `http://127.0.0.1:8091`。脚本优先使用现有环境变量，未配置
Gateway API Key 时从终端隐藏输入，Key 只进入进程环境，不写入配置。
`go.work` 引用相邻 Agent SDK 的会话接口、Connectors 的知识库 Provider，以及 Identity 的权限恢复发布修复。
实际模型与知识库地址从 `AGENT_WEB_SERVICES_CONFIG` 指定的 JSON 配置读取，
默认 `~/Library/Application Support/domainry-agent/web-services.json`；环境变量优先。
没有地址配置时，脚本要求输入模型服务源地址，不再绑定服务商。
共享服务密钥使用 `AGENT_PROVIDER_API_KEY`，模型和知识库分别可用
`AGENT_CONVERSATION_MODEL_API_KEY`、`AGENT_KNOWLEDGE_API_KEY` 覆盖。

脚本首次生成稳定的 Identity 签名密钥、数据加密密钥和随机初始密码，
以 `0600` 权限保存在仓库外。macOS 默认位置是
`~/Library/Application Support/domainry-agent/web-identity.json`，
其他 Unix 系统是 `~/.config/domainry-agent/web-identity.json`。
`AGENT_WEB_IDENTITY_CONFIG` 可以覆盖路径。Provider 密钥不在该文件中。

Identity 当前 module 初始化逻辑提供 `admin@example.com` 及内置角色种子用户，
初始密码采用 `AUTH_DEFAULT_PASSWORD`。脚本把首次登录信息单独写入同目录
`web-first-login.txt`，同样为 `0600`。首次登录须设置新密码；此文件不会
跟随修改更新，重启也不会重置数据库中的密码。Agent 不自行创建账户表、
密码哈希算法或用户管理接口。

正式环境可直接运行 `go run ./cmd/domainry-agent-web`。必填环境变量
`AUTH_JWT_SECRET`、`IDENTITY_DATA_SECRET_KEY`、`AUTH_DEFAULT_PASSWORD`，
各至少 16 字符，前两者须不同，同时遵守 Identity 自身生产配置校验。
脚本将 `AUTH_ACCESS_TTL` 默认设为 `15m`，支持环境覆盖。

| 配置 | 默认值 / 用途 |
| --- | --- |
| `AGENT_WEB_ADDRESS` | `127.0.0.1:8091` |
| `AGENT_WEB_ORIGIN` | `http://` 加监听地址；精确 origin，不包含路径 |
| `AGENT_WEB_RUNTIME_ID` | `agent-web`；持久化后应保持稳定 |
| `AGENT_WEB_WORKSPACE_ID` | `agent-workspace`；当前宿主服务的工作空间 |
| `AGENT_WEB_APPLICATION_KEY` | `domainry-agent-web`；Identity token audience |
| `AGENT_WEB_DB` | 系统用户缓存目录下 `domainry-agent/web/agent.db` |
| `AGENT_WEB_FRONTEND` | `frontend/dist` |

非 loopback 监听须配置 HTTPS 公网 origin 和 `APP_ENV=production`。
HTTPS 由同源反向代理终止，代理保留原始 Host；宿主不信任请求传入的身份头或
`X-Forwarded-Host`。HTTPS origin 启用 Secure Cookie。

## 请求与会话边界

- `internal/assembly/web` 负责两个 module 的装配与生命周期。
- `internal/transport/http/web` 使用 Identity SDK browser gateway 的登录、刷新、
  退出、当前会话及改密处理器。当前提供密码登录，没有注册、组织管理、MFA
  或第三方登录界面。
- Identity 管理刷新凭据轮换和撤销。宿主将其签发的 access token 放入附加的
  HttpOnly Cookie，令原生 EventSource 也能鉴权。浏览器 JSON 和前端存储不含
  access/refresh token，宿主没有第二套登录会话库。
- 每个受保护请求通过 token verifier 和 `CurrentSession` 检查 audience、用户、
  工作空间及会话状态。首登改密前禁止访问 Agent；SSE 每次输出前重新确认会话
  有效，因此已打开的连接也不能绕过退出撤销。
- 只挂载 Agent 声明为 `authenticated` 的 conversation actions。会话服务继续按
  runtime/workspace/user 检查消息、事件、运行和个人记忆归属。
- `/app/config` 返回非敏感部署信息；`/app/session` 返回验证后的用户信息、
  草稿范围和模型状态。Agent 请求使用 `X-Agent-Scope`，SSE 使用相同的 scope
  查询值。它是三个身份字段的 SHA-256，不是凭据；与真实身份不符时拒绝请求，
  防止旧标签页在 Cookie 已切换后向新账号误发消息。
- 所有写请求须匹配配置 Origin；Host 与跨站请求也受校验，不开放 CORS。

## 前端与数据库

`SessionApp` 负责登录、首登改密、恢复及退出；`App` 接收 `AppSession` 和退出
回调，继续复用 AI Elements。`session.ts` 用 Web Locks 串行化跨标签页刷新，
通过 BroadcastChannel 同步身份变化，丢弃旧身份在途响应。首次 401 后恢复会话，
只有身份范围相同才重试原请求。

草稿按服务端确认的 runtime/workspace/user 隔离。退出卸载历史与记忆视图，
清除会话选择；草稿保留在对应账号的浏览器范围中。Playground 保留原来的
`local-playground` 范围和数据，前端通过 `/app/config` 区分模式。

独立网页宿主目前支持 SQLite 单进程，持有数据库旁的文件锁排除第二个宿主。
Agent、Identity 和 Identity 内部的 Metadata/Audit 共用一个池和唯一的
`_schema_migrations`，迁移按 owner/version 区分。回调失败保留 dirty 记录，
重启拒绝重复执行或接受 checksum 漂移；DDL/DML 使用 domainry-orm。
此入口使用独立的新数据库，不自动接管旧 Playground/SaaS 的迁移账本，
也不把固定测试用户的历史分配给真实账号。

## 验收

工具执行扩展：宿主会注册 `agent.conversation_tools.time_now`、`calculate`、
`history_search`、`history_read`、`memory_search` 对应的完整 Action / Permission
键，source owner 为 `agent:conversation_tools`。已有角色不会自动获得新权限；
管理员通过 Identity 角色权限发布入口显式授予所需工具，个人资料使用 `owner`
数据范围。Identity 的 owner 策略读取服务端提供的 `owner_user_id`，Agent 的实际
存储查询同时限制 runtime/workspace/user。

每次工具调用和恢复均通过 `Principals().Resolve` 获取当前身份及 AccessBundle，
不长期保存浏览器令牌。模型发起下一次请求前还会重新检查已消费的工具数据。
用户时区优先读取实时 Identity profile，宿主默认值由
`AGENT_CONVERSATION_TIMEZONE` 设置（未配置为 UTC），工具可显式请求其他 IANA 时区。
文档、业务数据和外部账号仍需各自的资源授权接入，详见能力清单。

```sh
go test -race ./internal/assembly/web ./internal/infrastructure/persistence/webhost ./internal/transport/http/playground
npm --prefix frontend test
npm --prefix frontend run build
```

真实 Identity module 测试覆盖登录、强制改密、Cookie 轮换、旧 token 撤销、
已打开 SSE 撤销、双用户和跨工作空间隔离、进程重开、旧标签页身份错误与 CSRF。
前端测试覆盖刷新重试、切换账号后不重放写请求及丢弃旧响应。
浏览器验收记录见 `testing-2026-09-09.md`。
