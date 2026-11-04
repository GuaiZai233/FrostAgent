# Docker 部署与更新

## 默认入口与首次初始化

标准镜像默认监听容器内 `0.0.0.0:8080`，`compose.yaml` 直接发布
`127.0.0.1:8080:8080`。宿主机使用 http://127.0.0.1:8080；Windows Docker Desktop
使用 Linux 容器。此默认路径不需要 `compose.local.yaml` 或 socat。

端口映射后的请求通常来自网桥地址，因此管理 API 必须携带控制令牌。
本地 Host、私有来源 IP、Cloudflare/代理请求头都不能替代令牌认证。
Host/Origin 检查仍然生效，适配器端口 `11023` 仍不提供管理路由。

1. 复制 `docker.env.example` 为部署目录的 `.env`，保留已有环境文件内容。
2. 将 `FROSTAGENT_BOOTSTRAP_CONTROL_TOKEN` 设置为随机令牌：32–4096 个可打印
   ASCII 字符，无内部空格。可用 `openssl rand -hex 32` 生成并妥善保存。
3. 启动：

   ```bash
   docker compose up -d --build
   ```

4. 打开 http://127.0.0.1:8080/#/settings/frontend，在「API / 控制平面访问凭据」
   中填写同一令牌并保存，刷新页面。此页面无需先创建或选择实例。

如果 SQL 中已有非空 `MCP_CONTROL_TOKEN`，或作为回退的 `ADMIN_TOKEN`，初始化输入
不会覆盖它，也无需继续提供。网页应填写 SQL 中实际生效的令牌。
没有现有令牌且初始化输入缺失或不合法时，镜像在绑定端口前退出并提示变量名，
不会生成默认密码或在日志中输出令牌。

初始化输入仅在进程启动或切换到无令牌的目标数据库时用于一次 SQL 写入，
不是应用设置的环境变量覆盖机制，
也不用于轮换密码。首次成功后，从部署环境中移除它，再重建容器以移除容器环境中的副本。
以后在已认证的服务端设置中轮换令牌，并同步更新网页凭据。

切换数据库时，目标库使用它自己的现有令牌；切换后网页凭据也须匹配目标库。
目标库没有令牌时，需要容器仍提供上述初始化输入，否则切换会失败并恢复原库。
切换前可重新设置该变量并重建容器，完成初始化后再移除；不会复制或覆盖其他库的令牌。

## 直接使用镜像

先在当前终端或环境文件中设置上述初始化变量，再运行：

```bash
docker run -d --name frostagent --restart unless-stopped \
  -p 127.0.0.1:8080:8080 -p 127.0.0.1:11023:11023 \
  -e FROSTAGENT_BOOTSTRAP_CONTROL_TOKEN \
  -v frostagent-data:/app/data ghcr.io/guaizai233/frostagent:latest
```

这是 Bash 换行写法；PowerShell 可写成一行。`EXPOSE` 只声明容器端口，
`docker run` 仍须提供 `-p` 才会发布到宿主机。端口、数据卷及已有自定义参数应沿用原部署。

## 更新

先备份整个持久化数据；SQLite 应停机复制完整数据卷，PostgreSQL 另做数据库备份。
保留原部署目录、Compose 项目名（`-p` / `COMPOSE_PROJECT_NAME`）、环境文件和数据卷。
以下 Compose 命令都应继续携带原来必要的 `-f` / `--env-file` 参数。

仓库默认的源码构建部署：

```bash
git pull --ff-only
docker compose up -d --build
```

使用自己配置的 GHCR `image:`，且没有本地构建的部署：

```bash
docker compose pull frostagent
docker compose up -d --no-build frostagent
```

固定版本或 digest 时先改为目标引用。若有共享 FrostAgent 网络命名空间的
`cloudflared`，一并重建它：

```bash
docker compose -f compose.yaml -f compose.cloudflare.yaml up -d --build --force-recreate frostagent cloudflared
```

镜像部署将 `--build` 换成 `--no-build`。`docker run` 部署则拉取目标镜像、停止并删除
旧容器，再沿用原数据卷和参数重新创建。不要使用删除数据卷的选项。

```bash
docker compose ps
docker compose logs --tail 100 frostagent
```

不要用 `docker compose down -v` 更新，它会删除应用数据卷。数据库升级后，
回退旧镜像前须确认兼容性，不能手工调低 schema 版本。

## 旧部署迁移到直接映射

已有 SQL `LISTEN_ADDR` 始终优先，不会自动改成 `0.0.0.0:8080`。
如果保存的是 `127.0.0.1:8080` / 其他回环端口，先通过原入口登录，在全局设置
中将管理监听改为 `0.0.0.0:8080`，再使用默认直连入口。SQL 中未保存该键时直接使用新默认值。
旧镜像若拒绝通配地址，先升级镜像，借助原 socat 或共享命名空间的回环入口完成修改。
在原入口访问旧库时先设置管理令牌；无 SQL 令牌的旧 Cloudflare/回环部署升级时也需
提供一次初始化变量。新镜像仍支持保存回环地址，但普通 bridge 端口映射无法访问它。

**旧 socat 入口不会自动获得直连入口的网桥令牌保护。** 它会把请求转换成真实回环连接，
仍属于此前的可信本机模式。若采用网桥必须认证的直连部署，迁移成功后必须停止并删除
两个旧 relay，不能只从下一条命令中省略覆盖文件：

```bash
docker compose -f compose.yaml -f compose.local.yaml stop local-panel local-panel-origin
docker compose -f compose.yaml -f compose.local.yaml rm -f local-panel local-panel-origin
```

保留应用数据卷。随后不再叠加 `compose.local.yaml`。旧配置仅用于显式保留的遗留回环
部署，其既有信任边界见 [legacy socat 入口](docker-local-panel.md)。

## Cloudflare 与 host 网络

容器 `cloudflared` 可继续通过 `compose.cloudflare.yaml` 共享 FrostAgent 网络命名空间，
回源至 `http://127.0.0.1:8080`。该真实回环连接沿用原 Cloudflare Access 策略；
整个公网主机名仍须由 Access 保护，并在 SQL `HTTP_ALLOWED_ORIGINS` 中允许对应来源。
其他网桥容器即使伪造 Cloudflare 头也不能获得回环信任。
已显式开启 `MCP_ENFORCE_LOCAL_TOKEN` 时，回环 connector 也必须提供正确令牌。

Linux host 网络使用单独的 `compose.host.yaml`，构建时将管理和适配器默认地址都设为
宿主回环，并保留强制管理回环校验；不要把标准 bridge 镜像直接用于 `--network host`。
SQL 配置优先，切换模式前应检查已有监听地址。Docker Desktop 的 host 转发与 Linux
原生 host 网络不同，本地验证默认使用 bridge 发布端口。
