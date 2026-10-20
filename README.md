# Domainry Agent

Domainry Agent 是 Agent 运行与编排模块，不是 Knowledge、Todo 或具体业务产品的聚合宿主。

## 职责边界

Agent 自己负责：

- 对话、执行步骤、流式事件与恢复
- Agent 定义、技能、协作、委派与后台任务
- 用户记忆、执行结果、产物与原始会话附件
- 通用工具目录、授权、调用回执与结果持久化

外部能力由业务宿主通过 `ConversationOptions.ToolHost` 和
`ConversationOptions.ToolDefinitions` 注入。Knowledge、Todo、报表、邮件、日历等模块
各自持有服务、数据表、权限和 HTTP 接口；Agent 不加载它们的实现，也不为它们提供专用适配层。

## 目录

```text
cmd/domainry-agent/   独立进程入口
internal/             Agent 内部应用、共享组合、部署装配、基础设施、传输和测试
module/               进程内 Module 门面
docs/                 设计与验证资料
```

根目录只保留可复用公共门面和部署入口。定义、集成测试、测试支撑均位于
`internal/`，不会形成可被其他仓库依赖的公共包。

## 使用方式

- 进程内集成使用 `module.NewFactory`。
- SaaS 远程集成使用 `module.NewSaaSFactory`。
- 独立服务命令入口为 `go run ./cmd/domainry-agent`。

依赖统一使用已发布的 Go module 版本；仓库不依赖 `go.work` 或本地目录替换。
