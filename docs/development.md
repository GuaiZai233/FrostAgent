# 开发指南

[返回首页](../README_zh_CN.md) · [配置与数据](configuration.md) · [Docker 部署](docker.md)

以下命令均在仓库根目录执行。

## 环境准备

需安装 Go ≥ 1.27.3、Node.js、pnpm、buf、Make 和 Bash，并确保在 PATH 中。

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
make dev          # 启动 Go 后端与 Vite 开发服务器
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

## JSON 群存储兼容与人工恢复

以下仅适用于直接使用 JSON `GroupManager` 的存储目录。当前正常运行实例使用 SQL，本步骤不会把旧 JSON 导入 SQL。

旧的 `groups/<SafeGroupKey>` 目录保留 QQ 兼容路径；此前的 JSON 群列表同样通过 QQ getter 读取它们。目录和文件均未记录可靠的平台归属，不能据此判断历史数据实际来自哪个平台。非 QQ 群使用 `groups/p_<平台 UTF-8 字节的小写十六进制>_<SafeGroupKey>`，不会自动接管旧目录。

如果通过外部记录确认某个旧目录完整属于一个非 QQ 群，可人工恢复：

1. 停止所有访问该 JSON 目录的进程，将整个 `groups` 备份到活动目录之外。
2. 确认平台与群 ID，检查原目录名确实为该群规范化 ID 的 `SafeGroupKey`。平台按 `CanonicalPlatform` 规范化；空值、`qq`、`onebot`、`aiocqhttp` 属于 QQ，无需改路径。
3. 为非 QQ 目录添加平台前缀。例如 Telegram 的 UTF-8 十六进制为 `74656c656772616d`，将整个 `groups/g_...` 移到 `groups/p_74656c656772616d_g_...`，保留目录内所有文件和内容。目标必须不存在；若已存在，停止操作，不覆盖或自动合并。
4. 重启后核对群列表的平台、群资料、成员称呼、记忆及主题目录，并保留备份。来源不能确认或已混写多个平台时，保留备份供人工核查，不把整份数据复制给多个平台。

## 清理

```bash
make clean        # 清除所有构建产物
```
