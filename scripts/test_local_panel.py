"""Unit checks for the local-panel smoke's isolation and cleanup guardrails."""

import copy
import subprocess
import sys
import unittest
from pathlib import Path
from unittest.mock import Mock, call, patch

from docker_local_panel_smoke import (TOKEN, enable_local_token, exercise, http_request, prepare_test_config,
                                     published_ports, validate_config, wait_ready, wait_websocket)


PROJECT = 'frostagent-local-smoke-' + 'a' * 12


def topology():
    base = {
        'services': {'frostagent': {'image': 'frostagent:local', 'ports': [
            {'host_ip': '127.0.0.1', 'published': '1234', 'target': 1234}],
            'volumes': [{'type': 'volume', 'source': 'frostagent-data', 'target': '/app/data'}]}},
        'volumes': {'frostagent-data': {'name': PROJECT + '_frostagent-data'}},
        'networks': {'default': {'name': PROJECT + '_default'}},
    }
    local = copy.deepcopy(base)
    relay = {'image': 'alpine/socat:1.8.1.3@sha256:' + 'a' * 64,
             'user': '10001:10001', 'read_only': True, 'init': True,
             'cap_drop': ['ALL'], 'security_opt': ['no-new-privileges:true'],
             'volumes': [{'source': 'local-panel-socket', 'target': '/run/frostagent-panel',
                          'volume': {'nocopy': True}}]}
    origin = copy.deepcopy(relay)
    origin.update({'network_mode': 'service:frostagent',
                   'depends_on': {'frostagent': {'restart': True}},
                   'entrypoint': ['/bin/sh', '-ec'],
                   'environment': {'FROSTAGENT_PANEL_TARGET_PORT': '8080'},
                   'command': ['exec socat UNIX-LISTEN:/run/frostagent-panel/management.sock,'
                               'unlink-early,mode=0600,fork "TCP4:127.0.0.1:$port"']})
    ingress = copy.deepcopy(relay)
    ingress.update({'networks': {'local-panel': {}},
                    'depends_on': {'local-panel-origin': {'restart': True}},
                    'command': ['TCP4-LISTEN:8080,bind=0.0.0.0,reuseaddr,fork',
                                'UNIX-CONNECT:/run/frostagent-panel/management.sock'],
                    'ports': [{'host_ip': '127.0.0.1', 'published': '8088',
                               'target': 8080, 'protocol': 'tcp'}]})
    ingress['volumes'][0]['read_only'] = True
    local['services'].update({'local-panel-origin': origin, 'local-panel': ingress})
    local['networks']['local-panel'] = {
        'name': PROJECT + '_local-panel', 'driver': 'bridge',
        'driver_opts': {'com.docker.network.bridge.enable_icc': 'false'}}
    local['volumes']['local-panel-socket'] = {
        'name': PROJECT + '_local-panel-socket', 'driver': 'local', 'driver_opts': {
            'type': 'tmpfs', 'device': 'tmpfs',
            'o': 'size=1m,uid=10001,gid=10001,mode=0700,nosuid,nodev,noexec'}}
    return base, local


class LocalPanelGuardTests(unittest.TestCase):
    def test_live_smoke_refuses_optimized_python_before_accessing_docker(self):
        result = subprocess.run(
            [sys.executable, '-B', '-O', '-c',
             'from docker_local_panel_smoke import exercise; exercise(None)'],
            cwd=Path(__file__).resolve().parent, capture_output=True, text=True, timeout=10,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Live smoke requires Python assertions', result.stderr)

    def test_expected_topology_is_accepted(self):
        validate_config(*topology())

    def test_host_wildcard_publication_is_rejected(self):
        base, local = topology()
        local['services']['local-panel']['ports'][0]['host_ip'] = '0.0.0.0'
        with self.assertRaises(AssertionError):
            validate_config(base, local)

    def test_ingress_cannot_join_adapter_network(self):
        base, local = topology()
        local['services']['local-panel']['networks']['default'] = {}
        with self.assertRaises(AssertionError):
            validate_config(base, local)

    def test_origin_cannot_publish_management_tcp(self):
        base, local = topology()
        local['services']['local-panel-origin']['ports'] = [{'target': 8080}]
        with self.assertRaises(AssertionError):
            validate_config(base, local)

    def test_origin_cannot_listen_on_adapter_network(self):
        base, local = topology()
        local['services']['local-panel-origin']['command'][0] += ' TCP4-LISTEN:18080'
        with self.assertRaises(AssertionError):
            validate_config(base, local)

    def test_internal_only_network_cannot_disable_port_publication(self):
        base, local = topology()
        local['networks']['local-panel']['internal'] = True
        with self.assertRaises(AssertionError):
            validate_config(base, local)

    def test_adapter_and_data_configuration_are_unchanged(self):
        for attribute in ('ports', 'volumes'):
            base, local = topology()
            local['services']['frostagent'][attribute] = []
            with self.subTest(attribute=attribute), self.assertRaises((AssertionError, ValueError)):
                validate_config(base, local)

    def test_inter_container_connectivity_cannot_be_enabled(self):
        base, local = topology()
        local['networks']['local-panel']['driver_opts']['com.docker.network.bridge.enable_icc'] = 'true'
        with self.assertRaises(AssertionError):
            validate_config(base, local)

    def test_socket_is_read_only_at_ingress(self):
        base, local = topology()
        local['services']['local-panel']['volumes'][0]['read_only'] = False
        with self.assertRaises(AssertionError):
            validate_config(base, local)

    def test_smoke_randomizes_published_ports(self):
        _, local = topology()
        prepared = prepare_test_config(local, PROJECT)
        for service in prepared['services'].values():
            self.assertEqual(service['restart'], 'no')
            for port in service.get('ports', []):
                self.assertEqual(port['published'], '0')

    def test_smoke_never_uses_foreign_or_external_resources(self):
        for kind in ('volumes', 'networks'):
            for change in ({'name': 'existing-user-resource'}, {'external': True}):
                _, local = topology()
                next(iter(local[kind].values())).update(change)
                with self.subTest(kind=kind, change=change), self.assertRaises((AssertionError, ValueError)):
                    prepare_test_config(local, PROJECT)

    def assert_unsafe_storage_rejected_before_docker(self, local):
        with self.assertRaises(ValueError):
            prepare_test_config(copy.deepcopy(local), PROJECT)
        base = copy.deepcopy(local)
        base['services'] = {'frostagent': base['services']['frostagent']}
        with self.assertRaises(ValueError):
            validate_config(base, local)
        with patch('docker_local_panel_smoke.render', return_value=local), \
                patch('docker_local_panel_smoke.command') as docker:
            with self.assertRaises(ValueError):
                exercise(None)
            docker.assert_not_called()

    def test_app_host_bind_is_rejected_before_docker(self):
        _, local = topology()
        local['services']['frostagent']['volumes'] = [
            {'type': 'bind', 'source': '/synthetic-existing-data', 'target': '/app/data'}]
        self.assert_unsafe_storage_rejected_before_docker(local)

    def test_extra_app_mount_is_rejected_before_docker(self):
        _, local = topology()
        local['services']['frostagent']['volumes'].append(
            {'type': 'bind', 'source': '/synthetic-existing-config', 'target': '/app/config'})
        self.assert_unsafe_storage_rejected_before_docker(local)

    def test_replaced_data_source_is_rejected_before_docker(self):
        _, local = topology()
        local['services']['frostagent']['volumes'][0]['source'] = 'existing-user-volume'
        self.assert_unsafe_storage_rejected_before_docker(local)

    def test_local_driver_bind_options_are_rejected_before_docker(self):
        _, local = topology()
        local['volumes']['frostagent-data'].update({
            'driver': 'local', 'driver_opts': {
                'type': 'none', 'device': '/synthetic-existing-data', 'o': 'bind'}})
        self.assert_unsafe_storage_rejected_before_docker(local)

    def test_nonlocal_storage_driver_is_rejected_before_docker(self):
        _, local = topology()
        local['volumes']['frostagent-data']['driver'] = 'synthetic-network-volume-plugin'
        self.assert_unsafe_storage_rejected_before_docker(local)

    def test_explicit_local_driver_without_options_is_accepted(self):
        _, local = topology()
        local['volumes']['frostagent-data']['driver'] = 'local'
        prepare_test_config(local, PROJECT)

    def test_cleanup_project_must_be_generated(self):
        for project in ('frostagent', 'frostagent-local-smoke-', 'production'):
            with self.subTest(project=project), self.assertRaises(AssertionError):
                prepare_test_config(topology()[1], project)

    def test_closed_or_timed_out_transport_is_not_an_http_response(self):
        for error in (ConnectionRefusedError(), TimeoutError(), ConnectionResetError()):
            with self.subTest(error=type(error).__name__):
                with patch('docker_local_panel_smoke.socket.create_connection', side_effect=error):
                    self.assertEqual(http_request('127.0.0.1', 8088), {'status': 0, 'body': ''})

    def test_token_setup_uses_credential_rpc_not_settings_import(self):
        exported = {'format_version': 1, 'settings': {'MCP_CONTROL_TOKEN': ''}}
        api = Mock(side_effect=[{'success': True}, exported, {'success': True}])
        enable_local_token(api, 'a1b2c3d4')
        self.assertEqual(api.call_args_list, [
            call('/instances/a1b2c3d4/frostagent.v1.SettingsService/UpdateEnvVar',
                 {'key': 'MCP_CONTROL_TOKEN', 'value': TOKEN}),
            call('/api/instances/global/backup/settings'),
            call('/api/instances/global/import/settings', {
                'format_version': 1, 'settings': {
                    'MCP_CONTROL_TOKEN': '', 'MCP_ENFORCE_LOCAL_TOKEN': 'true'}}),
        ])

    def test_published_ports_are_rediscovered_after_restart(self):
        with patch('docker_local_panel_smoke.command', side_effect=[
            '127.0.0.1:31001', '127.0.0.1:31002', '127.0.0.1:32001', '127.0.0.1:31002',
        ]), patch('builtins.print'):
            self.assertEqual(published_ports(('docker', 'compose')), (31001, 31002))
            self.assertEqual(published_ports(('docker', 'compose')), (32001, 31002))

    def test_ready_timeout_reports_last_http_status(self):
        with patch('docker_local_panel_smoke.time.monotonic', side_effect=[0, 0, 61]), \
                patch('docker_local_panel_smoke.time.sleep'):
            with self.assertRaisesRegex(AssertionError, 'last HTTP status: 503'):
                wait_ready(lambda _: {'status': 503, 'body': ''})

    def test_adapter_wait_requires_successful_upgrade(self):
        read = Mock(side_effect=[{'status': 0}, {'status': 101}])
        with patch('docker_local_panel_smoke.time.sleep'):
            wait_websocket(read)
        self.assertEqual(read.call_count, 2)

    def test_adapter_wait_timeout_reports_last_status(self):
        with patch('docker_local_panel_smoke.time.monotonic', side_effect=[0, 0, 61]), \
                patch('docker_local_panel_smoke.time.sleep'):
            with self.assertRaisesRegex(AssertionError, 'last HTTP status: 404'):
                wait_websocket(lambda: {'status': 404})


if __name__ == '__main__':
    unittest.main()
