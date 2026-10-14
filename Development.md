# 开发指南

完整说明已整理至 [VitePress 开发指南](docs/development/index.md)，文档站维护见 [维护文档站](docs/development/docs.md)。

## 环境

准备 Go ≥ 1.27.0、Node.js 22+、pnpm 10.33.0、Buf、Make 和 Bash。控制台采用 Vite + 原生 TypeScript；Go 协议输出和嵌入前端资源需先生成。

```bash
pnpm install --frozen-lockfile
go install github.com/bufbuild/buf/cmd/buf@latest
make proto-tools
make proto-generate
make dev
```

Vite 控制台端口为 4200，后端默认 8080。Vite 提供前端热更新；Go 源码修改后需重启后端。

## 构建与验证

```bash
make build
pnpm --filter @frostagent/web test
pnpm --filter @frostagent/web check
pnpm exec eslint .
make test-api
make vet
```

`make ci` 执行仓库 Makefile 检查，实际前端回归测试单独由 `pnpm --filter @frostagent/web test` 执行。

SQL Schema 修改遵守[不可变 v7 基线、追加事务迁移与双数据库验证规则](docs/development/index.md#sql-schema-migration)。

## 文档

```bash
pnpm docs:dev
pnpm docs:check
pnpm docs:build
pnpm docs:preview
```

文档静态输出位于 `docs/.vitepress/dist/`，支持通过 `DOCS_BASE` 指定部署子路径。
