from __future__ import annotations

import asyncio
import base64
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


class FakeHTTPResponse:
    def __init__(self, data: bytes, status: int = 200) -> None:
        self.data = data
        self.status = status

    def __enter__(self):
        return self

    def __exit__(self, *_args) -> None:
        return None

    def getcode(self) -> int:
        return self.status

    def read(self, limit: int = -1) -> bytes:
        return self.data if limit < 0 else self.data[:limit]


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

    def test_sticker_image_serializes_sub_type(self) -> None:
        """StickerImage.toDict() must produce a OneBot segment with sub_type=1."""
        with load_plugin_module() as module:
            sticker = module.StickerImage("base64://c3RpY2tlcg==")
            result = sticker.toDict()

            self.assertEqual(result, {
                "type": "image",
                "data": {
                    "file": "base64://c3RpY2tlcg==",
                    "sub_type": 1,
                },
            })

    def test_sticker_image_is_not_astrbot_image(self) -> None:
        """StickerImage must NOT be an instance of Image to bypass aiocqhttp
        base64 conversion."""
        with load_plugin_module() as module:
            sticker = module.StickerImage("base64://c3RpY2tlcg==")
            self.assertNotIsInstance(sticker, FakeImage)

    def test_sticker_endpoint_is_fetched_as_base64_with_sub_type(self) -> None:
        action = {
            "messages": [
                {
                    "type": "image",
                    "url": "/api/sticker/abc/image",
                    "is_sticker": True,
                },
            ]
        }
        image_data = b"fake-png-data"

        with load_plugin_module() as module:
            with patch.object(
                module,
                "urlopen",
                return_value=FakeHTTPResponse(image_data),
            ) as mocked_urlopen:
                resolved = asyncio.run(
                    module.resolve_sticker_sources(action, "http://frostagent:8080")
                )
            parts = module.action_to_message_components(resolved)

            self.assertEqual(len(parts), 1)
            self.assertIsInstance(parts[0], module.StickerImage)
            payload = parts[0].toDict()
            self.assertEqual(payload["data"]["sub_type"], 1)
            self.assertEqual(
                payload["data"]["file"],
                "base64://" + base64.b64encode(image_data).decode("ascii"),
            )
            self.assertNotIn("data/sticker", payload["data"]["file"])
            request = mocked_urlopen.call_args.args[0]
            self.assertEqual(
                request.full_url,
                "http://frostagent:8080/api/sticker/abc/image",
            )

    def test_sticker_http_failure_does_not_create_payload(self) -> None:
        action = {
            "messages": [
                {
                    "type": "image",
                    "url": "/api/sticker/missing/image",
                    "is_sticker": True,
                },
            ]
        }

        with load_plugin_module() as module:
            with patch.object(
                module,
                "urlopen",
                return_value=FakeHTTPResponse(b"not found", status=404),
            ):
                with self.assertRaises(module.StickerFetchError):
                    asyncio.run(
                        module.resolve_sticker_sources(
                            action,
                            "http://frostagent:8080",
                        )
                    )

    def test_sticker_local_path_is_rejected(self) -> None:
        action = {
            "messages": [
                {
                    "type": "image",
                    "path": "data/sticker/private.png",
                    "is_sticker": True,
                },
            ]
        }

        with load_plugin_module() as module:
            with patch.object(module, "urlopen") as mocked_urlopen:
                with self.assertRaises(module.StickerFetchError):
                    asyncio.run(
                        module.resolve_sticker_sources(
                            action,
                            "http://frostagent:8080",
                        )
                    )
                mocked_urlopen.assert_not_called()

    def test_regular_image_uses_image_component(self) -> None:
        """Non-sticker images still use the standard Image component."""
        action = {
            "messages": [
                {"type": "image", "url": "http://example.com/pic.png"},
            ]
        }

        with load_plugin_module() as module:
            with patch.object(module, "urlopen") as mocked_urlopen:
                resolved = asyncio.run(
                    module.resolve_sticker_sources(action, "http://frostagent:8080")
                )
            parts = module.action_to_message_components(resolved)

            self.assertEqual(len(parts), 1)
            self.assertIsInstance(parts[0], FakeImage)
            mocked_urlopen.assert_not_called()


if __name__ == "__main__":
    unittest.main()
