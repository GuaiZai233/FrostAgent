# 构建版本信息

FrostAgent 在编译时确定版本身份，运行时不会访问 Git 或把镜像标签当作版本。
`internal/buildinfo.DisplayVersion()` 同时供实例引擎和管理状态接口使用。

| 构建方式 | 显示值 |
| --- | --- |
| CI 从有效版本 tag（如 `v1.2.3`、`v1.2.3-rc.1`）构建 | 去掉开头 `v` 的版本 |
| main／PR 预构建，或本地源码构建 | 构建提交的前七位 hash |
| 没有注入信息，也没有 Go VCS 元数据 | `unknown` |

本地源码恰好位于版本 tag 也仍显示 hash。显示值不追加 `dirty`；未提交改动
不会产生新的提交标识，因此 hash 不是源码完整性或官方发行身份的证明。
完整提交保存在构建元数据中，只在展示时截为七位。`package.json` 的版本不参与此判断。

## 本地构建

`make build`、`make dev`、`make serve-api` 和 `make serve-agent` 会通过
`git rev-parse HEAD` 注入完整提交。直接 `go build ./cmd/app` 可使用 Go 自动嵌入的
`vcs.revision`；`-buildvcs=false` 或无 Git 源码包不保证有此信息。

如需明确提供构建身份，可使用 Go 的链接参数（以下为示例，不会创建发布标签）：

```sh
go build -ldflags="-X github.com/GuaiZai233/FrostAgent/internal/buildinfo.Version=1.2.3 -X github.com/GuaiZai233/FrostAgent/internal/buildinfo.Commit=<完整提交>" -o bin/app ./cmd/app
```

## Docker / CI

Docker 上下文不包含 `.git`。从源码构建镜像时显式传入提交：

```sh
docker build --build-arg FROSTAGENT_COMMIT="$(git rev-parse HEAD)" -t frostagent:local .
```

版本构建额外传入 `--build-arg FROSTAGENT_VERSION=1.2.3`。普通本地
`docker compose up --build` 没有传入构建身份时显示 `unknown`，不能凭包版本猜测。

GitHub Docker 工作流从已检出的提交和 `GITHUB_REF` 解析元数据；版本 tag 必须
使用 `vMAJOR.MINOR.PATCH`，可带 SemVer 预发布后缀（如 `-rc.1`），不支持 `+build` 元数据。
完整 tag（含开头的 `v`）最长 128 个字符，与 Docker 镜像标签限制一致。
例如 `v1.2.3+build.1` 会被拒绝，而 `v1.2.3-build.1` 是允许的预发布版本。
这是为了避免 Docker 标签规范化把 `+` 替换为 `-`，导致不同版本覆盖同一个镜像标签。
不支持的 tag 会在构建元数据解析阶段失败，不输出版本参数，也不会启动依赖该阶段的镜像发布。
测试镜像和发布镜像使用同一组元数据，容器 smoke test 会验证实际状态接口返回的版本。
`main`／`latest` 保持源码预构建语义；`sha-*` 只由非 tag 构建发布，避免同一提交的
版本构建覆盖预构建镜像。版本 tag 本身由维护者创建，本工作流不创建 Release 或二进制附件。

## 回归验证

```sh
go test ./internal/buildinfo -count=1
python3 -m unittest discover -s scripts -p 'test_build_metadata.py' -v
```

Go 测试会实际编译并执行小型二进制，验证版本、完整提交、七位展示、无 VCS 回退和
本地 VCS 路径。Docker 工作流还会验证真实应用二进制经容器状态接口返回的版本。
