from __future__ import annotations

import asyncio
import base64
import importlib
import sys
import types
import unittest
from types import SimpleNamespace
from typing import Any


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


def _register(*_args: object, **_kwargs: object):
    return lambda cls: cls


astrbot_module = types.ModuleType("astrbot")
api_module = types.ModuleType("astrbot.api")
event_module = types.ModuleType("astrbot.api.event")
star_module = types.ModuleType("astrbot.api.star")
api_module.logger = _Logger()
event_module.AstrMessageEvent = object
event_module.MessageChain = _MessageChain
event_module.filter = _Filter()
star_module.Context = object
star_module.Star = _Star
star_module.register = _register
astrbot_module.api = api_module
api_module.event = event_module
api_module.star = star_module
sys.modules["astrbot"] = astrbot_module
sys.modules["astrbot.api"] = api_module
sys.modules["astrbot.api.event"] = event_module
sys.modules["astrbot.api.star"] = star_module
sys.modules["websockets"] = types.ModuleType("websockets")

FrostAgentAdapter = importlib.import_module(
    "adapters.astrbot_plugin_frostagent.main"
).FrostAgentAdapter


class At:
    def __init__(self, qq: str):
        self.qq = qq


class Image:
    def __init__(self, url: str):
        self.url = url

    async def convert_to_base64(self) -> str:
        return base64.b64encode(self.url.encode("utf-8")).decode("ascii")


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
        self.message_id = "msg_test"
        self.platform = "test"
        self.is_at_or_wake_command = is_wake
        self.call_llm = False
        self.should_call_llm_calls: list[bool] = []
        self.message_obj = SimpleNamespace(
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
        adapter.settings = SimpleNamespace(
            forward_all_group_messages=forward_all_group_messages,
            http_base_url="http://127.0.0.1:8080",
        )
        adapter.client = FakeClient()

        async for _result in adapter.forward_to_frostagent(event):
            pass

        return adapter.client.sent_events

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
        adapter.settings = SimpleNamespace(forward_all_group_messages=True)
        adapter.client = FakeClient()
        event = FakeEvent(group_id="group_test", content="普通群聊消息")

        results = [
            result async for result in adapter.forward_to_frostagent(event)
        ]

        self.assertEqual(results, [])
        self.assertFalse(event.call_llm)
        self.assertEqual(event.should_call_llm_calls, [])


if __name__ == "__main__":
    unittest.main()
