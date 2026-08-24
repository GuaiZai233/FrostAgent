from __future__ import annotations

import importlib.util
import sys
import types
import unittest
from contextlib import contextmanager
from pathlib import Path
from unittest.mock import patch


class FakeAt:
    def __init__(self, *, qq: str) -> None:
        self.qq = qq


class FakePlain:
    def __init__(self, text: str) -> None:
        self.text = text


class FakeImage:
    def __init__(self, source: str) -> None:
        self.source = source


class FakeLogger:
    def debug(self, *_args, **_kwargs) -> None:
        pass

    info = debug
    warning = debug
    error = debug


class FakeFilter:
    class EventMessageType:
        ALL = object()

    @staticmethod
    def event_message_type(_event_type):
        return lambda handler: handler


def fake_register(*_args, **_kwargs):
    return lambda plugin: plugin


@contextmanager
def load_plugin_module():
    astrbot = types.ModuleType("astrbot")
    api = types.ModuleType("astrbot.api")
    event = types.ModuleType("astrbot.api.event")
    star = types.ModuleType("astrbot.api.star")
    components = types.ModuleType("astrbot.api.message_components")
    websockets = types.ModuleType("websockets")

    api.logger = FakeLogger()
    event.AstrMessageEvent = type("AstrMessageEvent", (), {})
    event.filter = FakeFilter()
    star.Context = type("Context", (), {})
    star.Star = type("Star", (), {})
    star.register = fake_register
    components.At = FakeAt
    components.Image = FakeImage
    components.Plain = FakePlain

    modules = {
        "astrbot": astrbot,
        "astrbot.api": api,
        "astrbot.api.event": event,
        "astrbot.api.star": star,
        "astrbot.api.message_components": components,
        "websockets": websockets,
    }
    module_name = "frostagent_astrbot_plugin_test"
    main_path = Path(__file__).resolve().parents[1] / "main.py"
    spec = importlib.util.spec_from_file_location(module_name, main_path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"无法加载插件模块: {main_path}")
    module = importlib.util.module_from_spec(spec)
    modules[module_name] = module

    with patch.dict(sys.modules, modules):
        spec.loader.exec_module(module)
        yield module


class MessageComponentTests(unittest.TestCase):
    def test_mention_user_and_plain_build_ordered_chain(self) -> None:
        action = {
            "messages": [
                {"type": "mention_user", "mention_user_id": "114514"},
                {"type": "plain", "text": " 一起来聊天吧"},
            ]
        }

        with load_plugin_module() as module:
            parts = module.action_to_message_components(action)

            self.assertEqual([type(part) for part in parts], [FakeAt, FakePlain])
            self.assertEqual(parts[0].qq, "114514")
            self.assertEqual(parts[1].text, " 一起来聊天吧")


if __name__ == "__main__":
    unittest.main()
