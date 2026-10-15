"""Exercise authenticated Docker deployment using only disposable data."""

import base64
import json
import os
import re
import secrets
import socket
import sqlite3
import ssl
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from contextlib import closing
from pathlib import Path


PEER_SCRIPT = """
import http.client, json, socket, ssl, sys
request = json.load(sys.stdin)
try:
    connection = socket.create_connection(('gateway', request['port']), timeout=5)
except ConnectionRefusedError:
    print(json.dumps({'status': 0}))
    sys.exit(0)
if request.get('tls'):
    context = ssl.create_default_context(cadata=request['ca'])
    context.minimum_version = ssl.TLSVersion.TLSv1_2
    connection = context.wrap_socket(connection, server_hostname='localhost')
body = json.dumps(request['payload']) if request.get('payload') is not None else ''
headers = {'Host': '127.0.0.1:8080', 'Connection': 'close',
           'Content-Type': 'application/json', 'Content-Length': str(len(body.encode()))}
headers.update(request.get('headers', {}))
raw = request['method'] + ' ' + request['path'] + ' HTTP/1.1\\r\\n'
raw += ''.join(key + ': ' + value + '\\r\\n' for key, value in headers.items())
connection.sendall((raw + '\\r\\n' + body).encode())
response = http.client.HTTPResponse(connection)
response.begin()
data = '' if response.status == 101 else response.read().decode()
print(json.dumps({'status': response.status, 'body': data}))
connection.close()
"""


def command(*args, env=None):
    return subprocess.check_output(args, text=True, encoding="utf-8", env=env).strip()


def request(base, path, payload=None, authorization=None, context=None):
    data = None if payload is None else json.dumps(payload).encode()
    headers = {"Content-Type": "application/json", "Origin": base}
    if authorization:
        headers["Authorization"] = authorization
    req = urllib.request.Request(base + path, data=data, headers=headers)
    with urllib.request.urlopen(req, timeout=5, context=context) as response:
        return response.read()


def wait_ready(base, authorization, context):
    deadline = time.monotonic() + 60
    last_error = None
    while time.monotonic() < deadline:
        try:
            return request(base, "/", authorization=authorization, context=context).decode()
        except (OSError, urllib.error.URLError) as error:
            last_error = error
            time.sleep(0.2)
    raise AssertionError(f"service did not become ready at {base}: {last_error}")


def websocket(port, instance_id, adapter, context=None, authorization=None):
    connection = socket.create_connection(("127.0.0.1", port), timeout=5)
    if context:
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        connection = context.wrap_socket(connection, server_hostname="127.0.0.1")
    with connection:
        scheme = "https" if context else "http"
        auth = f"Authorization: {authorization}\r\n" if authorization else ""
        connection.sendall(
            (
                f"GET /instances/{instance_id}/ws/{adapter}?mock=true HTTP/1.1\r\n"
                f"Host: 127.0.0.1:{port}\r\nOrigin: {scheme}://127.0.0.1:{port}\r\n"
                "Upgrade: websocket\r\nConnection: Upgrade\r\n"
                "Sec-WebSocket-Version: 13\r\n"
                "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
                f"{auth}\r\n"
            ).encode()
        )
        response = b""
        while b"\r\n\r\n" not in response:
            chunk = connection.recv(4096)
            if not chunk:
                break
            response += chunk
        assert response.startswith(b"HTTP/1.1 101 "), response


def main():
    root = Path(__file__).resolve().parents[1]
    project = "frostagent-smoke-" + uuid.uuid4().hex[:12]
    password = secrets.token_urlsafe(24)
    username = "synthetic-admin"
    authorization = "Basic " + base64.b64encode(f"{username}:{password}".encode()).decode()
    password_hash = command("docker", "run", "--rm", "caddy:2-alpine", "caddy",
                            "hash-password", "--plaintext", password)
    env = {**os.environ, "FROSTAGENT_ADMIN_USER": username,
           "FROSTAGENT_ADMIN_PASSWORD_HASH": password_hash}
    compose_source = ("docker", "compose", "-p", project, "--env-file", os.devnull,
                      "-f", str(root / "compose.yaml"))
    missing_credentials = {key: value for key, value in env.items()
                           if key != "FROSTAGENT_ADMIN_PASSWORD_HASH"}
    assert subprocess.run((*compose_source, "config", "--quiet"), env=missing_credentials,
                          capture_output=True).returncode != 0, "empty credentials were accepted"
    config = json.loads(command(*compose_source, "config", "--format", "json", env=env))
    gateway = config["services"]["gateway"]
    gateway["ports"] = [{"target": port, "published": "0", "host_ip": "127.0.0.1",
                         "protocol": "tcp"} for port in (8080, 1234, 1235)]
    service = config["services"]["frostagent"]
    service["environment"] = {"LISTEN_ADDR": "0.0.0.0:9999", "WS_LISTEN_ADDR": "0.0.0.0:9998"}
    service["restart"] = "no"
    gateway["restart"] = "no"
    command("docker", "pull", "python:3.13-alpine")
    with tempfile.TemporaryDirectory(prefix=project) as temporary:
        config_path = Path(temporary) / "compose.json"
        config_path.write_text(json.dumps(config), encoding="utf-8")
        compose = ("docker", "compose", "-p", project, "-f", str(config_path))

        def port(number):
            return int(command(*compose, "port", "gateway", str(number)).rsplit(":", 1)[1])

        def peer(port_number, path, method="GET", payload=None, headers=None, tls=True):
            data = {"port": port_number, "path": path, "method": method, "payload": payload,
                    "headers": headers or {}, "tls": tls, "ca": certificate}
            result = subprocess.run(
                ("docker", "run", "--rm", "-i", "--network", project + "_default",
                 "python:3.13-alpine", "python", "-c", PEER_SCRIPT), input=json.dumps(data),
                capture_output=True, text=True, encoding="utf-8", check=True, timeout=30)
            return json.loads(result.stdout)

        try:
            command(*compose, "up", "--detach", "--no-build")
            certificate = ""
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                result = subprocess.run((*compose, "exec", "-T", "gateway", "cat",
                                         "/data/caddy/pki/authorities/local/root.crt"),
                                        capture_output=True, text=True, encoding="utf-8")
                if result.returncode == 0:
                    certificate = result.stdout
                    break
                time.sleep(0.2)
            assert certificate, "gateway did not create its TLS authority"
            context = ssl.create_default_context(cadata=certificate)
            context.minimum_version = ssl.TLSVersion.TLSv1_2
            base = f"https://127.0.0.1:{port(8080)}"

            def authenticated(path, payload=None):
                return request(base, path, payload, authorization, context)

            def api(path, payload=None):
                return json.loads(authenticated(path, payload))

            html = wait_ready(base, authorization, context)
            assert "<html" in html.lower(), "embedded frontend is missing"
            asset = re.search(r'<script[^>]+src="([^"]+)"', html)
            assert asset and authenticated(asset.group(1)), "frontend script is missing"
            for path, method, payload in (
                ("/", "GET", None),
                ("/api/instances", "GET", None),
                ("/api/instances", "POST", {"name": "unauthorized"}),
                ("/api/instances/global/import/settings", "POST", {}),
                ("/api/storage", "GET", None),
                ("/frostagent.v1.ModelRouterService/GetState", "POST", {}),
            ):
                assert peer(8080, path, method, payload)["status"] == 401, path
            assert peer(8080, "/api/instances", headers={"Authorization": "Basic invalid"})["status"] == 401
            assert peer(8080, "/api/instances", headers={"Authorization": authorization})["status"] == 200
            assert peer(8081, "/api/instances", tls=False)["status"] == 0, "management listener was exposed"
            assert peer(2019, "/config/", tls=False)["status"] == 0, "proxy admin API was exposed"
            assert peer(1234, "/api/instances", tls=False)["status"] == 404
            assert not api("/api/instances")["instances"], "unauthorized request wrote an instance"
            assert command(*compose, "exec", "-T", "frostagent", "id", "-u") == "10001"
            instance = api("/api/instances", {"name": "Docker smoke"})
            assert not instance.get("error"), instance
            assert api(f'/api/instances/{instance["id"]}/enable', {"enabled": True}).get("success")
            for adapter in ("onebot", "astrbot"):
                websocket(port(1234), instance["id"], adapter)
                websocket(port(8080), instance["id"], adapter, context, authorization)
                path = f'/instances/{instance["id"]}/ws/{adapter}?mock=true'
                upgrade = {"Connection": "Upgrade", "Upgrade": "websocket",
                           "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ=="}
                assert peer(8080, path, headers=upgrade)["status"] == 401
                assert peer(1234, path, headers=upgrade, tls=False)["status"] == 101

            settings = api("/api/instances/global/backup/settings")
            settings["settings"]["WS_LISTEN_ADDR"] = "0.0.0.0:1235"
            assert api("/api/instances/global/import/settings", settings).get("success")
            websocket(port(1235), instance["id"], "onebot")
            for address in ("0.0.0.0:8081", "[::]:8081", ":8081", "127.0.0.1:1235"):
                unsafe = json.loads(json.dumps(settings))
                unsafe["settings"]["LISTEN_ADDR"] = address
                try:
                    authenticated("/api/instances/global/import/settings", unsafe)
                    raise AssertionError(f"unsafe listener {address} was accepted")
                except urllib.error.HTTPError as error:
                    assert error.code == 400, error
                assert "LISTEN_ADDR" not in api("/api/instances/global/backup/settings")["settings"]
            assert peer(8081, "/api/instances", tls=False)["status"] == 0
            command(*compose, "exec", "-T", "frostagent", "sh", "-c",
                    "printf 'synthetic-sticker-bytes' > /app/data/docker-smoke-marker")
            container = command(*compose, "ps", "--quiet", "frostagent")
            command(*compose, "stop")
            assert command("docker", "inspect", "--format", "{{.State.ExitCode}}", container) == "0"
            command(*compose, "up", "--detach", "--no-build", "--force-recreate")
            base = f"https://127.0.0.1:{port(8080)}"
            wait_ready(base, authorization, context)
            persisted = api("/api/instances")["instances"]
            assert len(persisted) == 1 and persisted[0]["id"] == instance["id"], persisted
            assert persisted[0]["enabled"] and not persisted[0].get("error"), persisted
            assert command(*compose, "exec", "-T", "frostagent", "cat", "/app/data/docker-smoke-marker") == "synthetic-sticker-bytes"
            for adapter in ("onebot", "astrbot"):
                websocket(port(1235), instance["id"], adapter)
            assert peer(8080, "/api/instances")["status"] == 401

            # An existing database with an unsafe listener must fail before binding.
            container = command(*compose, "ps", "--quiet", "frostagent")
            command(*compose, "stop", "frostagent")
            database_path = Path(temporary) / "frostagent.db"
            command("docker", "cp", container + ":/app/data/frostagent.db", str(database_path))
            with closing(sqlite3.connect(database_path)) as database:
                database.execute("INSERT INTO settings(scope, instance_id, key, value) "
                                 "VALUES ('global', '', 'LISTEN_ADDR', '0.0.0.0:8081') "
                                 "ON CONFLICT(scope, instance_id, key) DO UPDATE SET value=excluded.value")
                database.commit()
            command("docker", "cp", str(database_path), container + ":/app/data/frostagent.db")
            volume = config["volumes"]["frostagent-data"]["name"]
            command("docker", "run", "--rm", "--user", "0", "--entrypoint", "chown",
                    "-v", volume + ":/app/data", "frostagent:local", "10001:10001", "/app/data/frostagent.db")
            command(*compose, "start", "frostagent")
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                if command("docker", "inspect", "--format", "{{.State.Status}}", container) == "exited":
                    break
                time.sleep(0.2)
            assert command("docker", "inspect", "--format", "{{.State.ExitCode}}", container) == "1"
            startup_logs = subprocess.run(("docker", "logs", container), capture_output=True,
                                          text=True, encoding="utf-8", check=True)
            assert "management listener must use a loopback IP" in startup_logs.stdout + startup_logs.stderr
            assert peer(8081, "/api/instances", tls=False)["status"] == 0
            print("PASS: HTTPS authentication, same-network isolation, SQL listener guards, frontend, adapters, graceful shutdown and persistence")
        except BaseException:
            subprocess.run((*compose, "logs", "--no-color"), check=False)
            raise
        finally:
            subprocess.run((*compose, "down", "--volumes"), check=True)


if __name__ == "__main__":
    main()
