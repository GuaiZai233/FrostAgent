from __future__ import annotations

import asyncio
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


def _register(*_args: object, **_kwargs: object):
    return lambda cls: cls


astrbot_module = types.ModuleType("astrbot")
api_module = types.ModuleType("astrbot.api")
event_module = types.ModuleType("astrbot.api.event")
star_module = types.ModuleType("astrbot.api.star")
api_module.logger = _Logger()
event_module.AstrMessageEvent = object
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


class FakeEvent:
    def __init__(
        self,
        *,
        group_id: str = "",
        content: str = "",
        is_wake: bool = False,
        mention_bot: bool = False,
    ):
        components = [At("bot_self")] if mention_bot else []
        self.message_id = "msg_test"
        self.platform = "test"
        self.is_at_or_wake_command = is_wake
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


class FakeClient:
    def __init__(self):
        self.sent_events: list[dict[str, Any]] = []
        self.queue: asyncio.Queue | None = None

    def register_waiter(self, _msg_id: str) -> asyncio.Queue:
        self.queue = asyncio.Queue()
        return self.queue

    def unregister_waiter(self, _msg_id: str) -> None:
        self.queue = None

    async def send_event(self, payload: dict[str, Any]) -> None:
        self.sent_events.append(payload)
        assert self.queue is not None
        await self.queue.put({"action": "noop"})


class ForwardToFrostAgentTest(unittest.IsolatedAsyncioTestCase):
    async def forward(
        self,
        event: FakeEvent,
        *,
        forward_all_group_messages: bool = True,
    ) -> list[dict[str, Any]]:
        adapter = object.__new__(FrostAgentAdapter)
        adapter.settings = SimpleNamespace(
            forward_all_group_messages=forward_all_group_messages
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


if __name__ == "__main__":
    unittest.main()
