"""Exercise the opt-in local panel with isolated test data on Desktop/Linux Docker.

--config-only checks Compose rendering without a running Docker daemon. The live
test requires an already-built frostagent:local image and never uses user data.
"""

import argparse
import copy
import http.client
import json
import os
import re
import socket
import subprocess
import tempfile
import time
import uuid
from pathlib import Path


SOCKET_PATH = '/run/frostagent-panel/management.sock'
HELPER_IMAGE = 'python:3.13-alpine'
SERVICES = ('frostagent', 'local-panel-origin', 'local-panel')
UPGRADE = {'Connection': 'Upgrade', 'Upgrade': 'websocket',
           'Sec-WebSocket-Version': '13', 'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ=='}
TOKEN = 'synthetic-local-panel-token'
BEARER = {'Authorization': 'Bearer ' + TOKEN}


def command(*args, env=None):
    return subprocess.check_output(args, text=True, encoding='utf-8', env=env).strip()


def render(root, project, local=True, panel_port=8088, target_port=8080):
    env = {**os.environ, 'FROSTAGENT_PANEL_PORT': str(panel_port),
           'FROSTAGENT_PANEL_TARGET_PORT': str(target_port)}
    args = ('docker', 'compose', '-p', project, '--env-file', os.devnull,
            '-f', str(root / 'compose.yaml'))
    if local:
        args += ('-f', str(root / 'compose.local.yaml'))
    return json.loads(command(*args, 'config', '--format', 'json', env=env))


def validate_config(base, local, panel_port=8088, target_port=8080):
    """Reject topology changes that would silently widen the trust boundary."""
    assert set(base['services']) == {'frostagent'}
    assert set(local['services']) == set(SERVICES)
    assert local['services']['frostagent'] == base['services']['frostagent'], \
        'local panel must not modify the app, adapter network, ports or data mount'
    assert local['volumes']['frostagent-data'] == base['volumes']['frostagent-data']
    assert set(local['volumes']) == {'frostagent-data', 'local-panel-socket'}
    origin = local['services']['local-panel-origin']
    ingress = local['services']['local-panel']
    assert origin['network_mode'] == 'service:frostagent'
    assert not origin.get('networks') and not origin.get('ports')
    assert origin['depends_on']['frostagent']['restart']
    assert origin['entrypoint'] == ['/bin/sh', '-ec']
    assert str(origin['environment']['FROSTAGENT_PANEL_TARGET_PORT']) == str(target_port)
    origin_command = '\n'.join(origin['command'])
    assert 'UNIX-LISTEN:' + SOCKET_PATH in origin_command
    assert ',unlink-early,mode=0600,fork' in origin_command
    assert 'TCP4:127.0.0.1:' in origin_command
    assert not re.search(r'TCP\d?-LISTEN:', origin_command, re.IGNORECASE), \
        'origin must never open a management listener on the adapter network'
    assert ingress['depends_on']['local-panel-origin']['restart']
    assert set(ingress['networks']) == {'local-panel'}
    assert not ingress.get('network_mode')
    ports = ingress['ports']
    assert len(ports) == 1
    assert ports[0]['host_ip'] == '127.0.0.1'
    assert int(ports[0]['published']) == panel_port and int(ports[0]['target']) == 8080
    assert ports[0]['protocol'] == 'tcp'
    assert ingress['command'][1] == 'UNIX-CONNECT:' + SOCKET_PATH
    assert 'TCP4-LISTEN:8080' in ingress['command'][0]
    for service in (origin, ingress):
        assert re.fullmatch(r'alpine/socat:[^@]+@sha256:[0-9a-f]{64}', service['image'])
        assert service['user'] == '10001:10001'
        assert service['read_only'] and service['init']
        assert service['cap_drop'] == ['ALL']
        assert 'no-new-privileges:true' in service['security_opt']
        assert not service.get('privileged') and not service.get('cap_add')
        assert len(service['volumes']) == 1
        mount = service['volumes'][0]
        assert mount['source'] == 'local-panel-socket'
        assert mount['target'] == '/run/frostagent-panel'
        assert mount['volume']['nocopy']
    assert ingress['volumes'][0]['read_only']
    network = local['networks']['local-panel']
    assert not network.get('internal'), 'internal-only bridges suppress port publication'
    assert network['driver'] == 'bridge'
    assert network['driver_opts']['com.docker.network.bridge.enable_icc'] == 'false'
    volume = local['volumes']['local-panel-socket']
    assert volume['driver'] == 'local'
    assert volume['driver_opts']['type'] == volume['driver_opts']['device'] == 'tmpfs'
    options = set(volume['driver_opts']['o'].split(','))
    assert {'uid=10001', 'gid=10001', 'mode=0700', 'nosuid', 'nodev', 'noexec'} <= options


def http_request(host, port, path='/', payload=None, headers=None, method=None):
    """Use direct sockets, never HTTP proxy environment variables."""
    connection = None
    try:
        connection = socket.create_connection((host, port), timeout=3)
        data = json.dumps(payload).encode() if payload is not None else b''
        fields = {'Host': f'127.0.0.1:{port}', 'Connection': 'close',
                  'Content-Type': 'application/json', 'Content-Length': str(len(data))}
        fields.update(headers or {})
        method = method or ('POST' if payload is not None else 'GET')
        request = f'{method} {path} HTTP/1.1\r\n'
        request += ''.join(f'{key}: {value}\r\n' for key, value in fields.items()) + '\r\n'
        connection.sendall(request.encode() + data)
        response = http.client.HTTPResponse(connection)
        response.begin()
        body = '' if response.status == 101 else response.read().decode()
        return {'status': response.status, 'body': body}
    except (OSError, http.client.HTTPException):
        return {'status': 0, 'body': ''}
    finally:
        if connection is not None:
            connection.close()


PEER_REQUEST = r"""
import http.client, json, socket, sys
request = json.load(sys.stdin)
try:
    connection = socket.create_connection((request['host'], request['port']), timeout=2)
    headers = {'Host': '127.0.0.1:8080', 'Connection': 'close'}
    headers.update(request.get('headers', {}))
    data = 'GET ' + request['path'] + ' HTTP/1.1\r\n'
    data += ''.join(key + ': ' + value + '\r\n' for key, value in headers.items()) + '\r\n'
    connection.sendall(data.encode())
    response = http.client.HTTPResponse(connection)
    response.begin()
    print(json.dumps({'status': response.status}))
    connection.close()
except (OSError, http.client.HTTPException):
    print(json.dumps({'status': 0}))
"""


def peer_request(container, host, port, path='/api/instances', headers=None):
    result = subprocess.run(('docker', 'exec', '-i', container, 'python', '-c', PEER_REQUEST),
                            input=json.dumps({'host': host, 'port': port, 'path': path,
                                              'headers': headers or {}}),
                            text=True, encoding='utf-8', capture_output=True, check=True,
                            timeout=12)
    return json.loads(result.stdout)['status']


def inspect(container):
    return json.loads(command('docker', 'inspect', container))[0]


def wait_ready(read):
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        result = read('/')
        if result['status'] == 200:
            assert '<html' in result['body'].lower(), 'embedded frontend missing'
            return result['body']
        time.sleep(0.25)
    raise AssertionError('local panel did not become ready')


def prepare_test_config(config, project, ports=None):
    """Only generated smoke resources may be created or removed by this script."""
    assert re.fullmatch(r'frostagent-local-smoke-[0-9a-f]{12}', project)
    for kind in ('volumes', 'networks'):
        for resource in config[kind].values():
            assert not resource.get('external')
            assert resource['name'].startswith(project + '_')
    for name, service in config['services'].items():
        assert not service.get('container_name')
        service['restart'] = 'no'
        for port in service.get('ports', []):
            port['published'] = str((ports or {}).get(name, 0))
    return config


def exercise(root):
    project = 'frostagent-local-smoke-' + uuid.uuid4().hex[:12]
    config = prepare_test_config(render(root, project), project)
    operating_system = command('docker', 'info', '--format', '{{.OperatingSystem}}')
    desktop = 'docker desktop' in operating_system.lower()
    command('docker', 'image', 'inspect', 'frostagent:local')
    helpers = []
    with tempfile.TemporaryDirectory(prefix=project + '-') as temporary:
        path = Path(temporary) / 'compose.json'
        path.write_text(json.dumps(config), encoding='utf-8')
        compose = ('docker', 'compose', '-p', project, '-f', str(path))

        def container(service):
            return command(*compose, 'ps', '--quiet', service)

        def published(service, port):
            address = command(*compose, 'port', service, str(port))
            assert address.startswith('127.0.0.1:'), address
            return int(address.rsplit(':', 1)[1])

        def helper(suffix, network):
            args = ('docker', 'run', '--detach', '--name', project + '-' + suffix,
                    '--network', network)
            if not desktop:
                args += ('--add-host', 'host.docker.internal:host-gateway')
            identifier = command(*args, HELPER_IMAGE, 'sleep', '1800')
            helpers.append(identifier)
            return identifier

        try:
            command(*compose, 'up', '--detach', '--no-build')
            panel_port = published('local-panel', 8080)
            ws_port = published('frostagent', 1234)

            def read(endpoint='/', payload=None, headers=None):
                return http_request('127.0.0.1', panel_port, endpoint, payload, headers)

            def api(endpoint, payload=None, headers=None):
                result = read(endpoint, payload, headers)
                assert result['status'] == 200, (endpoint, result)
                return json.loads(result['body'])

            html = wait_ready(read)
            asset = re.search(r'<script[^>]+src="([^"]+)"', html)
            assert asset and read(asset.group(1))['status'] == 200
            assert api('/api/instances')['instances'] == []
            assert read('/api/storage')['status'] == 200
            for endpoint in ('/', '/api/instances', '/api/storage'):
                assert read(endpoint, headers={'Host': 'attacker.example'})['status'] == 403
                assert read(endpoint, headers={'Origin': 'https://attacker.example'})['status'] == 403

            default_network = config['networks']['default']['name']
            panel_network = config['networks']['local-panel']['name']
            peer = helper('adapter-peer', default_network)
            panel_peer = helper('panel-peer', panel_network)
            network = json.loads(command('docker', 'network', 'inspect', default_network))[0]
            gateway = network['IPAM']['Config'][0]['Gateway']
            desktop_gateway = command('docker', 'exec', peer, 'python', '-c',
                                      "import socket; print(socket.gethostbyname('host.docker.internal'))")

            def check_isolation(target_port=8080):
                app = container('frostagent')
                actual = inspect(app)
                assert not actual['HostConfig']['PortBindings'].get('8080/tcp')
                assert not actual['HostConfig']['PortBindings'].get('18080/tcp')
                origin = inspect(container('local-panel-origin'))
                assert origin['HostConfig']['NetworkMode'] == 'container:' + app
                ingress = inspect(container('local-panel'))
                ingress_ip = ingress['NetworkSettings']['Networks'][panel_network]['IPAddress']
                assert ingress_ip
                assert set(ingress['NetworkSettings']['Networks']) == {panel_network}
                assert not any(m['Destination'] == '/app/data' for m in ingress['Mounts'])
                for service in ('local-panel-origin', 'local-panel'):
                    assert command(*compose, 'exec', '-T', service, 'id', '-u') == '10001'
                for host, port in (('frostagent', target_port), ('frostagent', 18080),
                                   (ingress_ip, 8080), (gateway, panel_port),
                                   (desktop_gateway, panel_port)):
                    assert peer_request(peer, host, port) == 0, \
                        f'adapter-network peer reached management at {host}:{port}'
                assert peer_request(panel_peer, ingress_ip, 8080) == 0, \
                    'inter-container connectivity on panel network must be disabled'

            check_isolation()
            created = api('/api/instances', {'name': 'Synthetic local panel smoke'})
            instance_id = created['id']
            assert api(f'/api/instances/{instance_id}/enable', {'enabled': True}).get('success')
            rpc = f'/instances/{instance_id}/frostagent.v1.SettingsService/ListEnvVars'
            assert read(rpc, {})['status'] == 200
            for adapter in ('onebot', 'astrbot'):
                endpoint = f'/instances/{instance_id}/ws/{adapter}?mock=true'
                assert read(endpoint, headers=UPGRADE)['status'] == 101
                assert http_request('127.0.0.1', ws_port, endpoint, headers=UPGRADE)['status'] == 101
                assert peer_request(peer, 'frostagent', 1234, endpoint, UPGRADE) == 101
            assert peer_request(peer, 'frostagent', 1234, '/api/storage') == 404
            print('PASS: host HTML/assets/API/RPC/WebSocket, adapter connectivity and network isolation')

            settings = api('/api/instances/global/backup/settings')
            settings['settings'].update({'MCP_CONTROL_TOKEN': TOKEN, 'MCP_ENFORCE_LOCAL_TOKEN': 'true'})
            assert api('/api/instances/global/import/settings', settings).get('success')
            assert read('/api/instances')['status'] == 401
            assert read(rpc, {})['status'] == 401
            assert read('/api/instances', headers={'Authorization': 'Bearer incorrect-synthetic-token'})['status'] == 401
            assert api('/api/instances', headers=BEARER)['instances'][0]['id'] == instance_id
            assert read(rpc, {}, BEARER)['status'] == 200
            before = api('/api/instances/global/backup/settings', headers=BEARER)
            print('PASS: explicit local token enforcement is not bypassed')

            command(*compose, 'stop', 'local-panel-origin')
            assert container('local-panel'), 'ingress should remain running for the failure-path check'
            assert read('/api/instances', headers=BEARER)['status'] == 0
            command(*compose, 'start', 'local-panel-origin')
            wait_ready(read)
            assert api('/api/instances/global/backup/settings', headers=BEARER) == before
            print('PASS: origin-only stop fails closed and restarts without changing settings')

            command(*compose, 'restart', 'local-panel-origin', 'local-panel')
            wait_ready(read)
            old_app = container('frostagent')
            # Preserve actual allocated host ports across full recreation.
            for service, port in (('local-panel', panel_port), ('frostagent', ws_port)):
                config['services'][service]['ports'][0]['published'] = str(port)
            path.write_text(json.dumps(config), encoding='utf-8')
            command(*compose, 'up', '--detach', '--no-build', '--force-recreate', *SERVICES)
            wait_ready(read)
            assert container('frostagent') != old_app
            check_isolation()
            instances = api('/api/instances', headers=BEARER)['instances']
            assert len(instances) == 1 and instances[0]['id'] == instance_id and instances[0]['enabled']
            assert api('/api/instances/global/backup/settings', headers=BEARER) == before
            print('PASS: restart/recreation rebinds the namespace and preserves instance/settings data')

            updated = copy.deepcopy(before)
            updated['settings']['LISTEN_ADDR'] = '127.0.0.1:8081'
            # A listener change may close this connection before its response is read.
            result = read('/api/instances/global/import/settings', updated, BEARER)
            assert result['status'] in (0, 200), result
            custom = prepare_test_config(render(root, project, target_port=8081), project,
                                         {'local-panel': panel_port, 'frostagent': ws_port})
            path.write_text(json.dumps(custom), encoding='utf-8')
            command(*compose, 'up', '--detach', '--no-build', '--force-recreate', *SERVICES)
            wait_ready(read)
            assert api('/api/instances/global/backup/settings', headers=BEARER) == updated
            check_isolation(target_port=8081)
            print('PASS: explicit target port follows existing SQL listener without rewriting settings')

            command(*compose, 'stop', 'local-panel', 'local-panel-origin')
            assert read('/api/instances', headers=BEARER)['status'] == 0
            endpoint = f'/instances/{instance_id}/ws/onebot?mock=true'
            assert peer_request(peer, 'frostagent', 1234, endpoint, UPGRADE) == 101
            command(*compose, 'up', '--detach', '--no-build', 'local-panel-origin', 'local-panel')
            wait_ready(read)
            assert api('/api/instances', headers=BEARER)['instances'][0]['id'] == instance_id
            print('PASS: disabling/re-enabling the panel leaves the application and data intact')
        except BaseException:
            subprocess.run((*compose, 'logs', '--no-color'), check=False)
            raise
        finally:
            for helper_id in helpers:
                subprocess.run(('docker', 'rm', '--force', helper_id), check=False)
            # Names were checked above and belong only to this generated test project.
            subprocess.run((*compose, 'down', '--volumes'), check=True)
    print('PASS: local panel live smoke on ' + operating_system)
    print('NOT EXECUTED: live Cloudflare authentication, real IM platforms, or LAN client probing')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config-only', action='store_true', help='no Docker daemon or containers required')
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1],
                        help='repository containing the Compose files')
    args = parser.parse_args()
    root = args.root.resolve()
    project = 'frostagent-local-config-check'
    base = render(root, project, local=False)
    validate_config(base, render(root, project))
    validate_config(base, render(root, project, panel_port=18088, target_port=8081),
                    panel_port=18088, target_port=8081)
    print('PASS: opt-in Compose topology, loopback publication, immutable relays and unchanged default/data')
    if args.config_only:
        print('NOT EXECUTED: live networking, application authentication, restart or persistence tests')
    else:
        exercise(root)


if __name__ == '__main__':
    main()
