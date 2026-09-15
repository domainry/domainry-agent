# K08 完整编码执行环境验收

日期：2026-09-16。本轮完成 P3 的完整编码执行环境；K08 验收后，完整清单进度为 **23／25**，按约定不启动 V01。

## 组件与 owner 边界

- Agent SDK 定义 12 个稳定模型工具及 `ConversationCodingRuntime` 端口：文件读取、字面搜索、带版本写入、精确编辑，PTY 打开、输入、读取、关闭，后台进程启动、读取、停止，以及 LSP 定义／引用查询。
- Runtime 显式装配本机编码工作区并拥有文件、PTY、后台进程、OS 沙箱和语言服务进程。未提供 `AgentCodingWorkspace` 时，Agent 不发布这些工具。
- Agent 拥有工具目录注入、当前 Identity 动作授权、冻结 Profile、输入／输出 Schema、写确认、执行账本、幂等键、未知效果恢复、取消与页面投影。产品 ToolHost 返回的同名定义会被删除，不能替换 Runtime 实现。
- `domainry-tools` 继续拥有可复用的业务工具注册、选择与适配，包括记录、日历、邮件、Web、MCP、报表、分析和调度。截图中的共享执行四工具也不属于 `domainry-tools`：它们读写 Agent 自己的委派、发布和执行账本，因此由 Agent SDK 与 Agent application 持有。
- 长期成果继续由既有 Artifact owner 保存和导出；工作区文件和命令输出只是运行证据。

## 授权、确认与回执

每个编码工具使用独立的 `agent.conversation_tools.coding_*` Action。SDK 的 `ConversationToolActions` 将工具定义投影为 Identity permission；目录生成和实际执行都会用当前已认证的 Runtime、Workspace、User 与执行主体重新授权，目录可见性本身不构成授权。

Agent 只接受 SDK 中准确的定义摘要。所有写文件、打开或输入终端、启动或停止进程的操作都被强制标记为需确认；只有运行已冻结的准确写范围，或持久交互记录中同时匹配用户、确认 ID、工具定义摘要和参数摘要的批准回执，才能解除确认。Runtime 收到的 scope 和幂等键完全由 Agent 根据当前 run 生成，模型不能指定。已完成结果写入原工具账本；写操作出现超时、无效输出或不可核查重放时保存为 `uncertain`，不会直接重做。

## 执行边界

- 文件 API 通过 `os.Root` 解析相对路径，拒绝绝对路径、遍历、符号链接逃逸、非普通文件、非 UTF-8 和超限文件。读取返回完整文件 SHA-256；写入和编辑必须携带准确版本或新文件的 `missing`，编辑还要求唯一匹配或显式全部替换，临时文件在根内写入后原子更名。
- 当前执行宿主是 Darwin `sandbox-exec`。Shell、后台进程和 LSP 使用清空凭证后的固定环境，禁止网络和工作区外写入；允许读取安装好的系统工具和语言服务。非 Darwin 或沙箱缺失时显式装配失败，不发布一个名义上受限而实际未受限的环境。
- PTY 和后台进程按 Runtime、Workspace、User、Conversation 与 Run 的准确组合隔离。终端可跨模型步骤持续使用，输出保留 2 MiB 有界缓冲并通过游标读取；取消、运行终态或服务关闭会结束本 scope 的进程。
- LSP 命令和扩展映射由部署固定，模型不能传可执行路径。每次查询执行真实 stdio JSON-RPC 初始化、`didOpen`、definition／references 请求，要求 UTF-16 位置语义，只返回工作区内位置并限制 200 项。

## 页面观察

原有任务详情和普通运行详情继续复用 `ExecutionActivity`。新增结构化展示文件内容片段、大小和版本，搜索命中，文件变更前后摘要，终端输入／输出和状态，后台进程 argv、输出和退出码，以及 LSP 位置。显示来自已保存工具结果，不从模型文本推断执行过程。

## 与 DeepSeek Harness 当前源码对照

对照固定到官方 `master` 的 `0d1f50007f9bca3f52b06e1c3074fa14d5fb0720`：

- DSH 文件工具提供 read、read_image、write、edit，可选读取观察策略；搜索由同组 discovery 工具提供。Domainry 当前不含图片读取，采用 UTF-8 字节分页、内置字面搜索和强制 SHA-256 写前版本检查。
- DSH 终端提供 open、send、read、signal、close、list，包含前台就绪判断和通用后台 job。Domainry 当前提供持久 PTY 与独立后台进程工具，采用轮询游标输出，没有 signal／list 和前台就绪协议。
- DSH LSP 提供 definition、references、implementation、hover，并按语言与工作区复用 server。Domainry 当前只提供验收要求中的 definition／references，每次查询启动一个受限语言服务进程。
- Domainry 额外把编码能力接入现有 Identity 动作权限、逐项写确认、持久执行回执、运行 scope 清理和 Artifact owner 边界。

官方依据：[DSH 文件工具](https://github.com/deepseek-ai/deepseek-harness/blob/0d1f50007f9bca3f52b06e1c3074fa14d5fb0720/packages/fs/tool-fs/README.md)、[终端工具](https://github.com/deepseek-ai/deepseek-harness/blob/0d1f50007f9bca3f52b06e1c3074fa14d5fb0720/packages/terminal/tool-terminal/README.md)、[Shell 宿主](https://github.com/deepseek-ai/deepseek-harness/blob/0d1f50007f9bca3f52b06e1c3074fa14d5fb0720/packages/terminal/terminal-bash/README.md)、[LSP 工具](https://github.com/deepseek-ai/deepseek-harness/blob/0d1f50007f9bca3f52b06e1c3074fa14d5fb0720/packages/lsp/tool-lsp/README.md)、[stdio LSP](https://github.com/deepseek-ai/deepseek-harness/blob/0d1f50007f9bca3f52b06e1c3074fa14d5fb0720/packages/lsp/lsp-stdio/README.md)。

## 验收结果

- Agent SDK 全包通过，12 个定义、读写效果、Action key 与恢复语义已固定。
- Agent 生产包集合通过；编码目录、当前授权、写确认、定义防伪、准确 scope、清理和 Runtime 路由测试通过。全量扫描同时修复了 K07 外部 Agent HTTP 路由遗漏的能力分类与旧 descriptor 断言。
- Runtime 的编码执行、公开 host、启动装配和 transport 包通过；真实测试覆盖版本冲突、编辑、搜索、路径逃逸、持久 PTY、后台进程、工作区外写入拒绝、definition／references 和 scope 清理。
- Agent 与 Runtime 的 K08 核心测试通过 race detector；前端 94 项测试和生产构建通过。
- 三个仓库 `git diff --check` 无错误；证据清单与源码 SHA-256 可复核。

原始命令、行为摘要、源码哈希和证据文件哈希见 [`docs/evidence/2026-09-16-k08-coding-environment`](evidence/2026-09-16-k08-coding-environment)。
