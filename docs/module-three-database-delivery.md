# 拆分模块三数据库适配

## 实现

- Todo 和 Knowledge 原资料库表沿用 `domainry-orm` 的 SQLite、MySQL、PostgreSQL renderer / profile。宿主提供连接池、事务、来源授权和迁移账本；独立使用不创建 Agent 会话表。
- Knowledge 结构化记录移除 SQLite 专用 DDL、`?` 占位符和裸表名。通过 ORM 生成带 schema 的 SQL，JSON 存为文本，键长度满足 MySQL 联合索引要求；保留 CAS、不可变版本、原子业务回执以及整事务重试。
- `module.NewRecordStore(ctx, backend, hostMigrations, namespace)` 借用宿主后端，以 `knowledge_records` owner 注册迁移。整个产品数据库只有 `_schema_migrations` 一个账本。原 SQLite TEXT/BLOB 表可继续读取、回放旧回执和写入下一版本。
- PM、Work 共用宿主通过 `internal/infrastructure/persistence/{sqlite,mysql,postgres}` 打开数据库。SQLite 使用原文件锁和路径；网络数据库使用固定连接上的启动迁移锁，允许 Identity 递归注册 Metadata、Audit 迁移。MySQL 非事务 DDL 失败时保留 dirty 状态，禁止直接重放。
- PostgreSQL 支持独立 schema，并为所有池连接设置 search_path。MySQL 统一 utf8mb4、二进制排序规则、InnoDB 和返回匹配行数的 UPDATE 语义。Agent 在支持行锁的数据库上用 ORM 的 `FOR UPDATE / SKIP LOCKED` 领取任务。

## SaaS 配置

| 配置 | SQLite | MySQL | PostgreSQL |
| --- | --- | --- | --- |
| `SAAS_DATABASE_DRIVER` | `sqlite`（默认） | `mysql` | `postgres` |
| `SAAS_DB` | 数据库文件路径 | 不使用 | 不使用 |
| `SAAS_DATABASE_DSN` | 留空 | `user:password@tcp(host:3306)/product_db` | `postgres://user:password@host:5432/product_db?sslmode=require` |
| `SAAS_DATABASE_SCHEMA` | 留空 | DSN 已选择数据库，通常留空 | 默认 `public`，可指定专属 schema |
| `SAAS_STORAGE_PATH` | 默认沿用数据库路径 | 必填文件前缀 | 必填文件前缀 |

MySQL 数据库需预先创建为 `utf8mb4 COLLATE utf8mb4_bin`。`SAAS_STORAGE_PATH=data/pm-files` 对应 `.artifacts`、`.attachments`、`.documents` 三个持久目录。默认文件存储仍按单主机运行；切换连接配置不会搬迁旧数据库中的数据。

## 启动链兼容修复

真实 MySQL 验收发现原有依赖中的适配缺口，已在所属模块修复：

- Identity：元数据刷新回执联合键宽度、安装管理员回执索引名长度、非唯一 TEXT 搜索索引、TEXT 默认值。
- Metadata：本地化文本五字段联合键超过 utf8mb4 索引上限。MySQL 使用完整字段长度编码后的生成 SHA-256 唯一键；原字段长度、中文内容与唯一性保留，不截断键值。
- Audit：记录游标索引利用 InnoDB 隐含的主键后缀，减少显式索引宽度；事件值和游标排序不变。
- MySQL 更新未变值时默认返回 0 行，导致同一秒内更新角色资料偶发误判为不存在。宿主启用 `clientFoundRows`；回归直接断言未变值更新仍匹配一行。

Metadata、Audit 从当前使用的 `v0.1.6` 标签检出到相邻目录，保持 detached HEAD，没有创建分支或 worktree。修复通过现有 `go.work` 纳入源码构建，未修改 Go 模块缓存、远程标签或发布版本。SQLite / PostgreSQL 的 Metadata、Audit 迁移语句保持原样；本轮不对已有其他部署执行跨数据库迁移或重写迁移账本。

## 验证

使用隔离的 MySQL 9.5.0、PostgreSQL 18.6 实例，以及实际 modernc SQLite 驱动。没有使用用户现有数据库。

- [真实三库集成记录](verification/module-three-db-integration.txt)：Todo / Knowledge 独立仓库；并发同键保存、CAS 冲突、不可变版本、回执、验证失败回滚、产品与用户隔离、分页；迁移锁排斥和释放、dirty 迁移拒绝。
- PM 需求、Work 会议、Work 办公文档各自跑过三种数据库：真实 Identity 登录、首次改密、权限初始化、HTTP 保存、流式协议模型夹具写入业务记录后创建 Todo、注销及完整宿主重启恢复。
- 完整宿主还验证 255 字符中文键、超过 191 字符的不同后缀、不同 row ID 下自然键重复拒绝，以及未变值 UPDATE 匹配行数。
- [MySQL 稳定性回归](verification/module-three-db-mysql-stability.txt)：三个产品流程各连续五次运行。
- [相关 Go 回归](verification/module-three-db-regression.txt)：Agent、Knowledge、Todo、Tools、PM、Work、Metadata、Audit 及受影响的 Identity 持久化包。前端没有本轮数据库变更，未重复前端测试。

复验时设置 `DOMAINRY_TEST_MYSQL_DSN`、`DOMAINRY_TEST_POSTGRES_DSN`，再运行：

```sh
go test -count=1 -v ./internal/infrastructure/persistence/webhost \
  ../domainry-pm/internal/assembly/saas ../domainry-work/internal/assembly/saas
```

测试账号需要创建 / 删除隔离数据库或 schema 的权限。每个用例使用随机命名空间并自动清理；未配置的网络数据库用例明确 skip。

PM、Work 后端二进制已重新构建，[产物校验值](verification/module-three-db-builds.json) 已记录；隔离测试实例已停止，临时数据库目录已清理。
