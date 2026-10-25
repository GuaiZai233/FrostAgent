"""Verify origin isolation locally; never open a tunnel or contact an Access app."""

import argparse
import json
import os
import re
import sqlite3
import subprocess
import tempfile
import time
import uuid
from contextlib import closing
from pathlib import Path


CLIENT = r"""
import http.client, json, socket, sys
request = json.load(sys.stdin)
try:
    connection = socket.create_connection((request['host'], request['port']), timeout=3)
except (ConnectionRefusedError, TimeoutError):
    print(json.dumps({'status': 0, 'body': ''}))
    sys.exit(0)
body = json.dumps(request['payload']) if request.get('payload') is not None else ''
headers = {'Host': '127.0.0.1:8080', 'Connection': 'close',
           'Content-Type': 'application/json', 'Content-Length': str(len(body.encode()))}
headers.update(request.get('headers', {}))
raw = request['method'] + ' ' + request['path'] + ' HTTP/1.1\r\n'
raw += ''.join(key + ': ' + value + '\r\n' for key, value in headers.items())
connection.sendall((raw + '\r\n' + body).encode())
response = http.client.HTTPResponse(connection)
response.begin()
data = '' if response.status == 101 else response.read().decode()
print(json.dumps({'status': response.status, 'body': data}))
connection.close()
"""
UPGRADE = {'Connection': 'Upgrade', 'Upgrade': 'websocket',
           'Sec-WebSocket-Version': '13', 'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ=='}
PUBLIC_ORIGIN = 'https://frostagent.example.com'
FORWARDED = {'Host': 'frostagent.example.com', 'Origin': PUBLIC_ORIGIN,
             'X-Forwarded-Proto': 'https', 'X-Forwarded-For': '198.51.100.20',
             'Cf-Access-Authenticated-User-Email': 'synthetic@example.invalid'}


def command(*args, env=None):
    return subprocess.check_output(args, text=True, encoding='utf-8', env=env).strip()


def render(root, project, *files, env=None):
    source = ('docker', 'compose', '-p', project, '--env-file', os.devnull)
    for file in files:
        source += ('-f', str(root / file))
    return json.loads(command(*source, 'config', '--format', 'json', env=env))


def inspect(container):
    return json.loads(command('docker', 'inspect', container))[0]


def client(container, host, port, path='/', payload=None, headers=None, method=None):
    data = {'host': host, 'port': port, 'path': path, 'payload': payload,
            'headers': headers or {}, 'method': method or ('POST' if payload is not None else 'GET')}
    result = subprocess.run(('docker', 'exec', '-i', container, 'python', '-c', CLIENT),
                            input=json.dumps(data), capture_output=True, text=True,
                            encoding='utf-8', check=True, timeout=15)
    return json.loads(result.stdout)


def helper(name, network):
    return command('docker', 'run', '--detach', '--name', name, '--network', network,
                   'python:3.13-alpine', 'sleep', '1800')


def remove(container):
    if container:
        command('docker', 'rm', '--force', container)


def wait_ready(read):
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        result = read('/')
        if result['status'] == 200:
            assert '<html' in result['body'].lower(), 'embedded frontend missing'
            return result['body']
        time.sleep(0.2)
    raise AssertionError('origin did not become ready')




def exercise(root, temporary, host_mode=False):
    project = 'frostagent-smoke-' + uuid.uuid4().hex[:12]
    env = {**os.environ, 'TUNNEL_TOKEN': 'synthetic-unusable-test-token'}
    source = ('compose.host.yaml',) if host_mode else ('compose.yaml', 'compose.cloudflare.yaml')
    config = render(root, project, *source, env=env)
    service = config['services']['frostagent']
    service['restart'] = 'no'
    # Legacy application environment variables must not override SQL/default listeners.
    service['environment'] = {'LISTEN_ADDR': '0.0.0.0:9999', 'WS_LISTEN_ADDR': '0.0.0.0:9998'}
    if host_mode:
        assert not service.get('ports'), 'host mode must not pretend to publish ports'
        assert service['network_mode'] == 'host'
        assert service['build']['args']['DOCKER_WS_LISTEN_ADDR'] == '127.0.0.1:1234'
    else:
        assert {int(p['target']) for p in service['ports']} == {1234}
        assert all(p['host_ip'] == '127.0.0.1' for p in service['ports'])
        service['ports'] = [{'target': p, 'published': '0', 'host_ip': '127.0.0.1',
                             'protocol': 'tcp'} for p in (1234, 1235)]
        connector = config['services']['cloudflared']
        assert connector['network_mode'] == 'service:frostagent'
        assert connector['depends_on']['frostagent']['restart']
        assert not connector.get('ports')
        # Run the real connector image without opening any external tunnel.
        connector['command'], connector['restart'] = ['version'], 'no'
    path = Path(temporary) / (project + '.json')
    path.write_text(json.dumps(config), encoding='utf-8')
    compose = ('docker', 'compose', '-p', project, '-f', str(path))
    origin = peer = None
    try:
        command(*compose, 'up', '--detach', '--no-build')
        app = command(*compose, 'ps', '--quiet', 'frostagent')
        origin = helper(project + '-origin', 'host' if host_mode else 'container:' + app)
        if host_mode:
            command('docker', 'network', 'create', project + '-peer')
            peer = helper(project + '-peer', project + '-peer')
            network = json.loads(command('docker', 'network', 'inspect', project + '-peer'))[0]
            remote_host = network['IPAM']['Config'][0]['Gateway']
        else:
            peer = helper(project + '-peer', project + '_default')
            remote_host = 'frostagent'
            actual = inspect(app)
            assert not actual['HostConfig']['PortBindings'].get('8080/tcp')
            connector_id = command(*compose, 'ps', '--all', '--quiet', 'cloudflared')
            assert inspect(connector_id)['HostConfig']['NetworkMode'] == 'container:' + app

        def read(path, payload=None, headers=None):
            return client(origin, '127.0.0.1', 8080, path, payload, headers)

        def api(path, payload=None, headers=None):
            result = read(path, payload, headers)
            assert result['status'] == 200, (path, result)
            return json.loads(result['body'])

        html = wait_ready(read)
        asset = re.search(r'<script[^>]+src="([^"]+)"', html)
        assert asset and read(asset.group(1))['status'] == 200
        assert read('/api/instances', headers=FORWARDED)['status'] == 403
        settings = api('/api/instances/global/backup/settings')
        settings['settings'].update({'HTTP_ALLOWED_ORIGINS': PUBLIC_ORIGIN,
                                     'WS_ALLOWED_ORIGINS': PUBLIC_ORIGIN})
        assert api('/api/instances/global/import/settings', settings).get('success')
        # A real public Host/Origin and forwarded headers work without another login.
        assert api('/api/instances', headers=FORWARDED)['instances'] == []
        assert read('/api/storage', headers=FORWARDED)['status'] == 200
        assert read('/', headers={**FORWARDED, 'Host': 'attacker.example'})['status'] == 403
        assert read('/', headers={**FORWARDED, 'Origin': 'https://attacker.example'})['status'] == 403
        assert command(*compose, 'exec', '-T', 'frostagent', 'id', '-u') == '10001'
        instance = api('/api/instances', {'name': 'Docker smoke'}, FORWARDED)
        assert not instance.get('error'), instance
        instance_id = instance['id']
        assert api(f'/instances/{instance_id}/frostagent.v1.SettingsService/UpdateEnvVar',
                   {'key': 'MCP_CONTROL_TOKEN', 'value': 'synthetic-control-token'}, FORWARDED).get('success')
        assert read('/api/instances', headers=FORWARDED)['status'] == 200
        assert api(f'/api/instances/{instance_id}/enable', {'enabled': True}, FORWARDED).get('success')
        rpc = f'/instances/{instance_id}/frostagent.v1.SettingsService/ListEnvVars'
        assert read(rpc, {}, FORWARDED)['status'] == 200

        management = [('/', 'GET', None), ('/api/instances', 'GET', None),
                      ('/api/instances', 'POST', {'name': 'unauthorized'}),
                      ('/api/storage', 'GET', None), ('/api/storage', 'POST', {}),
                      ('/api/instances/global/import/settings', 'POST', {}),
                      (rpc, 'POST', {}), ('/frostagent.v1.ModelRouterService/GetState', 'POST', {}),
                      ('/api/security/locked', 'GET', None), ('/api/actionscat/status', 'GET', None)]
        for path, method, payload in management:
            # No Origin and a spoofed local Host cannot bypass network isolation.
            assert client(peer, remote_host, 8080, path, payload, method=method)['status'] == 0, path
            assert client(peer, remote_host, 8080, path, payload, FORWARDED, method)['status'] == 0, path
            if not host_mode:
                assert client(peer, remote_host, 1234, path, payload, method=method)['status'] == 404, path
        assert len(api('/api/instances')['instances']) == 1
        for adapter in ('onebot', 'astrbot'):
            ws = f'/instances/{instance_id}/ws/{adapter}?mock=true'
            assert read(ws, headers={**FORWARDED, **UPGRADE})['status'] == 101
            assert client(origin, '127.0.0.1', 1234, ws, headers=UPGRADE)['status'] == 101
            if host_mode:
                assert client(peer, remote_host, 1234, ws, headers=UPGRADE)['status'] == 0
            else:
                assert client(peer, remote_host, 1234, ws, headers=UPGRADE)['status'] == 101
                published = int(command(*compose, 'port', 'frostagent', '1234').rsplit(':', 1)[1])
                assert client(origin, '127.0.0.1', 1234, rpc, {}, UPGRADE)['status'] == 404
                # Verify the host's published adapter port as well as direct bridge traffic.
                import socket
                with socket.create_connection(('127.0.0.1', published), timeout=5) as connection:
                    connection.sendall(f'GET {ws} HTTP/1.1\r\nHost: localhost\r\n'.encode() +
                                       ''.join(f'{k}: {v}\r\n' for k, v in UPGRADE.items()).encode() + b'\r\n')
                    assert connection.recv(4096).startswith(b'HTTP/1.1 101 ')
        if host_mode:
            print('PASS: host topology, double loopback, public Host/Origin, OneBot/AstrBot')
            return

        settings['settings']['WS_LISTEN_ADDR'] = '0.0.0.0:1235'
        assert api('/api/instances/global/import/settings', settings).get('success')
        for address in ('0.0.0.0:8080', '[::]:8080', ':8080', '127.0.0.1:1235'):
            unsafe = json.loads(json.dumps(settings))
            unsafe['settings']['LISTEN_ADDR'] = address
            assert read('/api/instances/global/import/settings', unsafe)['status'] == 400, address
            assert 'LISTEN_ADDR' not in api('/api/instances/global/backup/settings')['settings']
        for adapter in ('onebot', 'astrbot'):
            ws = f'/instances/{instance_id}/ws/{adapter}?mock=true'
            assert client(peer, remote_host, 1235, ws, headers=UPGRADE)['status'] == 101

        # Preserve explicit local-token enforcement; Access adds no mandatory local token.
        settings['settings']['MCP_ENFORCE_LOCAL_TOKEN'] = 'true'
        assert api('/api/instances/global/import/settings', settings).get('success')
        assert read('/api/instances', headers=FORWARDED)['status'] == 401
        bearer = {**FORWARDED, 'Authorization': 'Bearer synthetic-control-token'}
        assert read('/api/instances', headers=bearer)['status'] == 200
        # The former README bootstrap flow uses these two management endpoints.
        # Test their auth and settings semantics directly rather than executing docs.
        before = api('/api/instances/global/backup/settings', headers=bearer)
        bootstrap_origin = 'https://bootstrap.example.com'
        updated = json.loads(json.dumps(before))
        updated.pop('extra_env', None)
        origins_before = set(filter(None, updated['settings'].get('HTTP_ALLOWED_ORIGINS', '').split(',')))
        updated['settings']['HTTP_ALLOWED_ORIGINS'] = ','.join(sorted(origins_before | {bootstrap_origin}))
        for headers in ({}, {'Authorization': 'Bearer synthetic-incorrect-token'}):
            assert read('/api/instances/global/backup/settings', headers=headers)['status'] == 401
            assert read('/api/instances/global/import/settings', updated, headers=headers)['status'] == 401
            assert api('/api/instances/global/backup/settings', headers=bearer) == before
        assert api('/api/instances/global/import/settings', updated, bearer).get('success')
        after = api('/api/instances/global/backup/settings', headers=bearer)
        origins_after = set(after['settings'].get('HTTP_ALLOWED_ORIGINS', '').split(','))
        assert origins_after == origins_before | {bootstrap_origin}
        before['settings'].pop('HTTP_ALLOWED_ORIGINS', None)
        after['settings'].pop('HTTP_ALLOWED_ORIGINS', None)
        assert after == before, 'origin update changed unrelated settings'
        print('PASS: management endpoint token enforcement and settings preservation')
        settings['settings']['MCP_ENFORCE_LOCAL_TOKEN'] = 'false'
        assert api('/api/instances/global/import/settings', settings, bearer).get('success')
        command(*compose, 'exec', '-T', 'frostagent', 'sh', '-c',
                "printf 'synthetic-sticker-bytes' > /app/data/docker-smoke-marker")
        remove(origin)
        origin = None
        command(*compose, 'stop')
        assert inspect(app)['State']['ExitCode'] == 0
        command(*compose, 'up', '--detach', '--no-build', '--force-recreate')
        app = command(*compose, 'ps', '--quiet', 'frostagent')
        origin = helper(project + '-origin', 'container:' + app)
        wait_ready(read)
        connector_id = command(*compose, 'ps', '--all', '--quiet', 'cloudflared')
        assert inspect(connector_id)['HostConfig']['NetworkMode'] == 'container:' + app
        persisted = api('/api/instances', headers=FORWARDED)['instances']
        assert len(persisted) == 1 and persisted[0]['id'] == instance_id and persisted[0]['enabled'], persisted
        assert command(*compose, 'exec', '-T', 'frostagent', 'cat', '/app/data/docker-smoke-marker') == 'synthetic-sticker-bytes'
        assert client(peer, remote_host, 8080, '/api/storage')['status'] == 0

        # An unsafe saved SQL listener fails before any port is bound.
        command(*compose, 'stop', 'frostagent')
        database_path = Path(temporary) / 'frostagent.db'
        command('docker', 'cp', app + ':/app/data/frostagent.db', str(database_path))
        with closing(sqlite3.connect(database_path)) as database:
            database.execute("INSERT INTO settings(scope, instance_id, key, value) "
                             "VALUES ('global', '', 'LISTEN_ADDR', '0.0.0.0:8080') "
                             "ON CONFLICT(scope, instance_id, key) DO UPDATE SET value=excluded.value")
            database.commit()
        command('docker', 'cp', str(database_path), app + ':/app/data/frostagent.db')
        volume = config['volumes']['frostagent-data']['name']
        command('docker', 'run', '--rm', '--user', '0', '--entrypoint', 'chown',
                '-v', volume + ':/app/data', 'frostagent:local', '10001:10001', '/app/data/frostagent.db')
        command(*compose, 'start', 'frostagent')
        deadline = time.monotonic() + 15
        while inspect(app)['State']['Status'] != 'exited' and time.monotonic() < deadline:
            time.sleep(0.2)
        assert inspect(app)['State']['ExitCode'] == 1
        logs = subprocess.run(('docker', 'logs', app), capture_output=True, text=True,
                              encoding='utf-8', check=True)
        assert 'management listener must use a loopback IP' in logs.stdout + logs.stderr
        assert read('/api/instances')['status'] == 0
        print('PASS: connector namespace, bridge isolation, public Host/Origin, API/RPC, '
              'OneBot/AstrBot, SQL guards, non-root, graceful shutdown and persistence')
    except BaseException:
        subprocess.run((*compose, 'logs', '--no-color'), check=False)
        raise
    finally:
        remove(origin)
        remove(peer)
        subprocess.run((*compose, 'down', '--volumes'), check=True)
        if host_mode:
            subprocess.run(('docker', 'network', 'rm', project + '-peer'), check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--host-network', action='store_true',
                        help='also test the Linux Engine host namespace; no Desktop forwarding check')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    default = render(root, 'frostagent-config-smoke', 'compose.yaml')
    assert set(default['services']) == {'frostagent'}, 'default deployment added another component'
    assert set(default['volumes']) == {'frostagent-data'}
    no_token = {key: value for key, value in os.environ.items() if key != 'TUNNEL_TOKEN'}
    missing = subprocess.run(('docker', 'compose', '--env-file', os.devnull,
                              '-f', str(root / 'compose.yaml'), '-f', str(root / 'compose.cloudflare.yaml'),
                              'config', '--quiet'), env=no_token, capture_output=True)
    assert missing.returncode != 0, 'optional connector accepted an empty token'
    command('docker', 'pull', 'python:3.13-alpine')
    command('docker', 'pull', 'cloudflare/cloudflared:latest')
    with tempfile.TemporaryDirectory(prefix='frostagent-smoke-') as temporary:
        exercise(root, temporary)
        if args.host_network:
            exercise(root, temporary, host_mode=True)
        else:
            print('NOT EXECUTED: opt-in Linux host topology (pass --host-network to include it)')
    print('NOT EXECUTED: live Cloudflare Access authentication, public Tunnel, real IM platforms, '
          'or Docker Desktop host forwarding')


if __name__ == '__main__':
    main()
