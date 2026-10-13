"""Exercise the built image and Compose deployment with disposable data."""

import json
import re
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path


def command(*args):
    return subprocess.check_output(args, text=True).strip()


def request(base, path, payload=None):
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(
        base + path, data=data, headers={"Content-Type": "application/json"}
    )
    with urllib.request.urlopen(req, timeout=5) as response:
        return response.read()


def json_request(base, path, payload=None):
    return json.loads(request(base, path, payload))


def wait_ready(base):
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        try:
            return request(base, "/").decode()
        except (OSError, urllib.error.URLError):
            time.sleep(0.2)
    raise AssertionError(f"service did not become ready at {base}")


def websocket(port, instance_id, adapter):
    with socket.create_connection(("127.0.0.1", port), timeout=5) as connection:
        connection.sendall(
            (
                f"GET /instances/{instance_id}/ws/{adapter}?mock=true HTTP/1.1\r\n"
                f"Host: 127.0.0.1:{port}\r\n"
                "Upgrade: websocket\r\nConnection: Upgrade\r\n"
                "Sec-WebSocket-Version: 13\r\n"
                "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"
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
    config = json.loads(
        command("docker", "compose", "-p", project, "-f", str(root / "compose.yaml"),
                "config", "--format", "json")
    )
    service = config["services"]["frostagent"]
    service["ports"] = [
        {"target": port, "published": "0", "host_ip": "127.0.0.1", "protocol": "tcp"}
        for port in (8080, 8090, 1234)
    ]
    # Application settings must still come exclusively from SQL.
    service["environment"] = {"LISTEN_ADDR": "127.0.0.1:9999", "WS_LISTEN_ADDR": "127.0.0.1:9998"}
    service["restart"] = "no"
    with tempfile.TemporaryDirectory(prefix=project) as temporary:
        config_path = Path(temporary) / "compose.json"
        config_path.write_text(json.dumps(config), encoding="utf-8")
        compose = ("docker", "compose", "-p", project, "-f", str(config_path))

        def port(number):
            return int(command(*compose, "port", "frostagent", str(number)).rsplit(":", 1)[1])

        try:
            command(*compose, "up", "--detach", "--no-build")
            base = f"http://127.0.0.1:{port(8080)}"
            html = wait_ready(base)
            assert "<html" in html.lower(), "embedded frontend is missing"
            asset = re.search(r'<script[^>]+src="([^"]+)"', html)
            assert asset, "frontend script is missing"
            assert request(base, asset.group(1)), "frontend script is empty"
            assert not json_request(base, "/api/instances")["instances"]
            assert command(*compose, "exec", "-T", "frostagent", "id", "-u") == "10001"
            instance = json_request(base, "/api/instances", {"name": "Docker smoke"})
            assert not instance.get("error"), instance
            result = json_request(base, f'/api/instances/{instance["id"]}/enable', {"enabled": True})
            assert result.get("success"), result
            for adapter in ("onebot", "astrbot"):
                websocket(port(1234), instance["id"], adapter)
                websocket(port(8080), instance["id"], adapter)
            try:
                request(f"http://127.0.0.1:{port(1234)}", "/api/instances")
                raise AssertionError("adapter port exposed management API")
            except urllib.error.HTTPError as error:
                assert error.code == 404, error
            command(*compose, "exec", "-T", "frostagent", "sh", "-c",
                    "printf 'synthetic-sticker-bytes' > /app/data/docker-smoke-marker")

            settings = json_request(base, "/api/instances/global/backup/settings")
            settings["settings"]["LISTEN_ADDR"] = "0.0.0.0:8090"
            result = json_request(base, "/api/instances/global/import/settings", settings)
            assert result.get("success"), result
            wait_ready(f"http://127.0.0.1:{port(8090)}")

            container = command(*compose, "ps", "--quiet", "frostagent")
            command(*compose, "stop")
            assert command("docker", "inspect", "--format", "{{.State.ExitCode}}", container) == "0"
            command(*compose, "up", "--detach", "--no-build", "--force-recreate")
            base = f"http://127.0.0.1:{port(8090)}"
            wait_ready(base)
            persisted = json_request(base, "/api/instances")["instances"]
            assert len(persisted) == 1 and persisted[0]["id"] == instance["id"], persisted
            assert persisted[0]["enabled"] and not persisted[0].get("error"), persisted
            assert command(*compose, "exec", "-T", "frostagent", "cat", "/app/data/docker-smoke-marker") == "synthetic-sticker-bytes"
            for adapter in ("onebot", "astrbot"):
                websocket(port(1234), instance["id"], adapter)
            print("PASS: frontend, non-root startup, adapter isolation, WebSocket, SQL settings, graceful shutdown and volume persistence")
        except BaseException:
            subprocess.run((*compose, "logs", "--no-color"), check=False)
            raise
        finally:
            subprocess.run((*compose, "down", "--volumes"), check=True)


if __name__ == "__main__":
    main()
