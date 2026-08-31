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


class FakeMessageChain:
    def __init__(self, chain: list[object]) -> None:
        self.chain = chain


class OutboundEvent:
    def __init__(self) -> None:
        self.sent: list[FakeMessageChain] = []
        self.chain_results: list[list[object]] = []

    async def send(self, chain: FakeMessageChain) -> None:
        self.sent.append(chain)

    def chain_result(self, parts: list[object]) -> object:
        self.chain_results.append(parts)
        return parts


class InboundImage:
    def __init__(self, data: bytes, sub_type: int = 1, url: str = "") -> None:
        self.data_bytes = data
        self.subType = sub_type
        self.url = url

    async def convert_to_base64(self) -> str:
        return base64.b64encode(self.data_bytes).decode("ascii")


class Reply:
    def __init__(self, message_id: str, chain: list[object]) -> None:
        self.id = message_id
        self.chain = chain


class MessageObject:
    def __init__(self, message: list[object], raw_message: object = None) -> None:
        self.message = message
        self.raw_message = raw_message


class InboundEvent:
    def __init__(
        self,
        message: list[object],
        raw_segments: list[dict] = None,
        bot: object = None,
    ) -> None:
        raw_message = (
            {"message": raw_segments, "self_id": "bot_self"}
            if raw_segments is not None
            else None
        )
        self.message_obj = MessageObject(message, raw_message)
        self.bot = bot


class FakeBot:
    def __init__(self, response: dict) -> None:
        self.response = response
        self.calls: list[dict] = []

    async def call_action(self, **params) -> dict:
        self.calls.append(params)
        return self.response


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
    event.MessageChain = FakeMessageChain
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

    def test_current_and_quoted_images_are_forwarded_as_base64(self) -> None:
        current_data = b"current-sticker"
        quoted_data = b"quoted-sticker"
        event = InboundEvent([
            InboundImage(current_data),
            Reply("msg_quoted", [InboundImage(quoted_data)]),
        ])

        with load_plugin_module() as module:
            attachments = asyncio.run(module.extract_attachments(event, "msg_current"))

            self.assertEqual(len(attachments), 2)
            self.assertEqual(attachments[0]["message_id"], "msg_current")
            self.assertEqual(attachments[1]["message_id"], "msg_quoted")
            self.assertEqual(base64.b64decode(attachments[0]["content"]), current_data)
            self.assertEqual(base64.b64decode(attachments[1]["content"]), quoted_data)
            self.assertEqual(module.extract_reply_message_id(event), "msg_quoted")

    def test_raw_image_sticker_sub_type_is_preserved_without_downloadable_source(self) -> None:
        image_data = b"custom-sticker"
        event = InboundEvent(
            [InboundImage(image_data, sub_type=0)],
            raw_segments=[{
                "type": "image",
                "data": {"file": "cached-image-token", "sub_type": 1},
            }],
        )

        with load_plugin_module() as module:
            attachments = asyncio.run(module.extract_attachments(event, "msg_sticker"))

            self.assertEqual(len(attachments), 1)
            self.assertEqual(attachments[0]["sub_type"], 1)
            self.assertEqual(base64.b64decode(attachments[0]["content"]), image_data)

    def test_raw_regular_image_remains_non_sticker(self) -> None:
        image_data = b"regular-image"
        event = InboundEvent(
            [InboundImage(image_data, sub_type=0)],
            raw_segments=[{
                "type": "image",
                "data": {"file": "cached-image-token", "sub_type": 0},
            }],
        )

        with load_plugin_module() as module:
            attachments = asyncio.run(module.extract_attachments(event, "msg_image"))

            self.assertEqual(len(attachments), 1)
            self.assertEqual(attachments[0]["sub_type"], 0)

    def test_quoted_raw_image_sticker_sub_type_is_preserved(self) -> None:
        image_data = b"quoted-custom-sticker"
        bot = FakeBot({
            "message": [{
                "type": "image",
                "data": {"file": "quoted-cache-token", "sub_type": 1},
            }],
        })
        event = InboundEvent(
            [Reply("42", [InboundImage(image_data, sub_type=0)])],
            bot=bot,
        )

        with load_plugin_module() as module:
            attachments = asyncio.run(module.extract_attachments(event, "msg_current"))

            self.assertEqual(len(attachments), 1)
            self.assertEqual(attachments[0]["message_id"], "42")
            self.assertEqual(attachments[0]["sub_type"], 1)
            self.assertEqual(base64.b64decode(attachments[0]["content"]), image_data)

    def test_market_face_image_without_sub_type_is_a_sticker(self) -> None:
        image_data = b"market-face"
        market_url = (
            "https://gxh.vip.qq.com/club/item/parcel/item/ab/abcdef/raw300.gif"
        )
        event = InboundEvent([InboundImage(image_data, sub_type=0, url=market_url)])

        with load_plugin_module() as module:
            attachments = asyncio.run(module.extract_attachments(event, "msg_market"))

            self.assertEqual(len(attachments), 1)
            self.assertEqual(attachments[0]["sub_type"], 1)
            self.assertEqual(base64.b64decode(attachments[0]["content"]), image_data)

    def test_raw_mface_is_derived_and_forwarded_as_base64(self) -> None:
        image_data = b"native-mface"
        event = InboundEvent([], raw_segments=[{
            "type": "mface",
            "data": {
                "emoji_id": "abcdef",
                "emoji_package_id": "123",
                "key": "key",
            },
        }])

        with load_plugin_module() as module:
            with patch.object(
                module,
                "urlopen",
                return_value=FakeHTTPResponse(image_data),
            ) as mocked_urlopen:
                attachments = asyncio.run(
                    module.extract_attachments(event, "msg_mface")
                )

            self.assertEqual(len(attachments), 1)
            self.assertEqual(attachments[0]["sub_type"], 1)
            self.assertEqual(base64.b64decode(attachments[0]["content"]), image_data)
            request = mocked_urlopen.call_args.args[0]
            self.assertEqual(
                request.full_url,
                "https://gxh.vip.qq.com/club/item/parcel/item/ab/abcdef/raw300.gif",
            )

    def test_quoted_mface_is_recovered_from_onebot_get_msg(self) -> None:
        image_data = b"quoted-native-mface"
        bot = FakeBot({
            "message": [{
                "type": "mface",
                "data": {
                    "emoji_id": "abcdef",
                    "emoji_package_id": "123",
                    "key": "key",
                },
            }],
        })
        event = InboundEvent([Reply("42", [])], bot=bot)

        with load_plugin_module() as module:
            with patch.object(
                module,
                "urlopen",
                return_value=FakeHTTPResponse(image_data),
            ):
                attachments = asyncio.run(
                    module.extract_attachments(event, "msg_current")
                )

            self.assertEqual(len(attachments), 1)
            self.assertEqual(attachments[0]["message_id"], "42")
            self.assertEqual(attachments[0]["sub_type"], 1)
            self.assertEqual(base64.b64decode(attachments[0]["content"]), image_data)
            self.assertEqual(bot.calls, [{
                "action": "get_msg",
                "message_id": 42,
            }])

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

    def test_sticker_image_serializes_compatible_sub_types(self) -> None:
        """StickerImage must support snake-case and LLBot camel-case fields."""
        with load_plugin_module() as module:
            sticker = module.StickerImage("base64://c3RpY2tlcg==")
            result = sticker.toDict()

            self.assertEqual(result, {
                "type": "image",
                "data": {
                    "file": "base64://c3RpY2tlcg==",
                    "sub_type": 1,
                    "subType": 1,
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
            self.assertEqual(payload["data"]["subType"], 1)
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

    def test_sticker_action_bypasses_astrbot_result_pipeline(self) -> None:
        action = {
            "messages": [
                {
                    "type": "image",
                    "url": "base64://c3RpY2tlcg==",
                    "is_sticker": True,
                },
            ]
        }
        event = OutboundEvent()

        with load_plugin_module() as module:
            responses = asyncio.run(module.deliver_action_to_astrbot(event, action))

            self.assertEqual(responses, [])
            self.assertEqual(event.chain_results, [])
            self.assertEqual(len(event.sent), 1)
            self.assertEqual(len(event.sent[0].chain), 1)
            self.assertIsInstance(event.sent[0].chain[0], module.StickerImage)
            self.assertEqual(event.sent[0].chain[0].toDict()["data"]["sub_type"], 1)
            self.assertEqual(event.sent[0].chain[0].toDict()["data"]["subType"], 1)

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

    def test_mention_user_action_returns_one_chain_result(self) -> None:
        action = {
            "messages": [
                {"type": "mention_user", "mention_user_id": "2763665407"},
                {"type": "plain", "text": " 收到啦"},
            ]
        }

        class FakeEvent:
            @staticmethod
            def chain_result(parts):
                return parts

        with load_plugin_module() as module:
            results = module.action_to_astrbot_result(FakeEvent(), action)

            self.assertEqual(len(results), 1)
            self.assertEqual([type(part) for part in results[0]], [FakeAt, FakePlain])
            self.assertEqual(results[0][0].qq, "2763665407")
            self.assertEqual(results[0][1].text, " 收到啦")


if __name__ == "__main__":
    unittest.main()
