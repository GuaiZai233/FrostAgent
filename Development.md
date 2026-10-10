# 开发指南

## 环境准备

需安装 Go ≥ 1.25.3、pnpm、buf、Make，并确保在 PATH 中。

```bash
pnpm install       # 前端依赖 + protoc-gen-es
make proto-tools   # Go proto 插件 (protoc-gen-go, protoc-gen-connect-go)
```

## 代码生成

修改 `.proto` 后需重新生成：

```bash
make proto-generate        # Go + TypeScript 一起生成
make proto-generate-go     # 仅 Go
make proto-generate-web    # 仅 TypeScript
```

## 开发

```bash
make dev          # 全栈热重载 (Go + Angular)
make serve-web    # 仅前端 (端口 4200)
make serve-api    # 仅后端
```

## 构建

```bash
make build        # 生产构建 → ./bin/app, ./bin/agent
make build-web    # 仅构建前端
```

## 质量

```bash
make lint         # ESLint
make test-api     # Go 测试
make vet          # Go 静态分析
make ci           # 完整检查 (build + test + lint + vet)
```

## SQL Schema Migration

- `internal/storage/schema.go` 中的 v7 建表语句是不可变基线。今后每次改变数据库结构或持久化格式，都在 `migrations` 末尾追加连续编号的步骤，并更新 `SchemaVersion`；不得修改已发布的步骤来升级旧库。
- 每一步的结构与数据转换、`schema_meta` 版本推进必须在同一事务提交。程序只执行缺失步骤，迁移失败则停止 Runtime 启动；未来版本和 v7 以前的 SQL Schema 原样保留并拒绝加载。管理员放弃旧库时，应显式创建另一个数据库，不能通过启动时自动删表实现。
- 每个 Schema PR 都要验证已填充的上一版 SQLite 和真实 PostgreSQL 数据库：实例与密钥等业务数据保留、跨版本顺序升级、重复启动幂等、失败回滚后可重试、未来版本拒绝且不改库。CI 的 `PostgreSQL Migrations` job 提供一次性 PostgreSQL 服务，测试不能仅因缺少 DSN 而跳过。

## 清理

```bash
make clean        # 清除所有构建产物
```
