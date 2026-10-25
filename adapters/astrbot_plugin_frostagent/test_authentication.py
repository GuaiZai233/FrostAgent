from __future__ import annotations

import asyncio
import os
import unittest
from unittest.mock import Mock, patch

from adapters.astrbot_plugin_frostagent import test_main


adapter = test_main.adapter_module
TOKEN = "synthetic-adapter-token-for-tests-only"
URL = "wss://frostagent.invalid/instances/a1b2c3d4/ws/astrbot"


def authenticated_settings(**overrides):
    config = {"ws_url": URL, "computer_use_adapter_token": TOKEN}
    config.update(overrides)
    return adapter.load_settings(config)


class AuthenticationSettingsTest(unittest.TestCase):
    def test_optional_token_preserves_ordinary_private_network_transport(self):
        with patch.dict(os.environ, {}, clear=True):
            settings = adapter.load_settings({
                "ws_url": "ws://10.23.45.67/instances/a1b2c3d4/ws/astrbot"
            })
        self.assertEqual(settings.computer_use_adapter_token, "")

    def test_token_uses_config_then_environment_and_is_hidden_from_repr(self):
        environment_token = "synthetic-environment-token-for-tests"
        with patch.dict(os.environ, {
            "FROSTAGENT_COMPUTER_USE_ADAPTER_TOKEN": environment_token
        }, clear=True):
            configured = authenticated_settings()
            inherited = adapter.load_settings({"ws_url": URL})
            inactive = adapter._load_settings_unvalidated({"ws_url": "legacy"})
        self.assertEqual(configured.computer_use_adapter_token, TOKEN)
        self.assertEqual(inherited.computer_use_adapter_token, environment_token)
        self.assertEqual(inactive.computer_use_adapter_token, environment_token)
        for settings in (configured, inherited, inactive):
            self.assertNotIn(settings.computer_use_adapter_token, repr(settings))

    def test_authenticated_plaintext_accepts_only_numeric_loopback(self):
        for host in ("127.0.0.1", "127.0.0.9", "[::1]"):
            with self.subTest(host=host):
                settings = authenticated_settings(
                    ws_url=f"ws://{host}:8080/instances/a1b2c3d4/ws/astrbot"
                )
                self.assertEqual(settings.computer_use_adapter_token, TOKEN)
        for host in ("localhost", "10.23.45.67", "frostagent.invalid", "127.0.0.1.invalid", "[::]"):
            with self.subTest(host=host), self.assertRaisesRegex(
                adapter.WSURLConfigurationError, "numeric loopback"
            ):
                authenticated_settings(
                    ws_url=f"ws://{host}:8080/instances/a1b2c3d4/ws/astrbot"
                )

    def test_authenticated_url_cannot_contain_inline_secrets(self):
        for url in (
            URL + "?token=synthetic-query-secret",
            URL + "#synthetic-fragment",
            URL.replace("://", "://synthetic-user@"),
            URL.replace("://", "://synthetic-user:synthetic-password@"),
        ):
            with self.subTest(url=url), self.assertRaises(adapter.WSURLConfigurationError) as raised:
                authenticated_settings(ws_url=url)
            self.assertNotIn("synthetic-query-secret", str(raised.exception))
            self.assertNotIn("synthetic-password", str(raised.exception))

    def test_invalid_token_never_appears_in_configuration_error(self):
        for token in ("synthetic-short", "x" * 257, TOKEN + "\r\nInjected: value", TOKEN + "/"):
            with self.subTest(length=len(token)), self.assertRaises(
                adapter.WSURLConfigurationError
            ) as raised:
                authenticated_settings(computer_use_adapter_token=token)
            self.assertNotIn(token, str(raised.exception))

    def test_invalid_authenticated_configuration_does_not_start_worker(self):
        with patch.object(asyncio, "create_task") as create_task:
            plugin = adapter.FrostAgentAdapter(object(), {
                "ws_url": "ws://10.23.45.67/instances/a1b2c3d4/ws/astrbot",
                "computer_use_adapter_token": TOKEN,
            })
        create_task.assert_not_called()
        self.assertIsNotNone(plugin._configuration_error)
        self.assertNotIn(TOKEN, repr(plugin.settings))


class ModernConnection:
    def process_redirect(self, error):
        return "wss://another.invalid/instances/a1b2c3d4/ws/astrbot"


class LegacyConnection:
    def handle_redirect(self, uri):
        self.redirect_uri = uri


class AuthenticationHandshakeTest(unittest.TestCase):
    def test_modern_headers_are_handshake_only_and_disable_proxy_and_logging(self):
        received = {}
        connection = ModernConnection()

        def connect(uri, *, additional_headers=None, logger=None, proxy=True):
            received.update(uri=uri, headers=additional_headers, logger=logger, proxy=proxy)
            return connection

        with patch.object(adapter.websockets, "connect", connect, create=True):
            self.assertIs(adapter.open_websocket(authenticated_settings()), connection)
        self.assertEqual(received["uri"], URL)
        self.assertNotIn(TOKEN, received["uri"])
        self.assertEqual(received["headers"], {"Authorization": "Bearer " + TOKEN})
        self.assertIsNone(received["proxy"])
        self.assertTrue(received["logger"].disabled)
        self.assertFalse(received["logger"].propagate)
        failure = RuntimeError("synthetic redirect response")
        self.assertIs(connection.process_redirect(failure), failure)

    def test_legacy_api_uses_explicit_extra_headers_and_rejects_redirect(self):
        received = {}
        connection = LegacyConnection()

        def connect(uri, *, extra_headers=None, logger=None):
            received.update(uri=uri, headers=extra_headers, logger=logger)
            return connection

        with patch.object(adapter.websockets, "connect", connect, create=True):
            self.assertIs(adapter.open_websocket(authenticated_settings()), connection)
        self.assertEqual(received["headers"], {"Authorization": "Bearer " + TOKEN})
        self.assertTrue(received["logger"].disabled)
        with self.assertRaises(adapter.WSURLConfigurationError):
            connection.handle_redirect("wss://another.invalid")
        self.assertFalse(hasattr(connection, "redirect_uri"))

    def test_missing_signature_never_attempts_network_or_parameter_fallback(self):
        called = []

        def connect(uri, **kwargs):
            called.append((uri, kwargs))

        with patch.object(adapter.websockets, "connect", connect, create=True):
            with self.assertRaises(adapter.WSURLConfigurationError):
                adapter.open_websocket(authenticated_settings())
        self.assertEqual(called, [])

    def test_missing_redirect_control_fails_before_awaiting_connection(self):
        opened = []

        class UnrecognizedConnection:
            async def __aenter__(self):
                opened.append(True)
                return self

        def connect(uri, *, additional_headers=None, logger=None):
            return UnrecognizedConnection()

        with patch.object(adapter.websockets, "connect", connect, create=True):
            with self.assertRaisesRegex(adapter.WSURLConfigurationError, "redirects"):
                adapter.open_websocket(authenticated_settings())
        self.assertEqual(opened, [])

    def test_unconfigured_connection_uses_existing_call_without_headers(self):
        with patch.dict(os.environ, {}, clear=True):
            settings = adapter.load_settings({"ws_url": URL})
        connect = Mock(return_value=object())
        with patch.object(adapter.websockets, "connect", connect, create=True):
            adapter.open_websocket(settings)
        connect.assert_called_once_with(URL)


class AuthenticationLoggingTest(unittest.IsolatedAsyncioTestCase):
    async def test_connection_errors_cannot_echo_authentication_header(self):
        client = adapter.FrostAgentWSClient(authenticated_settings(), object())
        calls = []

        class FailingConnection(ModernConnection):
            async def __aenter__(self):
                client._running = False
                raise RuntimeError("Authorization: Bearer " + TOKEN)

            async def __aexit__(self, *args):
                return False

        def connect(uri, *, additional_headers=None, logger=None, proxy=True):
            calls.append(additional_headers)
            return FailingConnection()

        client._running = True
        with patch.object(adapter.websockets, "connect", connect, create=True), patch.object(
            adapter, "logger"
        ) as logger:
            await client._connection_loop()
        self.assertEqual(calls, [{"Authorization": "Bearer " + TOKEN}])
        self.assertNotIn(TOKEN, str(logger.mock_calls))
        self.assertNotIn("Authorization", str(logger.mock_calls))
        self.assertIn("详细信息已隐藏", str(logger.mock_calls))
        self.assertIsNone(client.ws)

    async def test_malformed_authenticated_frame_is_not_logged(self):
        client = adapter.FrostAgentWSClient(authenticated_settings(), object())
        with patch.object(adapter, "logger") as logger:
            await client._handle_incoming_action(TOKEN + " not-json")
        self.assertNotIn(TOKEN, str(logger.mock_calls))
        self.assertIn("内容已隐藏", str(logger.mock_calls))

    async def test_unconfigured_error_keeps_existing_diagnostics(self):
        with patch.dict(os.environ, {}, clear=True):
            settings = adapter.load_settings({"ws_url": URL})
        self.assertEqual(
            adapter._connection_error(settings, RuntimeError("synthetic ordinary error")),
            "synthetic ordinary error",
        )


if __name__ == "__main__":
    unittest.main()
