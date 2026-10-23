# Docker 部署

[返回首页](../README_zh_CN.md) · [English](docker.en.md)

所有命令在仓库根目录执行。先按 cloudflared 的运行位置选择一种方案，不要混用 host 和桥接文件。

## 先选部署方式

| 场景                                             | 使用文件                                         | 管理控制台                              |
| ------------------------------------------------ | ------------------------------------------------ | --------------------------------------- |
| 原生 Linux 本地测试，或 cloudflared 在同一宿主机 | `compose.host.yaml`                              | 宿主机 `http://127.0.0.1:8080`          |
| cloudflared 运行在容器中                         | `compose.yaml`，可叠加 `compose.cloudflare.yaml` | 仅容器网络命名空间内的 `127.0.0.1:8080` |

**公网访问必须先配置 Cloudflare Access，覆盖整个管理域名的全部路径，不留 API/RPC Bypass。Tunnel 连通不等于已经配置 Access。** 部署不附带 Caddy、Basic Auth 账号或本地 CA。

### 本地或宿主机 cloudflared

```bash
docker compose -f compose.host.yaml up -d --build
```

新数据库默认管理和适配器都监听宿主机回环，分别使用 8080、1234 端口；本地测试不需要 Tunnel。宿主机 cloudflared 回源到 `http://127.0.0.1:8080`。发布公网路由前，在本地控制台把完整公开地址（如 `https://frostagent.example.com`）加入 `HTTP_ALLOWED_ORIGINS`。

此方案的隔离保证仅适用于原生 Linux Engine。Docker Desktop 的 host 网络需要主动开启，且转发实现不同，未验证 Windows/macOS 的外部接口暴露范围；本地使用可选 [原生构建](../README_zh_CN.md#快速开始)。

### 容器 cloudflared

仅启动 FrostAgent：

```bash
docker compose up -d --build
```

管理只监听**容器内回环**，不发布端口，宿主机浏览器不能直接打开。适配器监听容器内 `0.0.0.0:1234`，映射到宿主机 `127.0.0.1:1234`；同网络适配器使用 `ws://frostagent:1234/instances/<实例ID>/ws/onebot` 或 `/ws/astrbot`。1234 不提供管理页面、API 或 RPC。

cloudflared 必须与 FrostAgent **共享网络命名空间**，仅加入同一桥接网络不够。让本项目运行已有 Tunnel 的 connector 时，按 [`docker.env.example`](../docker.env.example) 在 `.env` 中填写 `TUNNEL_TOKEN`，然后执行：

```bash
docker compose -f compose.yaml -f compose.cloudflare.yaml up -d --build
```

回源地址为 `http://127.0.0.1:8080`。更新时始终带上两个文件，使 connector 随 FrostAgent 重建。若 connector 已在别处运行，不必重复启动；为其设置 `network_mode: container:<FrostAgent容器ID>`，FrostAgent 重建后也须重建 connector。

发布路由前，先配置 Access，再设置 `HTTP_ALLOWED_ORIGINS`。可用上述 Linux 本地控制台，或展开下面的一次性配置命令。

<details>
<summary>首次设置公开 Host/Origin（Bash）</summary>

替换示例域名；已有自定义管理端口时同步修改 `base`。Python 辅助容器仅用于配置，不是常驻组件。

仅当已开启 `MCP_ENFORCE_LOCAL_TOKEN` 时，先输入现有管理令牌（不回显）；默认部署跳过：

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

完成后执行 `unset FROSTAGENT_BOOTSTRAP_TOKEN`（若之前设置了它）。

</details>

## 不要改动的安全边界

- 管理监听必须是数值回环 IP，且与适配器端口不同；启动、热更新、数据库切换都会拒绝不安全地址。**不要改成 `0.0.0.0` 来解决 Tunnel 连接问题。**
- 宿主机回环端口映射不能隔离同一桥接网络的容器，也不能让宿主机访问容器内回环。管理隔离依靠实际监听地址与网络命名空间，伪造 Host 或 Cloudflare 请求头不能绕过它。
- 镜像只信任实际来自回环的 connector 连接，保留公开 Host/Origin 和转发头；这是网络信任，不是请求头认证。通过 Access 的请求默认免额外本地登录，但显式启用 `MCP_ENFORCE_LOCAL_TOKEN` 后，前端和配置请求仍需 Bearer Token。普通构建规则见[管理认证](configuration.md#管理访问认证)。
- SQL 设置优先于镜像默认值。旧库的 `127.0.0.1:8081` 仍有效，Tunnel 应指向实际端口。桥接切换 host 前，若只允许本机适配器，先把已保存的 `WS_LISTEN_ADDR=0.0.0.0:1234` 改成 `127.0.0.1:1234`。

## 升级与数据保留

- 升级前备份应用卷和外部 PostgreSQL。`frostagent-data` 保存 `/app/data` 中的数据库、引导选择和贴图文件。
- `docker compose down` 保留卷；**`down -v` 会删除数据卷**，仅在明确要删数据时使用。
- 从旧 gateway 部署升级：先用旧文件执行 `down`（不带 `-v`），再选新方案启动；保持相同 Compose 项目名以复用数据。旧 `frostagent-gateway-data` 证书卷不再使用，也不自动删除。
- 应用配置来自 SQL；`.env` 的 `TUNNEL_TOKEN` 只供可选 connector 使用。容器的 `localhost` 指向当前命名空间，外部数据库、模型和沙箱必须使用可达地址。
- 镜像以 `10001:10001` 运行，系统 CA 包用于出站 TLS；不包含 Node.js/Python stdio MCP 运行时，执行命令仍需外部沙箱。

## 验证范围

[Docker CI](../.github/workflows/docker.yml) 构建两种镜像，并验证回源、Host/Origin、管理隔离、端口、OneBot/AstrBot、SQL 监听约束、非 root、退出和持久化。它使用本地模拟回源及只执行 `version` 的真实 cloudflared 镜像，**不验证**公网 Tunnel、Access 云端登录/策略、真实 IM 接入或 Docker Desktop 宿主机转发。
