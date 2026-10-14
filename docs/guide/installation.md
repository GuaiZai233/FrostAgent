# 安装与构建

## 工具要求

| 工具        | 要求与用途                                                         |
| ----------- | ------------------------------------------------------------------ |
| Go          | `go.mod` 要求 ≥ 1.27.0；编译服务端与协议工具                       |
| Node.js     | 使用 22 或更高版本；构建控制台和文档站                             |
| pnpm        | 根 `package.json` 固定为 10.33.0；使用仓库的 `pnpm-lock.yaml`      |
| Buf         | 编译 Protobuf；`go install github.com/bufbuild/buf/cmd/buf@latest` |
| Make + Bash | 使用根 `Makefile` 时需要；Make 指定 `SHELL := /bin/bash`           |

将 Go 的工具安装目录加入 `PATH`，确认 `go version`、`node --version`、`pnpm --version` 与 `buf --version` 可用。

## Make 构建

```bash
pnpm install --frozen-lockfile
go install github.com/bufbuild/buf/cmd/buf@latest
make build
```

`make build` 会生成前端协议代码、编译控制台、安装固定版本 Go 协议插件、生成 Go 协议代码，再构建服务端。

| 命令                            | 作用                              |
| ------------------------------- | --------------------------------- |
| `make build` / `make build-api` | 构建控制台、Go 协议和两个 Go 程序 |
| `make build-web`                | 生成 TypeScript 协议并构建控制台  |
| `make proto-generate`           | 生成 Go 和 TypeScript 协议代码    |
| `make serve-api`                | 构建依赖产物后通过 Go 启动服务    |

## Windows PowerShell 构建

如果没有 Bash / Make，可以直接运行各项工具。以下流程与现有 Windows CI 的构建方式一致，并额外显式生成 TypeScript 协议：

```powershell
pnpm install --frozen-lockfile
go install github.com/bufbuild/buf/cmd/buf@latest
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.20.0
pnpm exec buf generate --template buf.gen.web.yaml
pnpm --filter @frostagent/web build
buf generate --template buf.gen.yaml
New-Item -ItemType Directory -Force bin
go build -o bin/app.exe ./cmd/app
go build -o bin/agent.exe ./cmd/agent
.\bin\app.exe
```

Buf 的本地插件通过 `PATH` 查找；Go 安装工具后要确保终端能够找到它们。

## 构建输出

| 路径                           | 内容                                      |
| ------------------------------ | ----------------------------------------- |
| `bin/app` 或 `bin/app.exe`     | 主服务，包含嵌入的控制台静态资源          |
| `bin/agent` 或 `bin/agent.exe` | 旧版 JSON 记忆文件管理工具                |
| `internal/frontend/dist/`      | 控制台静态资源，Go 编译时嵌入             |
| `gen/`                         | 生成的 Go 协议代码                        |
| `docs/.vitepress/dist/`        | 执行 `pnpm docs:build` 后得到的独立文档站 |

服务部署至少需要主程序、稳定的工作目录和数据存储。保留 `eval/dialogue/dialogue.yml` 可让新实例按默认模板初始化示例对话。文档站不嵌入服务主程序。

## 安装后检查

启动主程序，打开控制台，创建实例并完成[快速开始](./quick-start)。如果 Go 编译报缺少前端资源或 `gen/proto` 包，先构建控制台和生成 Go 协议；不要用空目录代替实际前端构建。
