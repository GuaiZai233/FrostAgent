"""Regression coverage for the live smoke's WebSocket boundary assertions."""

import unittest
from unittest.mock import Mock

from docker_ws_smoke import check_adapter_websocket, check_management_websocket


class WebSocketSmokeTests(unittest.TestCase):
    def test_adapter_accepts_real_connection_and_rejects_both_mock_markers(self):
        for adapter in ('onebot', 'astrbot'):
            endpoint = '/instances/synthetic-instance/ws/' + adapter
            read = Mock(side_effect=[{'status': status} for status in (101, 403, 403, 403, 403)])
            check_adapter_websocket(read, endpoint)
            self.assertEqual(read.call_args_list[0].args, (endpoint,))
            self.assertEqual([call.args[0] for call in read.call_args_list[1:]],
                             [endpoint + '?mock=true'] * 2 + [endpoint] * 2)
            for index in (1, 3):
                self.assertNotIn('Origin', read.call_args_list[index].kwargs['headers'])
            for index in (3, 4):
                self.assertEqual(read.call_args_list[index].kwargs['headers']['X-Mock-Adapter'], 'true')

    def test_adapter_never_accepts_a_successful_mock_upgrade(self):
        for position in range(1, 5):
            statuses = [101, 403, 403, 403, 403]
            statuses[position] = 101
            with self.subTest(position=position), self.assertRaisesRegex(AssertionError, 'reject mock'):
                check_adapter_websocket(Mock(side_effect=[{'status': status} for status in statuses]), '/ws')

    def test_broken_ordinary_adapter_is_not_mistaken_for_isolation(self):
        for status in (0, 401, 403, 404, 503):
            with self.subTest(status=status), self.assertRaisesRegex(AssertionError, 'ordinary adapter'):
                check_adapter_websocket(Mock(return_value={'status': status}), '/ws')

    def test_management_preserves_trusted_connector_direct_chat(self):
        read = Mock(return_value={'status': 101})
        check_management_websocket(read, '/ws')
        self.assertEqual(read.call_count, 2)
        for call in read.call_args_list:
            self.assertNotIn('Origin', call.kwargs['headers'])
            self.assertNotIn('Authorization', call.kwargs['headers'])

    def test_management_explicit_token_policy_checks_each_mock_marker(self):
        read = Mock(side_effect=[{'status': status} for status in (401, 401, 101) * 2])
        check_management_websocket(read, '/ws', token='synthetic-control-token')
        for index in (2, 5):
            self.assertEqual(read.call_args_list[index].kwargs['headers']['Authorization'],
                             'Bearer synthetic-control-token')

    def test_management_rejects_missing_or_wrong_token_bypass(self):
        for position in (0, 1, 3, 4):
            statuses = [401, 401, 101] * 2
            statuses[position] = 101
            with self.subTest(position=position), self.assertRaisesRegex(AssertionError, 'token required'):
                check_management_websocket(Mock(side_effect=[{'status': status} for status in statuses]),
                                           '/ws', token='synthetic-control-token')

    def test_management_must_accept_valid_credentials(self):
        for status in (0, 401, 403, 404, 503):
            with self.subTest(status=status), self.assertRaisesRegex(AssertionError, 'Direct Chat'):
                check_management_websocket(Mock(side_effect=[{'status': code} for code in (401, 401, status)]),
                                           '/ws', token='synthetic-control-token')


if __name__ == '__main__':
    unittest.main()
