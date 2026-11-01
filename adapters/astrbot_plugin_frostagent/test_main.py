from __future__ import annotations

import asyncio
import base64
import importlib
import json
import os
import sys
import types
import unittest
from types import SimpleNamespace
from typing import Any
from unittest.mock import patch


class _Logger:
    def __getattr__(self, _name: str) -> Any:
        return lambda *_args, **_kwargs: None


class _EventMessageType:
    ALL = object()


class _Filter:
    EventMessageType = _EventMessageType

    @staticmethod
    def event_message_type(_event_type: object):
        return lambda handler: handler


class _Star:
    def __init__(self, _context: object):
        pass


class _MessageChain:
    def __init__(self, chain: list[object]):
        self.chain = chain


class Plain:
    def __init__(self, text: str):
        self.text = text


class At:
    def __init__(self, qq: str):
        self.qq = qq


class Image:
    def __init__(self, url: str):
        self.url = url

    async def convert_to_base64(self) -> str:
        return base64.b64encode(self.url.encode("utf-8")).decode("ascii")


class Reply:
    def __init__(self, id: str = "", **kwargs):
        self.id = id


def _register(*_args: object, **_kwargs: object):
    return lambda cls: cls


astrbot_module = types.ModuleType("astrbot")
api_module = types.ModuleType("astrbot.api")
event_module = types.ModuleType("astrbot.api.event")
star_module = types.ModuleType("astrbot.api.star")
components_module = types.ModuleType("astrbot.api.message_components")
api_module.logger = _Logger()
event_module.AstrMessageEvent = object
event_module.MessageChain = _MessageChain
event_module.filter = _Filter()
star_module.Context = object
star_module.Star = _Star
star_module.register = _register
components_module.At = At
components_module.Image = Image
components_module.Plain = Plain
components_module.Reply = Reply
astrbot_module.api = api_module
api_module.event = event_module
api_module.star = star_module
api_module.message_components = components_module
sys.modules["astrbot"] = astrbot_module
sys.modules["astrbot.api"] = api_module
sys.modules["astrbot.api.event"] = event_module
sys.modules["astrbot.api.star"] = star_module
sys.modules["astrbot.api.message_components"] = components_module
sys.modules["websockets"] = types.ModuleType("websockets")

adapter_module = importlib.import_module(
    "adapters.astrbot_plugin_frostagent.main"
)
FrostAgentAdapter = adapter_module.FrostAgentAdapter
FrostAgentWSClient = adapter_module.FrostAgentWSClient
Settings = adapter_module.Settings
load_settings = adapter_module.load_settings


class SettingsTest(unittest.TestCase):
    def test_ws_url_is_required_without_legacy_default(self):
        with patch.dict(os.environ, {}, clear=True):
            with self.assertRaisesRegex(ValueError, "ws_url is required"):
                load_settings({})

    def test_legacy_unscoped_ws_url_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "/instances/<instance-id>"):
            load_settings({"ws_url": "ws://127.0.0.1:1234/ws/astrbot"})

    def test_instance_scoped_ws_url_is_accepted(self):
        settings = load_settings(
            {
                "ws_url": (
                    "ws://127.0.0.1:1234/instances/a1b2c3d4/ws/astrbot"
                )
            }
        )
        self.assertEqual(
            settings.ws_url,
            "ws://127.0.0.1:1234/instances/a1b2c3d4/ws/astrbot",
        )

    def test_plugin_keeps_loading_with_legacy_ws_url(self):
        with patch.object(asyncio, "create_task") as create_task:
            adapter = FrostAgentAdapter(
                object(),
                {"ws_url": "ws://127.0.0.1:1234/ws/astrbot"},
            )
        self.assertIn("/instances/<instance-id>", adapter._configuration_error)
        self.assertEqual(adapter.settings.ws_url, "ws://127.0.0.1:1234/ws/astrbot")
        self.assertIsNone(adapter._init_task)
        create_task.assert_not_called()

    def test_plugin_starts_normally_with_instance_scoped_ws_url(self):
        created_task = object()

        def capture_task(coroutine):
            coroutine.close()
            return created_task

        with patch.object(asyncio, "create_task", side_effect=capture_task) as create_task:
            adapter = FrostAgentAdapter(
                object(),
                {"ws_url": "ws://127.0.0.1:1234/instances/a1b2c3d4/ws/astrbot"},
            )
        self.assertIsNone(adapter._configuration_error)
        self.assertIs(adapter._init_task, created_task)
        create_task.assert_called_once()


class FakeEvent:
    def __init__(
        self,
        *,
        group_id: str = "",
        content: str = "",
        is_wake: bool = False,
        mention_bot: bool = False,
        image_url: str = "",
        sender_id: str = "user_test",
    ):
        components = [At("bot_self")] if mention_bot else []
        if content:
            components.append(Plain(content))
        if image_url:
            components.append(Image(image_url))
        self.platform = "test"
        self.is_at_or_wake_command = is_wake
        self.call_llm = False
        self.should_call_llm_calls: list[bool] = []
        self.sent_chains: list[_MessageChain] = []
        self.message_obj = SimpleNamespace(
            message_id="msg_test",
            message=components,
            sender=SimpleNamespace(nickname="测试用户", card=""),
            group=SimpleNamespace(group_name="测试群"),
        )
        self._group_id = group_id
        self._content = content
        self._sender_id = sender_id

    def get_sender_id(self) -> str:
        return self._sender_id

    def get_group_id(self) -> str:
        return self._group_id

    def get_message_str(self) -> str:
        return self._content

    def get_self_id(self) -> str:
        return "bot_self"

    def plain_result(self, content: str) -> dict[str, str]:
        return {"content": content}

    def should_call_llm(self, call_llm: bool) -> None:
        self.call_llm = call_llm
        self.should_call_llm_calls.append(call_llm)

    async def send(self, chain: _MessageChain) -> None:
        self.sent_chains.append(chain)


class FakeClient:
    def __init__(self, actions: list[dict[str, Any]] | None = None):
        self.sent_events: list[dict[str, Any]] = []
        self.connected = True
        self.queue: asyncio.Queue | None = None
        self.actions = actions or [{"action": "noop"}]
        self.delivery_results: list[tuple[str, bool, str]] = []

    def register_waiter(self, _msg_id: str) -> asyncio.Queue:
        self.queue = asyncio.Queue()
        return self.queue

    def unregister_waiter(self, _msg_id: str) -> None:
        self.queue = None

    async def send_event(self, payload: dict[str, Any]) -> bool:
        if not self.connected:
            return False
        self.sent_events.append(payload)
        assert self.queue is not None
        for action in self.actions:
            await self.queue.put(action)
        return True

    async def send_delivery_result(self, delivery_id: str, delivered: bool, error: str = "") -> None:
        self.delivery_results.append((delivery_id, delivered, error))


class QuoteLookupRoutingTest(unittest.IsolatedAsyncioTestCase):
    async def test_backend_lookup_reaches_the_waiting_event(self):
        client = FrostAgentWSClient(SimpleNamespace(), None)
        queue = client.register_waiter("msg_test")
        request = {
            "type": "quote_lookup", "echo": "quote_lookup_1",
            "event_message_id": "msg_test", "message_id": "44",
        }
        await client._handle_incoming_action(json.dumps(request))
        self.assertEqual(await asyncio.wait_for(queue.get(), 0.1), request)


class ForwardToFrostAgentTest(unittest.IsolatedAsyncioTestCase):
    async def test_quote_lookup_capability_requires_qq_bot_action(self):
        event = FakeEvent(content="你觉得呢？")
        event.message_obj.message = [Reply("44")]
        event.platform = "aiocqhttp"
        event.bot = SimpleNamespace(call_action=lambda **_kwargs: None)
        payload = await adapter_module.build_frostagent_payload(event)
        self.assertTrue(payload["metadata"]["quote_lookup_supported"])

        event.bot = None
        payload = await adapter_module.build_frostagent_payload(event)
        self.assertNotIn("quote_lookup_supported", payload["metadata"])

        event.platform = "telegram"
        event.bot = SimpleNamespace(call_action=lambda **_kwargs: None)
        payload = await adapter_module.build_frostagent_payload(event)
        self.assertNotIn("quote_lookup_supported", payload["metadata"])

    async def test_quote_lookup_request_uses_current_qq_event(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(
            forward_all_group_messages=True,
            http_base_url="http://frostagent:8080",
        )
        adapter.client = FakeClient([
            {
                "type": "quote_lookup", "echo": "quote_lookup_1",
                "event_message_id": "msg_test", "message_id": "44",
            },
            {"action": "noop"},
        ])
        event = FakeEvent(content="你觉得呢？")
        event.platform = "aiocqhttp"
        calls = []

        async def call_action(**kwargs):
            calls.append(kwargs)
            return {"status": "ok", "retcode": 0, "data": {
                "message_id": 44, "message_type": "private",
                "user_id": "user_test",
                "message": [{"type": "text", "data": {"text": "同意"}}],
            }}

        event.bot = SimpleNamespace(call_action=call_action)
        results = [item async for item in adapter.forward_to_frostagent(event)]
        self.assertEqual(results, [])
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["action"], "get_msg")
        self.assertEqual(calls[0]["message_id"], 44)
        self.assertEqual(adapter.client.sent_events[1]["type"], "quote_lookup_result")
        self.assertEqual(adapter.client.sent_events[1]["echo"], "quote_lookup_1")
        self.assertEqual(adapter.client.sent_events[1]["data"]["message_id"], 44)

    async def test_mixed_sticker_fetch_failure_reports_rejection(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(forward_all_group_messages=True, http_base_url="http://frostagent:8080")
        adapter.client = FakeClient([
            {"action": "send_message", "is_intermediate": True, "delivery_id": "delivery_test",
             "messages": [{"type": "plain", "text": "hello"},
                          {"type": "image", "url": "/api/sticker/missing/image", "is_sticker": True}]},
            {"action": "noop"},
        ])
        event = FakeEvent(content="hello")
        with patch.object(adapter_module, "resolve_sticker_sources", side_effect=adapter_module.StickerFetchError("download failed")):
            results = [result async for result in adapter.forward_to_frostagent(event)]
        self.assertEqual(results, [])
        self.assertEqual(event.sent_chains, [])
        self.assertEqual(adapter.client.delivery_results, [("delivery_test", False, "download failed")])
        self.assertTrue(adapter.client.sent_events[0]["sticker_delivery_receipt"])

    async def test_mixed_sticker_send_reports_success(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(forward_all_group_messages=True, http_base_url="http://frostagent:8080")
        adapter.client = FakeClient([
            {"action": "send_message", "is_intermediate": True, "delivery_id": "delivery_test",
             "messages": [{"type": "plain", "text": "hello"},
                          {"type": "image", "url": "base64://c3RpY2tlcg==", "is_sticker": True}]},
            {"action": "noop"},
        ])
        event = FakeEvent(content="hello")
        results = [result async for result in adapter.forward_to_frostagent(event)]
        self.assertEqual(results, [])
        self.assertEqual(len(event.sent_chains), 1)
        self.assertEqual(adapter.client.delivery_results, [("delivery_test", True, "")])

    async def test_mixed_sticker_send_failure_reports_rejection(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(forward_all_group_messages=True, http_base_url="http://frostagent:8080")
        adapter.client = FakeClient([
            {"action": "send_message", "is_intermediate": True, "delivery_id": "delivery_test",
             "messages": [{"type": "plain", "text": "hello"},
                          {"type": "image", "url": "base64://c3RpY2tlcg==", "is_sticker": True}]},
            {"action": "noop"},
        ])

        class FailingEvent(FakeEvent):
            async def send(self, _chain: _MessageChain) -> None:
                raise RuntimeError("platform rejected")

        event = FailingEvent(content="hello")
        results = [result async for result in adapter.forward_to_frostagent(event)]
        self.assertEqual(results, [])
        self.assertEqual(event.sent_chains, [])
        self.assertEqual(adapter.client.delivery_results, [("delivery_test", False, "platform rejected")])

    async def test_astrbot_wake_prefix_does_not_wake_frostagent(self):
        event = FakeEvent(group_id="group_test", content="/help", is_wake=True)
        event.is_wake = True
        sent = await self.forward(event)
        self.assertEqual(len(sent), 1)
        self.assertFalse(sent[0]["is_wake"])
        self.assertFalse(sent[0]["is_at"])
        self.assertEqual(sent[0]["content"], "/help")

    async def test_astrbot_prefix_stays_local_when_group_forwarding_is_disabled(self):
        sent = await self.forward(
            FakeEvent(group_id="group_test", content="/hello", is_wake=True),
            forward_all_group_messages=False,
        )
        self.assertEqual(sent, [])

    async def test_filtered_sender_change_marks_next_forwarded_event(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(
            forward_all_group_messages=False, http_base_url="http://127.0.0.1:8080",
        )
        adapter.client = FakeClient()
        events = [
            FakeEvent(group_id="group_test", content="first", mention_bot=True, sender_id="bot_test"),
            FakeEvent(group_id="group_test", content="hidden human", sender_id="human_test"),
            FakeEvent(group_id="group_test", content="hidden bot", sender_id="bot_test"),
            FakeEvent(group_id="group_test", content="resumed", mention_bot=True, sender_id="bot_test"),
            FakeEvent(group_id="group_test", content="consecutive", mention_bot=True, sender_id="bot_test"),
        ]
        for event in events:
            async for _result in adapter.forward_to_frostagent(event):
                pass
        sent = adapter.client.sent_events
        self.assertEqual(len(sent), 3)
        self.assertEqual([item["content"] for item in sent], ["first", "resumed", "consecutive"])
        self.assertEqual([item["bot_loop_interrupted"] for item in sent], [True, True, False])
        self.assertTrue(all(item["user_id"] == "bot_test" for item in sent))

    async def test_interruption_survives_disconnected_send(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(
            forward_all_group_messages=False, http_base_url="http://127.0.0.1:8080",
        )
        adapter.client = FakeClient()
        async def forward(event: FakeEvent) -> None:
            async for _result in adapter.forward_to_frostagent(event):
                pass

        await forward(FakeEvent(group_id="group_test", content="first", mention_bot=True, sender_id="bot_test"))
        await forward(FakeEvent(group_id="group_test", content="hidden", sender_id="human_test"))
        adapter.client.connected = False
        await forward(FakeEvent(group_id="group_test", content="lost", mention_bot=True, sender_id="bot_test"))
        self.assertEqual(len(adapter.client.sent_events), 1)
        adapter.client.connected = True
        await forward(FakeEvent(group_id="group_test", content="resumed", mention_bot=True, sender_id="bot_test"))
        await forward(FakeEvent(group_id="group_test", content="consecutive", mention_bot=True, sender_id="bot_test"))
        self.assertEqual(
            [item["bot_loop_interrupted"] for item in adapter.client.sent_events],
            [True, True, False],
        )

    async def test_filtered_sender_change_is_scoped_to_group(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(
            forward_all_group_messages=False, http_base_url="http://127.0.0.1:8080",
        )
        adapter.client = FakeClient()
        events = [
            FakeEvent(group_id="group_a", content="a1", mention_bot=True, sender_id="bot_test"),
            FakeEvent(group_id="group_b", content="b1", mention_bot=True, sender_id="bot_test"),
            FakeEvent(group_id="group_a", content="hidden", sender_id="human_test"),
            FakeEvent(group_id="group_b", content="b2", mention_bot=True, sender_id="bot_test"),
            FakeEvent(group_id="group_a", content="a2", mention_bot=True, sender_id="bot_test"),
        ]
        for event in events:
            async for _result in adapter.forward_to_frostagent(event):
                pass
        self.assertEqual(
            [item["bot_loop_interrupted"] for item in adapter.client.sent_events],
            [True, True, False, True],
        )

    async def test_reply_to_bot_still_wakes_without_astrbot_flag(self):
        event = FakeEvent(group_id="group_test", content="继续")
        reply = Reply("reply_test")
        reply.sender_id = "bot_self"
        event.message_obj.message = [reply]
        sent = await self.forward(event, forward_all_group_messages=False)
        self.assertEqual(len(sent), 1)
        self.assertTrue(sent[0]["is_wake"])
        self.assertFalse(sent[0]["is_at"])

    async def test_reply_to_other_user_does_not_inherit_astrbot_wake(self):
        event = FakeEvent(group_id="group_test", content="继续", is_wake=True)
        reply = Reply("reply_test")
        reply.sender_id = "other_test"
        event.message_obj.message = [reply]
        sent = await self.forward(event)
        self.assertEqual(len(sent), 1)
        self.assertFalse(sent[0]["is_wake"])

    async def test_private_message_still_forwards_without_astrbot_wake(self):
        sent = await self.forward(FakeEvent(content="hello"))
        self.assertEqual(len(sent), 1)
        self.assertEqual(sent[0]["message_type"], "private")

    async def forward(
        self,
        event: FakeEvent,
        *,
        forward_all_group_messages: bool = True,
    ) -> list[dict[str, Any]]:
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(
            forward_all_group_messages=forward_all_group_messages,
            http_base_url="http://127.0.0.1:8080",
        )
        adapter.client = FakeClient()

        async for _result in adapter.forward_to_frostagent(event):
            pass

        return adapter.client.sent_events

    async def test_invalid_configuration_drops_events_without_forwarding(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = "invalid ws_url"
        adapter.settings = SimpleNamespace(forward_all_group_messages=True)
        adapter.client = FakeClient()
        event = FakeEvent(content="hello")

        results = [result async for result in adapter.forward_to_frostagent(event)]

        self.assertEqual(results, [])
        self.assertEqual(adapter.client.sent_events, [])

    async def test_uses_message_obj_message_id(self):
        event = FakeEvent(content="hello")
        self.assertFalse(hasattr(event, "message_id"))

        sent = await self.forward(event)

        self.assertEqual(len(sent), 1)
        self.assertEqual(sent[0]["message_id"], "msg_test")

    async def test_empty_group_message_is_dropped(self):
        sent = await self.forward(FakeEvent(group_id="group_test"))
        self.assertEqual(sent, [])

    async def test_mention_only_group_message_is_forwarded(self):
        sent = await self.forward(
            FakeEvent(group_id="group_test", mention_bot=True)
        )
        self.assertEqual(len(sent), 1)
        self.assertTrue(sent[0]["is_at"])
        self.assertTrue(sent[0]["is_wake"])
        self.assertEqual(sent[0]["content"], "")

    async def test_mention_only_group_message_bypasses_forward_all_filter(self):
        sent = await self.forward(
            FakeEvent(group_id="group_test", mention_bot=True),
            forward_all_group_messages=False,
        )
        self.assertEqual(len(sent), 1)
        self.assertTrue(sent[0]["is_at"])

    async def test_empty_private_wake_event_is_dropped(self):
        sent = await self.forward(FakeEvent(is_wake=True))
        self.assertEqual(sent, [])

    async def test_image_reply_disables_astrbot_default_llm(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(
            forward_all_group_messages=True,
            http_base_url="http://127.0.0.1:8080",
        )
        adapter.client = FakeClient([{"action": "reply", "content": "FrostAgent 回复"}])
        event = FakeEvent(image_url="https://example.com/image.png")

        results = [
            result async for result in adapter.forward_to_frostagent(event)
        ]

        self.assertEqual(results, [{"content": "FrostAgent 回复"}])
        self.assertTrue(event.call_llm)
        self.assertEqual(event.should_call_llm_calls, [True])

    async def test_multiple_responses_disable_default_llm_once(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(
            forward_all_group_messages=True,
            http_base_url="http://127.0.0.1:8080",
        )
        adapter.client = FakeClient([
            {
                "action": "send_message",
                "content": "工具消息",
                "is_intermediate": True,
            },
            {"action": "reply", "content": "最终回复"},
        ])
        event = FakeEvent(content="测试")

        results = [
            result async for result in adapter.forward_to_frostagent(event)
        ]

        self.assertEqual(results, [
            {"content": "工具消息"},
            {"content": "最终回复"},
        ])
        self.assertEqual(event.should_call_llm_calls, [True])

    async def test_noop_keeps_event_propagation_unchanged(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(forward_all_group_messages=True)
        adapter.client = FakeClient()
        event = FakeEvent(group_id="group_test", content="普通群聊消息")

        results = [
            result async for result in adapter.forward_to_frostagent(event)
        ]

        self.assertEqual(results, [])
        self.assertFalse(event.call_llm)
        self.assertEqual(event.should_call_llm_calls, [])

    async def test_admin_silent_drop_noop_suppresses_default_llm(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(forward_all_group_messages=True)
        adapter.client = FakeClient([
            {
                "action": "noop",
                "subtype": "admin_silent_drop",
                "suppress_llm": True,
            }
        ])
        event = FakeEvent(group_id="group_test", content="[@bot] /reset")

        results = [
            result async for result in adapter.forward_to_frostagent(event)
        ]

        self.assertEqual(results, [])
        self.assertTrue(event.call_llm)
        self.assertEqual(event.should_call_llm_calls, [True])

    async def test_proactive_silent_noop_suppresses_default_llm_without_response(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(forward_all_group_messages=True)
        adapter.client = FakeClient([
            {
                "action": "noop",
                "suppress_llm": True,
                "echo": "reply_msg_test",
            }
        ])
        event = FakeEvent(group_id="group_test", content="群闲聊命中主动回复后 stay_silent")

        results = [
            result async for result in adapter.forward_to_frostagent(event)
        ]

        self.assertEqual(results, [])
        self.assertTrue(event.call_llm)
        self.assertEqual(event.should_call_llm_calls, [True])

    async def test_proactive_security_blocked_noop_suppresses_default_llm(self):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(forward_all_group_messages=True)
        adapter.client = FakeClient([
            {
                "action": "noop",
                "suppress_llm": True,
                "echo": "reply_msg_test",
            }
        ])
        event = FakeEvent(group_id="group_test", content="群闲聊命中主动回复但被安全黑名单拦截")

        results = [
            result async for result in adapter.forward_to_frostagent(event)
        ]

        self.assertEqual(results, [])
        self.assertTrue(event.call_llm)
        self.assertEqual(event.should_call_llm_calls, [True])


class RuntimeInfoCommandTest(unittest.IsolatedAsyncioTestCase):
    def make_adapter(self, actions=None):
        adapter = object.__new__(FrostAgentAdapter)
        adapter._configuration_error = None
        adapter.settings = SimpleNamespace(
            forward_all_group_messages=False,
            http_base_url="http://127.0.0.1:8080",
        )
        adapter.client = FakeClient(actions)
        return adapter

    async def forward(self, adapter, event):
        return [result async for result in adapter.forward_to_frostagent(event)]

    async def test_current_group_command_bypasses_filter_without_wake(self):
        adapter = self.make_adapter([{"action": "reply", "content": "FrostAgent 运行状态"}])
        event = FakeEvent(group_id="group_test", content=" \n#fa\t ")
        original_send = adapter.client.send_event

        async def send(payload):
            self.assertEqual(event.should_call_llm_calls, [True])
            return await original_send(payload)

        adapter.client.send_event = send
        results = await self.forward(adapter, event)
        self.assertEqual(results, [{"content": "FrostAgent 运行状态"}])
        self.assertEqual(event.should_call_llm_calls, [True])
        self.assertIsNone(adapter.client.queue)
        payload = adapter.client.sent_events[0]
        self.assertEqual(payload["content"], "#fa")
        self.assertTrue(payload["metadata"]["runtime_info_command"])
        self.assertFalse(payload["is_at"])
        self.assertFalse(payload["is_wake"])
        self.assertEqual(payload["attachments"], [])

    async def test_real_bot_mention_and_split_current_text_are_accepted(self):
        event = FakeEvent(group_id="group_test", content="@Bot #fa")
        event.message_obj.message = [At("bot_self"), Plain(" #"), Plain("fa ")]
        event.message_obj.raw_message = {"message": [
            {"type": "at", "data": {"qq": "bot_self"}},
            {"type": "text", "data": {"text": " #fa "}},
        ]}
        payload = await adapter_module.build_frostagent_payload(event)
        self.assertTrue(payload["metadata"]["runtime_info_command"])
        self.assertEqual(payload["content"], "#fa")
        self.assertTrue(payload["is_at"])

    async def test_current_text_can_use_raw_onebot_segments_without_components(self):
        event = FakeEvent(group_id="group_test", content="unreliable fallback")
        event.message_obj.message = []
        event.message_obj.raw_message = {"message": json.dumps([
            {"type": "text", "data": {"text": " #fa "}},
        ])}
        payload = await adapter_module.build_frostagent_payload(event)
        self.assertTrue(payload["metadata"]["runtime_info_command"])
        self.assertEqual(payload["content"], "#fa")

    async def test_quote_only_and_string_fallback_do_not_trigger_or_escape_filter(self):
        for components in ([], [Reply("quote_test")]):
            with self.subTest(components=components):
                adapter = self.make_adapter()
                event = FakeEvent(group_id="group_test", content="#fa")
                event.message_obj.message = components
                if components:
                    components[0].chain = [Plain("#fa")]
                    components[0].message_str = "#fa"
                self.assertFalse(adapter_module.is_runtime_info_command(event))
                self.assertEqual(await self.forward(adapter, event), [])
                self.assertEqual(adapter.client.sent_events, [])
                self.assertEqual(event.should_call_llm_calls, [])

    async def test_current_command_ignores_quoted_text_and_images(self):
        event = FakeEvent(group_id="group_test", content="quoted history and #fa")
        reply = Reply("quote_test")
        reply.sender_id = "other_test"
        reply.chain = [Plain("private quoted history"), Image("https://example.com/private.png")]
        event.message_obj.message = [reply, Plain("#fa")]
        event.platform = "aiocqhttp"
        event.bot = SimpleNamespace(call_action=lambda **_kwargs: self.fail("must not look up history"))
        with patch.object(adapter_module, "extract_attachments", side_effect=AssertionError("must not load quoted media")):
            payload = await adapter_module.build_frostagent_payload(event)
        self.assertTrue(payload["metadata"]["runtime_info_command"])
        self.assertEqual(payload["content"], "#fa")
        self.assertEqual(payload["attachments"], [])
        self.assertNotIn("reply_message_id", payload["metadata"])
        self.assertNotIn("quote_lookup_supported", payload["metadata"])
        self.assertNotIn("private quoted history", json.dumps(payload))

    async def test_diagnostic_never_answers_quote_lookup_with_history(self):
        adapter = self.make_adapter([
            {"type": "quote_lookup", "echo": "lookup_test", "event_message_id": "msg_test", "message_id": "quote_test"},
            {"action": "noop", "suppress_llm": True},
        ])
        event = FakeEvent(group_id="group_test", content="#fa")
        event.platform = "aiocqhttp"
        with patch.object(adapter_module, "lookup_reply_onebot_message", side_effect=AssertionError("must not look up history")):
            self.assertEqual(await self.forward(adapter, event), [])
        self.assertEqual(adapter.client.sent_events[1]["type"], "quote_lookup_result")
        self.assertEqual(adapter.client.sent_events[1]["data"], {})

    async def test_non_commands_do_not_escape_group_filter(self):
        cases = [
            [Plain("#FA")], [Plain("say #fa")], [Plain("#fa extra")],
            [Plain("#fa\n#fa")], [Plain("#fa"), At("other_test")],
            [Plain("#fa"), Image("https://example.com/image.png")],
            [Plain("#fa"), SimpleNamespace(text="unknown component")],
            [Reply("quote_test"), Plain("ordinary text")],
        ]
        for components in cases:
            with self.subTest(components=components):
                adapter = self.make_adapter()
                event = FakeEvent(group_id="group_test", content="#fa")
                event.message_obj.message = components
                self.assertFalse(adapter_module.is_runtime_info_command(event))
                self.assertEqual(await self.forward(adapter, event), [])
                self.assertEqual(adapter.client.sent_events, [])
                self.assertEqual(event.should_call_llm_calls, [])

    async def test_raw_segments_must_agree_and_reject_nontext_content(self):
        cases = [
            [{"type": "text", "data": {"text": "not the command"}}],
            [{"type": "reply", "data": {"id": "quote_test", "text": "#fa"}}],
            [{"type": "text", "data": {"text": "#fa"}}, {"type": "image", "data": {"url": "unavailable"}}],
            [{"type": "text", "data": {"text": "#fa"}}, {"type": "at", "data": {"qq": "other_test"}}],
            [{"type": "text", "data": {"text": "#fa"}}, {"type": "face", "data": {"id": "face_test"}}],
            [{"type": "text", "data": {"text": "#fa"}}, "malformed segment"],
        ]
        for segments in cases:
            with self.subTest(segments=segments):
                event = FakeEvent(group_id="group_test", content="#fa")
                event.message_obj.raw_message = {"message": segments}
                self.assertFalse(adapter_module.is_runtime_info_command(event))
                payload = await adapter_module.build_frostagent_payload(event)
                self.assertFalse(payload["metadata"]["runtime_info_command"])

    async def test_private_message_is_not_diagnostic(self):
        event = FakeEvent(content="#fa")
        self.assertFalse(adapter_module.is_runtime_info_command(event))
        payload = await adapter_module.build_frostagent_payload(event)
        self.assertFalse(payload["metadata"]["runtime_info_command"])

    async def test_non_message_notice_cannot_be_diagnostic(self):
        event = FakeEvent(group_id="group_test", content="#fa")
        event.message_obj.raw_message = {"post_type": "notice"}
        self.assertFalse(adapter_module.is_runtime_info_command(event))

    async def test_disabled_or_blocked_terminal_noop_keeps_local_llm_suppressed(self):
        for action in (
            {"action": "noop", "subtype": "runtime_info_disabled", "suppress_llm": True},
            {"action": "noop", "subtype": "security_blocked", "suppress_llm": True},
            {"action": "noop"},
        ):
            with self.subTest(action=action):
                adapter = self.make_adapter([action])
                event = FakeEvent(group_id="group_test", content="#fa")
                self.assertEqual(await self.forward(adapter, event), [])
                self.assertEqual(event.should_call_llm_calls, [True])
                self.assertIsNone(adapter.client.queue)

    async def test_disconnection_or_send_failure_keeps_local_llm_suppressed(self):
        for raises in (False, True):
            with self.subTest(raises=raises):
                adapter = self.make_adapter()
                adapter.client.connected = False
                event = FakeEvent(group_id="group_test", content="#fa")
                if raises:
                    async def fail_send(_payload):
                        raise RuntimeError("connection closed")
                    adapter.client.send_event = fail_send
                self.assertEqual(await self.forward(adapter, event), [])
                self.assertEqual(event.should_call_llm_calls, [True])
                self.assertIsNone(adapter.client.queue)

    async def test_wait_timeout_keeps_local_llm_suppressed(self):
        adapter = self.make_adapter()
        event = FakeEvent(group_id="group_test", content="#fa")

        async def timeout_wait(awaitable, timeout):
            awaitable.close()
            raise asyncio.TimeoutError()

        with patch.object(asyncio, "wait_for", side_effect=timeout_wait):
            self.assertEqual(await self.forward(adapter, event), [])
        self.assertEqual(event.should_call_llm_calls, [True])
        self.assertIsNone(adapter.client.queue)

    async def test_invalid_configuration_keeps_local_llm_suppressed(self):
        adapter = self.make_adapter()
        adapter._configuration_error = "invalid ws_url"
        event = FakeEvent(group_id="group_test", content="#fa")
        self.assertEqual(await self.forward(adapter, event), [])
        self.assertEqual(event.should_call_llm_calls, [True])
        self.assertEqual(adapter.client.sent_events, [])


class FakeContext:
    def __init__(self) -> None:
        self.sent_messages: list[tuple[str, Any]] = []

    async def send_message(self, umo: str, chain: Any) -> None:
        self.sent_messages.append((umo, chain))


class ProactiveActionDispatchTest(unittest.IsolatedAsyncioTestCase):
    async def test_proactive_dispatch_platform_and_umo(self) -> None:
        fake_context = FakeContext()
        settings = Settings(
            ws_url="ws://127.0.0.1:1234/instances/a1b2c3d4/ws/astrbot",
            http_base_url="http://127.0.0.1:8080",
            forward_all_group_messages=True,
            heartbeat_interval=30,
            reconnect_interval=5,
        )
        client = FrostAgentWSClient(settings, context=fake_context)

        # 1. Group action with explicit platform -> {platform}:GroupMessage:{group_id}
        await client._dispatch_proactive_action({
            "action": "send_message",
            "platform": "telegram",
            "message_type": "group",
            "group_id": "tg_123",
            "content": "tg group reply",
        })
        self.assertEqual(len(fake_context.sent_messages), 1)
        umo, chain = fake_context.sent_messages[0]
        self.assertEqual(umo, "telegram:GroupMessage:tg_123")
        self.assertEqual(len(chain.chain), 1)
        self.assertEqual(chain.chain[0].text, "tg group reply")

        # 2. Private action with explicit platform -> {platform}:FriendMessage:{user_id}
        await client._dispatch_proactive_action({
            "action": "send_message",
            "platform": "discord",
            "message_type": "private",
            "user_id": "disc_456",
            "content": "discord private reply",
        })
        self.assertEqual(len(fake_context.sent_messages), 2)
        umo, chain = fake_context.sent_messages[1]
        self.assertEqual(umo, "discord:FriendMessage:disc_456")
        self.assertEqual(chain.chain[0].text, "discord private reply")

        # 3. Group action with fallback platform -> astrbot:GroupMessage:{group_id}
        await client._dispatch_proactive_action({
            "action": "send_message",
            "platform": "",
            "message_type": "group",
            "group_id": "grp_789",
            "content": "fallback group reply",
        })
        self.assertEqual(len(fake_context.sent_messages), 3)
        umo, chain = fake_context.sent_messages[2]
        self.assertEqual(umo, "astrbot:GroupMessage:grp_789")

        # 4. Private action with fallback platform -> astrbot:FriendMessage:{user_id}
        await client._dispatch_proactive_action({
            "action": "send_message",
            "platform": "",
            "message_type": "private",
            "user_id": "user_101",
            "content": "fallback private reply",
        })
        self.assertEqual(len(fake_context.sent_messages), 4)
        umo, chain = fake_context.sent_messages[3]
        self.assertEqual(umo, "astrbot:FriendMessage:user_101")


if __name__ == "__main__":
    unittest.main()
