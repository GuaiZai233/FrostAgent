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

## 国际化

```bash
make extract-i18n
```

## 清理

```bash
make clean        # 清除所有构建产物
```
