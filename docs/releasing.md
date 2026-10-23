# 源码安装与发布

## 分发和版本范围

维护者已选择 1.0.0 先仅发布源码。GitHub Release 提供 tag 对应的 Source code 归档；不上传预编译程序、不发布注册表镜像，也不自动打 tag。Docker 部署从同一 tag 的源码构建。

项目版本由根目录 `package.json` 表示，`apps/web/package.json` 和 `libs/frostagent-proto/package.json` 同步更新。这三个包均为私有 workspace 包，不发布到 npm。AstrBot 插件的 `metadata.yaml` 保留独立版本，不随项目版本机械修改。

当前准备版本为 `1.0.0`，发布说明源稿见 [v1.0.0.md](releases/v1.0.0.md)。仓库出现该版本号或说明文件不等于已经发布，实际状态以 [GitHub Releases](https://github.com/GuaiZai233/FrostAgent/releases) 为准。

## 获取固定版本

以下命令仅在维护者正式发布 `v1.0.0` 后使用。在新目录获取源码，避免覆盖现有配置或运行数据：

```sh
git clone --branch v1.0.0 --depth 1 https://github.com/GuaiZai233/FrostAgent.git FrostAgent-v1.0.0
cd FrostAgent-v1.0.0
```

也可解压 Release 提供的源码 ZIP / tar.gz。归档不包含 Node 依赖、Go 模块缓存或本地运行数据。构建时需要访问依赖源，按锁文件安装；不要为了完成安装删除锁文件或运行全量依赖升级。

## 构建和首次启动

准备 Go ≥ 1.27.3、Node.js 22 和 pnpm 10.33.0。将 Go 的安装工具目录加入 PATH；以下 buf 版本与当前 Dockerfile 一致。

### Linux / Bash

另需 GNU Make：

```sh
pnpm install --frozen-lockfile
go install github.com/bufbuild/buf/cmd/buf@v1.46.0
make build
./bin/app
```

### Windows / PowerShell

无需 Make，逐条执行以下命令，每条成功后再继续：

```powershell
pnpm install --frozen-lockfile
go install github.com/bufbuild/buf/cmd/buf@v1.46.0
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.20.0
pnpm exec buf generate --template buf.gen.web.yaml
buf generate --template buf.gen.yaml
pnpm --filter @frostagent/web build
go build -o bin/frostagent.exe ./cmd/app
.\bin\frostagent.exe
```

从源码根目录启动，使默认 `data/` 路径稳定。首次启动创建 SQLite 数据库且不创建实例。访问 `http://localhost:8080`，创建实例，配置供应商与模型路由，再启用实例和 IM 连接。Windows Credential Manager 凭据依赖运行 FrostAgent 的 Windows 用户。

Docker 用户在此固定版本目录内构建，按 [README](../README_zh_CN.md#docker-部署)选择桥接或原生 Linux host 拓扑。不要假设存在 `ghcr.io` 镜像；桥接部署的管理监听位于容器回环，宿主机浏览器不能直接访问。

## 升级、备份与回退

1. 记录当前源码提交、部署目录、服务运行用户、Compose 项目名和数据库选择。导出实例 ZIP 用于实例搬迁；它不含密钥及凭据来源，不能替代完整恢复备份。
2. 停止所有使用目标数据库的 FrostAgent 进程。备份整个 `data/` 或 Docker 应用卷；若使用 PostgreSQL，同时备份外部数据库。保留环境变量、Secret 文件和 Windows 凭据等外部来源，妥善保护备份。
3. 在新目录构建目标版本，再替换程序。沿用原来的工作目录 / 数据卷和运行身份；不要让新旧进程同时使用同一数据库。Docker 更新沿用原 Compose 项目名，始终携带所选拓扑文件；共享网络命名空间的 cloudflared 也需同步重建。不要使用 `docker compose down -v`。
4. 启动并检查迁移日志、实例列表、模型路由、管理鉴权、适配器连接和一次实际对话，再验证备份下载。数据库切换不会迁移数据，ZIP 还原的实例默认停用且需要补填凭据。
5. 若需回退，先停止新版程序，保存故障现场，再将升级前的数据快照与旧程序一起恢复。Schema 不支持自动降级，不能只换旧二进制继续使用已升级的数据库。

1.0.0 使用 Schema v14，支持从 v7–v13 顺序升级，拒绝更旧或未来版本。旧 JSON / `.env` 运行数据不自动迁移；需要保留原部署并制定人工迁移方案。详细兼容边界见 [Release Notes](releases/v1.0.0.md)。

## 维护者发布步骤

以下是人工发布流程，本文件中的命令不会被 CI 自动执行。所有 `gh` 命令在本机 PowerShell 运行。

发布前必须完成：

- 合并依赖安全修复、凭据日志修复和源码发布准备三个 PR，完成维护者 review；未合并的修复不属于 main 的发布内容。
- 对选定 main 提交核对 CI、Windows Smoke、PostgreSQL、Docker、CodeQL 的完整结果，并重新核对安全告警。不得把“扫描运行成功”当成“没有告警”。
- 在自己的部署环境完成全新安装、升级与完整备份回退验收，以及真实 IM / 模型、Cloudflare Access 等所用集成验证；记录尚未验证或由维护者明确接受的限制。
- 核对三个 workspace 版本都为 `1.0.0`，审阅发布说明和升级边界，确认远端不存在同名 tag / Release。

使用新的干净发布目录，固定候选提交；任何检查失败或尚未完成时停止，不进入打 tag 步骤：

```powershell
git clone https://github.com/GuaiZai233/FrostAgent.git FrostAgent-release
Set-Location FrostAgent-release
git switch --detach origin/main
$releaseCommit = git rev-parse HEAD
gh run list --repo GuaiZai233/FrostAgent --commit $releaseCommit --limit 30 --json name,headSha,status,conclusion,url
git ls-remote --tags origin refs/tags/v1.0.0
gh release list --repo GuaiZai233/FrostAgent --limit 30
```

维护者确认上述提交可发布后，才执行：

```powershell
git tag -a v1.0.0 $releaseCommit -m "FrostAgent v1.0.0"
git push origin refs/tags/v1.0.0
gh release create v1.0.0 --repo GuaiZai233/FrostAgent --verify-tag --draft --title "FrostAgent v1.0.0" --notes-file docs/releases/v1.0.0.md
gh release view v1.0.0 --repo GuaiZai233/FrostAgent --json tagName,isDraft,body,url
```

`--verify-tag` 防止 `gh` 隐式从最新 main 创建不同的 tag。这个流程只创建 Draft Release；草稿不等于正式发布，但推送的 tag 已公开可见。确认 tag 指向已验收提交、说明内容准确、源码归档可用后，由维护者执行：

```powershell
gh release edit v1.0.0 --repo GuaiZai233/FrostAgent --draft=false --latest
gh release view v1.0.0 --repo GuaiZai233/FrostAgent --json tagName,isDraft,isPrerelease,publishedAt,url
```

若同名 tag / Release 已存在，先检查其提交和发布状态，不强推、不删除后重建。后续修复通过新版本发布。
