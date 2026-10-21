# Docker 部署

容器部署的远程管理认证由前置 **Cloudflare Access** 负责。镜像不包含额外的反向代理、管理员账号系统或本地 CA。Tunnel 的运行位置由部署用户选择；Tunnel 连通本身不代表已经配置 Access。

以下命令在已克隆的仓库根目录执行，需要可用的 Docker Engine 与 Compose 插件。示例通过仓库 `Dockerfile` 本地构建镜像，应用设置仍由 SQL 管理。

| 场景                                     | 使用文件                                      | 管理入口                                         |
| ---------------------------------------- | --------------------------------------------- | ------------------------------------------------ |
| 原生 Linux 本地测试 / 宿主机 cloudflared | 独立 `compose.host.yaml`                      | 宿主机回环 `127.0.0.1:8080`                      |
| 容器内 cloudflared                       | `compose.yaml` 叠加 `compose.cloudflare.yaml` | connector 与应用共享网络命名空间，回源容器内回环 |
| 只运行默认桥接文件                       | `compose.yaml`                                | 不发布管理端口，不能直接从宿主浏览器打开         |

## 本地测试或宿主机 cloudflared（原生 Linux）

单独使用 host 网络文件，不与桥接文件叠加：

```bash
docker compose -f compose.host.yaml up -d --build
```

新数据库默认可在 `http://127.0.0.1:8080` 打开控制台；适配器为 `ws://127.0.0.1:1234/instances/<实例ID>/ws/onebot` 或 `/ws/astrbot`。该镜像将管理与适配器默认地址都设为回环；host 模式没有端口映射，隔离来自应用实际监听的 `127.0.0.1`。本地测试无需配置 Tunnel。

若 cloudflared 已运行在同一 Linux 宿主机，它直接回源到 `http://127.0.0.1:8080`。生产路径为：`浏览器 → Cloudflare Access → Tunnel → 宿主机 cloudflared → 回环管理监听`。先通过本地控制台把完整的公开地址（例如 `https://frostagent.example.com`）加入 `HTTP_ALLOWED_ORIGINS`，再启用公网路由。

此回环隔离方案针对原生 Linux Engine。Docker Desktop 的 host networking 需用户主动启用，且是不同的四层转发实现，不能据此保证 Windows/macOS 外部接口的暴露范围；仓库中的容器验证不覆盖 Desktop 宿主机转发。Windows/macOS 本地控制台可按[安装与构建](./installation)使用原生程序。参见 [Docker host 网络说明](https://docs.docker.com/engine/network/drivers/host/)。

## 容器内 cloudflared（桥接网络）

默认 `compose.yaml` 只启动 FrostAgent：

```bash
docker compose up -d --build
```

管理只监听**容器内** `127.0.0.1:8080`，不发布管理端口，因此宿主机浏览器不能直接访问。适配器监听容器内 `0.0.0.0:1234`，仅映射到宿主机 `127.0.0.1:1234`；同一 Docker 网络的适配器可用 `ws://frostagent:1234/instances/<实例ID>/ws/onebot` 或 `/ws/astrbot`。1234 只提供适配器 WebSocket，管理页面、API 和 RPC 均不可通过此端口使用。

cloudflared 必须与 FrostAgent **共享网络命名空间**，而不只是加入同一桥接网络。若希望由此 Compose 运行已有的 Tunnel connector，将 `docker.env.example` 中的 `TUNNEL_TOKEN` 填入 `.env`，并使用可选文件：

```bash
docker compose -f compose.yaml -f compose.cloudflare.yaml up -d --build
```

它只增加用户选择的 cloudflared，没有额外反向代理或认证组件。已在其他部署中运行的 connector 不必重复启动；可为其设置 `network_mode: container:<FrostAgent容器ID>`。FrostAgent 被重新创建后，connector 也须重新创建以加入新的命名空间；同一 Compose 中请始终带上两个文件执行更新，可选文件声明了此重建依赖。

在 Cloudflare 配置回源地址 `http://127.0.0.1:8080`，并在发布路由前为**整个管理域名的全部路径**配置 Access 应用与允许策略，不留管理 API/RPC 的 Bypass 路径。生产路径为：`浏览器 → Cloudflare Access → Tunnel → cloudflared → 同命名空间回环管理监听`。普通桥接网络容器无法连接该回环监听，伪造 Host 或 Cloudflare 请求头不能改变网络可达性。

首次配置公开 Host/Origin 可先使用上述 Linux 本地控制台，或者执行以下一次性管理请求（Bash 示例；替换域名，现有自定义管理端口需同步修改 `base`）。辅助 Python 容器仅用于本次配置，不是运行组件：

若已启用 `MCP_ENFORCE_LOCAL_TOKEN`，先在当前 Bash 会话输入现有的管理控制令牌（不回显）；默认免令牌部署跳过这一步：

```bash
read -r -s -p 'Existing management control token: ' FROSTAGENT_BOOTSTRAP_TOKEN
printf '\n'
export FROSTAGENT_BOOTSTRAP_TOKEN
```

```bash
docker run --rm -i --env FROSTAGENT_BOOTSTRAP_TOKEN --network container:$(docker compose ps -q frostagent) python:3.13-alpine python - https://frostagent.example.com <<'PY'
import json, os, sys, urllib.request as http
base = "http://127.0.0.1:8080/api/instances/global/"
headers = {"Content-Type": "application/json"}
token = os.environ.get("FROSTAGENT_BOOTSTRAP_TOKEN", "").strip()
if token:
    headers["Authorization"] = "Bearer " + token
request = http.Request(base + "backup/settings", headers=headers)
with http.urlopen(request, timeout=10) as response:
    config = json.load(response)
config.pop("extra_env", None)
origins = set(filter(None, config["settings"].get("HTTP_ALLOWED_ORIGINS", "").split(",")))
config["settings"]["HTTP_ALLOWED_ORIGINS"] = ",".join(sorted(origins | {sys.argv[1]}))
request = http.Request(base + "import/settings", json.dumps(config).encode(), headers)
with http.urlopen(request, timeout=10) as response:
    print(response.read().decode())
PY
```

若设置了临时令牌变量，完成后执行 `unset FROSTAGENT_BOOTSTRAP_TOKEN`。

只设置宿主机 `127.0.0.1` 发布地址不能隔离容器网络；反过来，将端口发布到宿主机也不能让容器内回环监听变得可达。不要把管理监听改成 `0.0.0.0` 来修复 Tunnel 连接。宿主机 cloudflared 使用 host 方案；独立网络的容器 cloudflared 使用共享命名空间方案。

## 配置、升级和验证边界

SQL 设置仍优先；启动、热更新和数据库切换必须保持管理地址为数值回环 IP，且与适配器不同端口。不安全的存量地址拒绝启动，不自动迁移。早期容器部署已保存的 `127.0.0.1:8081` 仍有效，需让 Tunnel 回源到该实际端口；新库默认管理端口改回 8080。host 镜像的回环默认值不会覆盖 SQL 中已保存的 `WS_LISTEN_ADDR=0.0.0.0:1234`；从桥接部署切换到 host 前，须按需要先改为 `127.0.0.1:1234`，避免适配器监听宿主机所有接口。

容器构建仅信任实际来自回环的 connector 连接，保留公共 Host/Origin 和转发头；这份信任来自网络隔离，不是对 Cloudflare 请求头的认证。通过 Access 的正常请求默认无需另一套本地登录。既有 `MCP_ENFORCE_LOCAL_TOKEN` 仍有效；若用户显式启用了它，前端及一次性管理请求仍需原有 Bearer Token。普通构建的远程控制令牌规则保持不变。

镜像使用 UID/GID `10001:10001`，系统 CA 信任包用于出站 TLS，不生成部署 CA。`frostagent-data` 保留 `/app/data` 中的数据库、引导选择和贴图文件。`docker compose down` 保留卷；只有明确要删除数据时才使用 `down -v`。升级前备份应用卷与外部 PostgreSQL；旧 `frostagent-gateway-data` 证书卷不再使用，不自动删除。从带 gateway 的旧版部署升级时先用旧部署文件执行 `down`（不带 `-v`），停用旧 gateway，再选择新拓扑启动，保留相同 Compose 项目名以复用数据卷。

应用配置仍来自 SQL，`.env` 中的 `TUNNEL_TOKEN` 只供可选 connector 使用。容器内 `localhost` 指向当前网络命名空间；外部数据库、模型与沙箱使用可达地址。镜像不包含 Node.js/Python 等 stdio MCP 运行时，命令执行仍交给外部沙箱。

CI 构建桥接及 host 镜像，使用临时数据验证回源路径、公共 Host/Origin、管理隔离、端口暴露、OneBot/AstrBot WebSocket、SQL 监听约束、非 root、退出与持久化。测试使用本地合成回源客户端和只运行 `version` 的真实 cloudflared 镜像，不建立公网 Tunnel；Cloudflare Access 云端登录/策略、真实 IM 接入及 Desktop 宿主机转发不属于已验证范围。
