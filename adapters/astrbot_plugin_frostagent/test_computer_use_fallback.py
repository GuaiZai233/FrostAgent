from __future__ import annotations

import asyncio
import unittest
from types import SimpleNamespace
from unittest.mock import patch

from adapters.astrbot_plugin_frostagent import test_main


adapter_module = test_main.adapter_module
COMMAND = "/computer start synthetic-project synthetic-instruction"


class ComputerUseFallbackTest(unittest.IsolatedAsyncioTestCase):
    def make_adapter(self, *, configuration_error=None, forward_all=True, actions=None):
        adapter = object.__new__(test_main.FrostAgentAdapter)
        adapter._configuration_error = configuration_error
        adapter.settings = SimpleNamespace(
            forward_all_group_messages=forward_all,
            http_base_url="http://127.0.0.1:8080",
        )
        adapter.client = test_main.FakeClient(actions)
        return adapter

    async def forward(self, adapter, event):
        return [result async for result in adapter.forward_to_frostagent(event)]

    async def test_suppresses_before_payload_can_await_attachment_io(self):
        adapter = self.make_adapter()
        event = test_main.FakeEvent(content=COMMAND)
        build_payload = adapter_module.build_frostagent_payload

        async def check_before_build(current_event):
            self.assertEqual(current_event.should_call_llm_calls, [True])
            return await build_payload(current_event)

        with patch.object(adapter_module, "build_frostagent_payload", check_before_build):
            self.assertEqual(await self.forward(adapter, event), [])
        self.assertEqual(event.should_call_llm_calls, [True])

    async def test_disconnected_command_never_falls_back_to_default_llm(self):
        adapter = self.make_adapter()
        adapter.client.connected = False
        event = test_main.FakeEvent(content=COMMAND)

        self.assertEqual(await self.forward(adapter, event), [])

        self.assertEqual(event.should_call_llm_calls, [True])
        self.assertEqual(adapter.client.sent_events, [])
        self.assertIsNone(adapter.client.queue)

    async def test_send_failure_is_already_suppressed(self):
        adapter = self.make_adapter()
        event = test_main.FakeEvent(content=COMMAND)

        async def fail_send(_payload):
            self.assertEqual(event.should_call_llm_calls, [True])
            raise ConnectionError("synthetic connection failure")

        with patch.object(adapter.client, "send_event", fail_send):
            with self.assertRaisesRegex(ConnectionError, "synthetic connection failure"):
                await self.forward(adapter, event)
        self.assertEqual(event.should_call_llm_calls, [True])
        self.assertIsNone(adapter.client.queue)

    async def test_timed_out_command_remains_suppressed(self):
        adapter = self.make_adapter()
        event = test_main.FakeEvent(content=COMMAND)

        async def time_out(waiter, *, timeout):
            waiter.close()
            self.assertEqual(event.should_call_llm_calls, [True])
            raise asyncio.TimeoutError

        with patch.object(adapter_module.asyncio, "wait_for", time_out):
            self.assertEqual(await self.forward(adapter, event), [])
        self.assertEqual(event.should_call_llm_calls, [True])
        self.assertIsNone(adapter.client.queue)

    async def test_paused_configuration_suppresses_without_building_or_sending(self):
        adapter = self.make_adapter(configuration_error="synthetic invalid configuration")
        event = test_main.FakeEvent(content=COMMAND)

        with patch.object(adapter_module, "build_frostagent_payload") as build_payload:
            self.assertEqual(await self.forward(adapter, event), [])
        build_payload.assert_not_called()
        self.assertEqual(event.should_call_llm_calls, [True])
        self.assertEqual(adapter.client.sent_events, [])

    async def test_filtered_group_command_never_falls_back_or_forwards(self):
        adapter = self.make_adapter(forward_all=False)
        event = test_main.FakeEvent(group_id="synthetic-group", content=COMMAND)

        self.assertEqual(await self.forward(adapter, event), [])

        self.assertEqual(event.should_call_llm_calls, [True])
        self.assertEqual(adapter.client.sent_events, [])

    async def test_command_reply_and_backend_suppression_do_not_repeat_local_suppression(self):
        for actions, expected in (
            ([{"action": "noop", "suppress_llm": True}], []),
            ([{"action": "reply", "content": "synthetic result"}],
             [{"content": "synthetic result"}]),
        ):
            with self.subTest(actions=actions):
                adapter = self.make_adapter(actions=actions)
                event = test_main.FakeEvent(content=COMMAND)
                self.assertEqual(await self.forward(adapter, event), expected)
                self.assertEqual(event.should_call_llm_calls, [True])

    async def test_only_exact_first_command_token_is_suppressed(self):
        for text, suppressed in (
            ("/computer", True),
            ("  /computer\tstatus", True),
            ("\n/computer\nstatus", True),
            ("\u3000/computer\u00a0status", True),
            ("/computerized status", False),
            ("/computer/status", False),
            ("/computer: status", False),
            ("/Computer status", False),
            ("/computer\x1cstatus", False),
            ("\x1f/computer status", False),
            ("please run /computer status", False),
            ("", False),
        ):
            with self.subTest(text=text):
                adapter = self.make_adapter(configuration_error="synthetic invalid configuration")
                event = test_main.FakeEvent(content=text)
                self.assertEqual(await self.forward(adapter, event), [])
                self.assertEqual(event.should_call_llm_calls, [True] if suppressed else [])

    async def test_ordinary_messages_preserve_fallback_on_all_nonresponse_paths(self):
        for state in ("paused", "disconnected", "timeout", "noop"):
            with self.subTest(state=state):
                adapter = self.make_adapter(
                    configuration_error="synthetic invalid configuration" if state == "paused" else None,
                )
                adapter.client.connected = state != "disconnected"
                event = test_main.FakeEvent(content="ordinary synthetic message")

                async def time_out(waiter, *, timeout):
                    waiter.close()
                    raise asyncio.TimeoutError

                if state == "timeout":
                    with patch.object(adapter_module.asyncio, "wait_for", time_out):
                        self.assertEqual(await self.forward(adapter, event), [])
                else:
                    self.assertEqual(await self.forward(adapter, event), [])
                self.assertEqual(event.should_call_llm_calls, [])


if __name__ == "__main__":
    unittest.main()
