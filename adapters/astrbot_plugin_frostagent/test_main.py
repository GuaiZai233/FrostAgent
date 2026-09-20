from __future__ import annotations

import asyncio
import base64
import importlib
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
    ):
        components = [At("bot_self")] if mention_bot else []
        if image_url:
            components.append(Image(image_url))
        self.platform = "test"
        self.is_at_or_wake_command = is_wake
        self.call_llm = False
        self.should_call_llm_calls: list[bool] = []
        self.message_obj = SimpleNamespace(
            message_id="msg_test",
            message=components,
            sender=SimpleNamespace(nickname="测试用户", card=""),
            group=SimpleNamespace(group_name="测试群"),
        )
        self._group_id = group_id
        self._content = content

    def get_sender_id(self) -> str:
        return "user_test"

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


class FakeClient:
    def __init__(self, actions: list[dict[str, Any]] | None = None):
        self.sent_events: list[dict[str, Any]] = []
        self.queue: asyncio.Queue | None = None
        self.actions = actions or [{"action": "noop"}]

    def register_waiter(self, _msg_id: str) -> asyncio.Queue:
        self.queue = asyncio.Queue()
        return self.queue

    def unregister_waiter(self, _msg_id: str) -> None:
        self.queue = None

    async def send_event(self, payload: dict[str, Any]) -> None:
        self.sent_events.append(payload)
        assert self.queue is not None
        for action in self.actions:
            await self.queue.put(action)


class ForwardToFrostAgentTest(unittest.IsolatedAsyncioTestCase):
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
