# Docker deployment

[Back to README](../README.md) · [中文](docker.md)

Run commands from the repository root. Choose one topology based on where cloudflared runs; do not combine host and bridge files.

## Choose a topology

| Use case                                                   | Files                                                     | Management console                                           |
| ---------------------------------------------------------- | --------------------------------------------------------- | ------------------------------------------------------------ |
| Native Linux local testing or cloudflared on the same host | `compose.host.yaml`                                       | Host `http://127.0.0.1:8080`                                 |
| Containerized cloudflared                                  | `compose.yaml`, optionally with `compose.cloudflare.yaml` | `127.0.0.1:8080` inside the container network namespace only |

**Before public access, configure Cloudflare Access for every path on the management hostname, with no API/RPC Bypass. A working Tunnel does not configure Access.** This deployment includes no Caddy, Basic Auth accounts or local CA.

### Local use or host cloudflared

```bash
docker compose -f compose.host.yaml up -d --build
```

New databases default both listeners to host loopback: management on 8080, adapters on 1234. Local testing needs no Tunnel. Host cloudflared uses `http://127.0.0.1:8080` as its origin. Before publishing a route, add the full public origin, such as `https://frostagent.example.com`, to `HTTP_ALLOWED_ORIGINS` through the local console.

This isolation guarantee targets native Linux Engine only. Docker Desktop host networking requires opt-in and uses different forwarding; Windows/macOS external-interface exposure is unverified. For a local console, use the [native build](../README.md#quick-start).

### Container cloudflared

Start only FrostAgent:

```bash
docker compose up -d --build
```

Management binds **container loopback** without publishing a port, so the host browser cannot open it. Adapters bind container `0.0.0.0:1234`, published at host `127.0.0.1:1234`. Same-network adapters use `ws://frostagent:1234/instances/<instance-id>/ws/onebot` or `/ws/astrbot`. Port 1234 serves no management pages, APIs or RPCs.

cloudflared must **share FrostAgent's network namespace**, not merely join its bridge network. To run your existing Tunnel connector here, set `TUNNEL_TOKEN` in `.env` using [`docker.env.example`](../docker.env.example), then run:

```bash
docker compose -f compose.yaml -f compose.cloudflare.yaml up -d --build
```

Use `http://127.0.0.1:8080` as the origin. Always include both files during updates so the connector is recreated with FrostAgent. If a connector already runs elsewhere, do not duplicate it: use `network_mode: container:<FrostAgent-container-ID>` and recreate it whenever FrostAgent is recreated.

Before publishing the route, configure Access and `HTTP_ALLOWED_ORIGINS`. Use the Linux local console above or the one-time command below.

<details>
<summary>Bootstrap the public Host/Origin (Bash)</summary>

Replace the example hostname; adjust `base` if a custom management port is saved. The disposable Python container is a configuration helper, not a runtime component.

Only if `MCP_ENFORCE_LOCAL_TOKEN` is enabled, enter the existing management token without echoing it. Skip this for the default deployment:

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

Run `unset FROSTAGENT_BOOTSTRAP_TOKEN` afterwards if you set it.

</details>

## Security boundaries

- Management must bind a numeric loopback IP on a different port from adapters. Startup, hot updates and database switches reject unsafe addresses. **Do not switch management to `0.0.0.0` to fix Tunnel connectivity.**
- Host-loopback publishing neither isolates bridge peers nor makes container-loopback listeners reachable from the host. Isolation depends on actual listener binds and network namespaces; spoofed Host or Cloudflare headers cannot bypass it.
- Images trust only actual loopback connector connections, preserving public Host/Origin and forwarding headers. This is network trust, not header authentication. Access-protected requests need no extra login by default, but explicit `MCP_ENFORCE_LOCAL_TOKEN` still requires a Bearer token in the frontend and configuration requests. See [ordinary-build authentication](configuration.en.md#management-authentication).
- SQL settings override image defaults. A saved `127.0.0.1:8081` remains valid; point the Tunnel at the actual port. Before switching bridge to host, change saved `WS_LISTEN_ADDR=0.0.0.0:1234` to `127.0.0.1:1234` if adapters should remain host-local.

## Upgrades and data

- Back up application volumes and external PostgreSQL before upgrading. `frostagent-data` stores `/app/data`, including the database, bootstrap selection and sticker files.
- `docker compose down` retains volumes; **`down -v` deletes data volumes**. Use it only when deletion is intended.
- From a legacy gateway deployment, run `down` with the old files (without `-v`), then start the chosen topology. Keep the same Compose project name to reuse data. The unused `frostagent-gateway-data` certificate volume is not deleted automatically.
- Application settings come from SQL; only the optional connector uses `.env`'s `TUNNEL_TOKEN`. Container `localhost` means the current namespace; external databases, models and sandboxes need reachable addresses.
- Images run as `10001:10001` with system CAs for outbound TLS. They contain no Node.js/Python stdio MCP runtime; command execution still requires an external sandbox.

## Verification scope

[Docker CI](../.github/workflows/docker.yml) builds both images and checks origin reachability, Host/Origin, management isolation, ports, OneBot/AstrBot, SQL listener guards, non-root operation, shutdown and persistence. It uses a synthetic local origin and a real cloudflared image running only `version`. It **does not verify** public Tunnels, live Access login/policies, real IM integration or Docker Desktop host forwarding.
