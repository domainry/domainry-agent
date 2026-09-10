# Agent 复用外部账号登录

Agent 网页宿主支持通过 `domainry-identity-bridge` 接入已有账号服务。外部模式不打开 Identity 账号数据库，不创建本地密码或刷新会话。浏览器携带配置的 Cookie，Bridge 调用已有校验接口，成功后生成 SDK Principal。

每个外部账号一个个人 Workspace；首次登录在宿主同一事务中写入 Workspace、外部归属和应用绑定。重新登录和重启复用原归属。Agent 会话、记忆、文件、后台工具授权和 SSE 使用该 Principal 的 Workspace 与用户。启动配置的 Workspace 在此模式下只是安装级目录作用域。

## 构建与配置

Bridge 尚未发布，外部入口显式使用 `external_identity` 构建标签。Go workspace 需要引用本仓库、Agent SDK、Identity SDK、Identity、Connectors 和 Bridge 的当前源码；普通构建不依赖未发布的 Bridge 包。

```sh
GOWORK=/absolute/path/source.work go build -tags external_identity \
  -o /absolute/path/agent-web ./cmd/domainry-agent-web
```

| 环境变量 | 用途 |
| --- | --- |
| `DOMAINRY_IDENTITY_BRIDGE_CONFIG_FILE` | 外部账号 JSON，参考 Bridge 的 examples/token-validation.config.json |
| `AGENT_WEB_EXTERNAL_ROLES_FILE` | 角色目录 JSON 数组，参考本仓库 examples/external-personal-roles.json |
| `AGENT_WEB_APPLICATION_KEY` | 必须与 Bridge application_key 相同 |
| `AGENT_WEB_WORKSPACE_ID` | 稳定的安装级目录作用域 |
| `AGENT_WEB_RUNTIME_ID` | 稳定的 Agent Runtime 标识 |
| `AGENT_WEB_DB` | 独立的持久化数据库路径 |
| `AGENT_WEB_ADDRESS` | 监听地址，如 127.0.0.1:8443 |
| `AGENT_WEB_ORIGIN` | 浏览器精确 HTTPS origin，如 https://agent.example.com:8443 |
| `AGENT_WEB_TLS_CERT` / `AGENT_WEB_TLS_KEY` | 直接提供 HTTPS 时的证书和私钥；也可用可信反向代理终结 TLS |
| `AGENT_WEB_FRONTEND` | 构建好的 frontend/dist 路径 |

外部模式不要求 `AUTH_JWT_SECRET`、`IDENTITY_DATA_SECRET_KEY`、`AUTH_DEFAULT_PASSWORD`。浏览器使用 Cookie 以支持原生 SSE；Cookie 名、登录链接、origin、校验 URL、请求字段和响应映射全部来自配置。初始角色必须存在于角色目录中。示例角色只授予个人及文件工具权限，不自动开放业务系统、知识库或账号管理。独立宿主不支持 Runtime 业务初始化数据；非空 application_bootstrap 会拒绝创建并回滚。

## 浏览器流程

打开 Agent，点击配置的账号服务登录按钮；现有服务设置 Cookie 后，使用其支持的回跳参数返回 Agent。`/app/session` 验证 Cookie 并获得个人 Workspace。不会调用本地密码登录、刷新或修改密码接口。账号切换后，旧请求和 SSE 不得继续使用原会话。

Cookie 必须覆盖 Agent 的域名和路径。localhost 收不到另一个域的 Cookie。本地验证可将 Cookie 已覆盖的测试子域名在 hosts 中映射到 127.0.0.1，并安装浏览器信任的 HTTPS 证书；无需先配置公网 DNS。正式上线需要对应 DNS 和正式证书。

仅在账号服务提供可导航的注销页面时配置 browser.logout_url；缺省时不显示退出按钮，应在账号服务管理登录状态。不要将只接受 POST 的注销 API 填成导航链接。

## 验证边界

```sh
GOWORK=/absolute/path/source.work go test -race -tags external_identity \
  ./internal/assembly/web -run 'TestExternalAccountAgentOwnershipAndRestart|TestIdentityModulesBrowserOwnershipAndRestart'
npm --prefix frontend test
npm --prefix frontend run build
```

测试使用真实 Bridge、持久化宿主、Agent 模块与浏览器边界，校验服务使用 TLS 协议夹具。覆盖业务拒绝、停用账号、两个账号的数据隔离、对话执行、后台工具授权、Workspace 伪造、SSE、重启恢复及禁止本地刷新。这些测试不代表某个真实账号已完成登录，实际账号验收仍需浏览器完成已有登录流程。

模型配置与账号登录独立。只验证登录时可设置 `AGENT_CONVERSATION_ENABLED=true`；无模型配置时页面显示模型未就绪。实际对话仍使用现有 AGENT_CONVERSATION_* 配置及进程凭证。

## 本机凭证复用

Python 启动和真实服务验收脚本会读取本机私有的 `web-credentials.json`。macOS 默认目录为 `~/Library/Application Support/domainry-agent`，其他系统为 `~/.config/domainry-agent`；可用 `AGENT_WEB_CREDENTIALS_FILE` 指定其他文件。文件必须归当前用户所有且不能授予组或其他用户权限（例如 `0600`）。只接受 `AGENT_PROVIDER_API_KEY`、`AGENT_CONVERSATION_MODEL_API_KEY`、`AGENT_KNOWLEDGE_API_KEY` 三种字符串配置，显式进程环境优先。这个文件不要加入仓库；Go 二进制仍只读取宿主提供的环境变量。

知识工具需同时配置服务和角色权限。外部模式每次请求重新检查实际个人 Workspace 的归属，再解析该用户的文档权限；安装级 `AGENT_KNOWLEDGE_WORKSPACE_ID` 不替代用户的 Workspace。仅授予 `knowledge_search` / `knowledge_read` 工具权限不会自动授予私有文档组权限，后者仍需 `AGENT_WEB_KNOWLEDGE_PERMISSIONS` 与角色授权映射。
