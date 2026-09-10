# Agent 对话页面

使用 React 19、Vite、Tailwind 4、shadcn/ui 和 Vercel AI Elements。这个页面同时支持 Identity 登录入口和本地 Playground，所有业务数据来自真实 Go HTTP Adapter 和 SQLite，没有浏览器假数据，也不直接调用模型。

带登录的入口见 [Identity Module 接入](../docs/identity-module.md)。构建后运行 `python3 scripts/run-agent-web.py`，默认地址为 `http://127.0.0.1:8091`。

## Playground 启动

在 Agent 仓库根目录执行：

```sh
npm --prefix frontend ci
npm --prefix frontend run build
export AGENT_CONVERSATION_PROVIDER=gateway
export AGENT_CONVERSATION_BASE_URL=https://models.example.com # 替换为实际服务地址
export AGENT_CONVERSATION_PROTOCOL=chat_completions
export AGENT_CONVERSATION_MODEL=glm-5.3-flash-free
# 在当前终端安全配置 AGENT_PROVIDER_API_KEY，勿提交到仓库。
go run ./cmd/domainry-agent-playground
```

打开 http://127.0.0.1:8090 。修改前端后重新构建并刷新页面即可，Go 服务读取 frontend/dist。服务重启后刷新页面更新本地 Cookie。

- `AGENT_PLAYGROUND_ADDRESS`：默认 `127.0.0.1:8090`，仅接受字面量 loopback IP。
- `AGENT_PLAYGROUND_DB`：默认系统用户缓存目录下 `domainry-agent/playground/agent.db`；macOS 为 `~/Library/Caches/domainry-agent/playground/agent.db`。
- `AGENT_PLAYGROUND_FRONTEND`：前端构建目录，默认相对于进程工作目录的 `frontend/dist`。
- Go 与 SDK 尚未发布的会话接口通过仓库现有 `go.work` 引用相邻 `domainry-agent-sdk`。

此命令使用固定的本地测试用户，限制 Host、Origin、跨站访问和本地 Cookie。它不是生产登录入口；生产环境仍使用宿主身份中间件和正式 Module/SaaS Binding。

## 组件与接口

官方组件通过 shadcn CLI 从 https://elements.ai-sdk.dev/api/registry/ 安装源码：

- `conversation`：滚动容器、空状态、返回底部。
- `message`：消息布局、Streamdown Markdown（包含代码、公式和 Mermaid 插件）。
- `prompt-input`：输入、Enter/Shift+Enter、停止/发送。
- `suggestion`：起始提示。
- `src/components/ui/`：上述组件依赖的 shadcn/ui 原语及 Dialog、Switch、Input。

安装来源及接口说明：https://elements.ai-sdk.dev/docs 。这些是官方组件源码，应用组合在 `src/App.tsx`；应用已组合持久工具执行、附件管理、资料库文档、个人事项及成果界面；联网搜索与模型选择仍未开放。

`src/api.ts` 映射 `docs/conversations.md` 中的产品接口。SSE 使用持久化事件序号、attempt 和 UTF-8 字节偏移拼接草稿；断线从 Run 快照恢复，不重复提交用户消息。未发送草稿及结果不确定的发送标识按会话保存在 localStorage，刷新后可恢复并复用同一幂等标识。历史、模型生成草稿、个人记忆始终由服务端保存；浏览器输入草稿尚未进入历史。

前端不使用 AI SDK 的默认 `/api/chat` 协议或 `useChat` 状态仓库，避免与 Go Agent 的持久化会话重复管理状态。AI Elements 的 `ai` 依赖用于组件类型。

当前未提供独立 Vite 开发服务器：页面与 API 由同一个 Go 本地服务提供，避免第二套身份和代理配置。

## 验证

```sh
npm --prefix frontend test
npm --prefix frontend run check
go test ./internal/transport/http/playground ./cmd/domainry-agent-playground
```

浏览器验收记录见 `../docs/testing-2026-09-09.md`。

## 日常交互

搜索框按会话标题过滤列表，Enter 或搜索按钮提交，清除按钮恢复列表。输入内容按会话自动保存在当前浏览器，切换与刷新后恢复，发送成功后清理；存储不可用时显示提示并保留当前页面内存副本。

完整用户消息和助手回复均有「保存为记忆」，点击后先预填表单，用户确认才写入记忆。长内容必须提炼到上限以内。记忆列表支持编辑，保存修改保持既有启用/停用状态。生成时冻结的上下文不会因编辑记忆而追溯变更。

运行错误显示中文处理建议，区分网络、限流、额度/账单、模型访问、超时和无效回复。原始上游错误正文不会进入页面。


## 附件另存到资料库

“附件 → 详情 → 另存到资料库”读取当前可访问的目标库；用户确认目标及可见范围后，后端将已保存的附件另存为独立文档。原附件保持私有，副本按目标库成员授权。页面保存有界重试回执，刷新后可继续原请求；来源文件不经浏览器再次上传。

实现与真实 Identity / HTTP / SQLite / 浏览器验证见[附件另存验收](../docs/testing-2026-09-10-attachment-library-copy.md)。`tests/knowledge-documents.browser.mjs` 使用隔离的 8092 测试宿主和新建 headless Chrome 配置，默认不读取用户浏览器 Cookie。执行需要 Playwright，可通过 `AGENT_PLAYWRIGHT_MODULE` 指定已有模块路径；测试宿主以 `AGENT_TOOL_UI_ACCEPTANCE=1 go test -race ./internal/assembly/web -run '^TestKnowledgeDocumentsIdentityHTTPAndPersistentOriginal$' -count=1 -timeout 18m -v` 启动，脚本结束时关闭它。
