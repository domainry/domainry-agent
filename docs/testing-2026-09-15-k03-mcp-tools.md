# K03 MCP 工具接入验收

日期：2026-09-15。本轮把现有 MCP Connector 接入 Agent 的受控工具执行链，保持连接、工具声明、执行授权和持久回执的 owner 边界。K03 验收完成后，完整清单进度为 **18／25**。

## Owner 与固定契约

- Connector SDK 新增 `mcp_tool/mcp` 的 `test_connection`、`list_tools`、`call_tool` 固定操作契约和 SHA-256；Connectors 负责 HTTP／stdio 会话、初始化、分页、allowlist、超时与协议调用，Integration 继续负责连接账户、凭证、生命周期和外部写入回执。
- Tools SDK 只发布 `mcp_accounts`、`mcp_list_tools` 和 `mcp_call_tool` 三项固定 Agent 工具。远端 MCP 工具不会动态变成系统工具，也不能声明 Domainry Action、改变 effect、取消确认或扩大用户已选择的工具范围。
- Integration 的 Action manifest 显式包含非 HTTP 的 `integration.connection_accounts.write`；Runtime 用当前 Identity 主体解析连接账户范围，并且只在账户列表、读取、写入和可信主体解析四项端口完整时装配 MCP 工具。

## 目录信任与执行边界

- `mcp_list_tools` 在 Connector 内最多读取 8 页、64 个 allowlist 工具。Tools 丢弃远端 `title`、`description`、`$comment`、`examples`、`default` 等注释，只向模型返回工具名和经过清理、重新编译的结构 Schema，并标记为不可信目录数据；外部 `$ref`、过深或过大的 Schema 会被拒绝。
- `mcp_call_tool` 始终要求 Agent 的精确调用确认。确认后、外部写入前再次核对当前工具授权、当前连接账户 revision、实时完整目录、allowlist 中的准确工具和参数 Schema；远端目录内容不能绕过本地工具选择、权限或确认。
- 实际调用使用 Integration 的稳定请求身份和持久写入回执。Agent 仍保存同一执行账本，成功结果使用 `accepted` 完成标记；超时或未知结果进入 receipt-only reconcile，重启和恢复不会再次调用远端工具。
- 已保存结果每次读取都会重新核对当前 Agent 工具授权和当前连接账户／来源。关闭 `mcp_call_tool` 或撤销连接访问后，原调用仍留在账本中，但其业务结果不再进入模型或页面结论。

## 验收结果

- 真实产品链使用实际 Identity、Agent、Tools、Integration、MCP Connector 和 SQLite，仅模型及 MCP 服务为协议夹具。场景完成账户发现、目录读取、参数调用、用户确认、回执保存、存储重开和撤权复核；恶意远端描述 `OVERRIDE_SYSTEM` 未进入模型上下文，确认前外部效果为 0，确认后为 1，重启后仍为 1。
- Connector SDK、Connectors、Integration SDK、Integration、Tools SDK、Tools 和 Agent SDK 全仓测试通过。Agent 实际源码目录除 Web 包外的首轮回归全部通过；Web 包首轮累计超过默认 10 分钟，20 分钟复验又使一个旧平权协作场景在整包资源竞争下超过自身约 100 秒等待上限。该旧场景单独复验 75.61 秒通过，K03 真实链重复 6.80 秒通过，没有 K03 断言失败。
- Runtime 的 Agent host 与 transport 包通过，包含“只有完整 Integration 端口才发布 MCP 工具”的组合测试。`runtime/bootstrap/runtime` 的本地工作区测试仍会命中既有 data_exchange／report 版本锁和本地未发布 Agent／report capability digest 差异；未为 K03 改写发布锁。
- 九个涉及仓库的 `git diff --check` 全部通过。

完整命令与日志见[验收证据](evidence/2026-09-15-k03-mcp-tools/commands.md)。
