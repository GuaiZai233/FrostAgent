# 开发指南

FrostAgent 使用 Go 服务端、ConnectRPC / Protobuf 协议、Vite + 原生 TypeScript 控制台，以及 pnpm 工作区。开发前阅读根目录 `AGENTS.md` 和与任务有关的设计材料。

## 目录

| 路径                                    | 职责                                 |
| --------------------------------------- | ------------------------------------ |
| `cmd/app/`                              | 主服务、管理路由、监听与数据库切换   |
| `cmd/agent/`                            | 旧版 JSON 记忆命令行工具             |
| `internal/instance/`                    | 实例生命周期与运行时装配             |
| `internal/llm/`                         | 模型循环、会话、上下文和群压缩       |
| `internal/memory/`                      | 记忆检索、写入、归属与反思           |
| `internal/adapter/`                     | OneBot / AstrBot 协议与消息转换      |
| `internal/modelrouter/`                 | 共享模型目录、凭据与实例路由         |
| `internal/storage/`、`internal/backup/` | SQL 存储、Schema 和应用存档          |
| `internal/service/`                     | ConnectRPC / REST 服务实现           |
| `internal/mcp/`、`internal/sandbox/`    | 外部工具和沙箱接入                   |
| `apps/web/`                             | 控制台页面、路由、实例状态与样式     |
| `proto/`、`libs/frostagent-proto/`      | 协议定义和 TypeScript 生成代码       |
| `adapters/astrbot_plugin_frostagent/`   | AstrBot 宿主插件                     |
| `.frostagent/whitepaper/`               | 设计材料；历史存储描述需结合源码核对 |
| `docs/`                                 | 独立 VitePress 文档站                |

## 环境与代码生成

按[安装与构建](../guide/installation)准备工具：

```bash
pnpm install --frozen-lockfile
go install github.com/bufbuild/buf/cmd/buf@latest
make proto-tools
make proto-generate
```

修改 `.proto` 后同步生成 Go 和 TypeScript 协议。Go 输出 `gen/` 为构建产物，TypeScript 生成目录按仓库现有跟踪规则处理。

## 开发服务

```bash
make dev
```

该命令生成协议、构建开发前端，再同时启动 Go 服务和 Vite。Vite 前端提供热更新，Go 服务由 `go run` 启动，修改 Go 源码后需重启；该命令不提供 Go 自动热重载。

控制台开发服务器端口为 4200，API 默认 8080。Vite 代理 `/instances/`、`/api/` 和全局 RPC 请求，包括直接对话 WebSocket。

| 命令                                  | 用途                             |
| ------------------------------------- | -------------------------------- |
| `make serve-web`                      | 仅控制台开发服务器               |
| `make serve-api`                      | 构建前端与协议后启动后端         |
| `pnpm --filter @frostagent/web check` | 控制台 TypeScript 检查           |
| `pnpm --filter @frostagent/web test`  | 控制台实际回归测试               |
| `pnpm exec eslint .`                  | ESLint                           |
| `make test-api`                       | 准备前端 / Go 协议后执行 Go 测试 |
| `make vet`                            | 准备依赖后执行 Go vet            |
| `make ci`                             | 仓库 Makefile 定义的完整检查     |
| `pnpm docs:check` / `pnpm docs:build` | 文档配置检查与生产构建           |

`make test-web` 当前执行类型检查和构建；实际前端测试需要单独运行 `pnpm --filter @frostagent/web test`，CI 也单独执行它。不要把编译或类型检查称为测试通过。

## SQL Schema Migration

`internal/storage/schema.go` 的 v7 建表语句是不可变基线。每次修改数据库结构或持久化格式，都在 `migrations` 末尾追加连续编号的步骤，并更新 `SchemaVersion`；不修改已发布步骤来升级旧库。

每一步的结构与数据转换、`schema_meta` 版本推进在同一事务提交。程序只执行缺失步骤，迁移失败则停止运行时启动；高于当前程序版本和低于 v7 的 SQL Schema 原样保留并拒绝加载。需要放弃旧库时显式创建另一个数据库，不在启动时自动删表。

每个 Schema PR 要验证已填充的上一版 SQLite 和真实 PostgreSQL 数据库，覆盖业务数据和密钥保留、顺序跨版本升级、重复启动幂等、失败回滚后重试，以及未来版本拒绝且不改库。CI 的 `PostgreSQL Migrations` job 提供一次性 PostgreSQL 服务，不能仅因缺少 DSN 而跳过真实数据库验证。

## 修改与验证边界

实例页面需要正确绑定当前实例，异步请求和卸载逻辑应避免旧实例结果覆盖新页面。群记忆身份与来源由可信适配器上下文决定，不能通过模型文字推断。持久化、路由、并发与失败路径改动要覆盖相应回归测试。

使用独立分支处理任务，保留无关本地改动，按 `AGENTS.md` 做 Implementation Assessment、验证和独立 Review。测试数据使用合成身份，不硬编码真实 QQ、邮箱或用户 ID。

更新用户可见行为时同步使用文档，文档维护方法见[维护文档站](./docs)。
