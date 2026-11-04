import asyncio
import json
import unittest
from pathlib import Path
from unittest.mock import patch

from tests.test_message_components import (
    FailingInboundImage, FakeBot, FakePlain, InboundEvent, InboundImage, Reply,
    load_plugin_module,
)


class InboundStickerSilenceTests(unittest.TestCase):
    def test_unreadable_sticker_payload_matches_backend_history_fixture(self):
        fixture_path = Path(__file__).resolve().parents[3] / "internal/adapter/astrbot/testdata/proactive_unreadable_sticker.json"
        expected = json.loads(fixture_path.read_text(encoding="utf-8"))
        cases = [
            {"type": "mface", "data": {"url": "https://example.invalid/sticker.gif"}},
            {"type": "mface", "data": {}},
            {"type": "mface"},
            {"type": "sticker"},
            {"type": "image", "data": {"is_sticker": True}},
            {"type": "image", "data": {"sub_type": 1, "file": "base64://invalid"}},
        ]
        with load_plugin_module() as module:
            for segment in cases:
                with self.subTest(segment=segment):
                    event = InboundEvent([], raw_segments=[segment])
                    event.platform = expected["platform"]
                    event.message_obj.message_id = expected["message_id"]
                    event.get_group_id = lambda: expected["group_id"]
                    event.get_sender_id = lambda: expected["user_id"]
                    event.message_str = ""
                    with patch.object(module, "download_sticker_as_base64", side_effect=RuntimeError("synthetic download failure")):
                        payload = asyncio.run(module.build_frostagent_payload(event))
                    for key, value in expected.items():
                        actual = payload.get(key)
                        if key == "metadata":
                            actual = {name: actual.get(name) for name in value}
                        self.assertEqual(actual, value, key)

    def test_failed_raw_stickers_preserve_ordinals_and_quoted_slots(self):
        segments = [
            {"type": "mface", "data": {"url": "https://example.invalid/sticker.gif"}},
            {"type": "mface", "data": {}},
            {"type": "mface", "data": {"file": "base64://c3RpY2tlcg=="}},
            {"type": "mface", "data": {"file": "base64://c3RpY2tlcg=="}},
        ]
        event = InboundEvent([Reply("quoted")], raw_segments=segments, bot=FakeBot({"message": segments}))
        with load_plugin_module() as module:
            counts = {}
            with patch.object(module, "download_sticker_as_base64", side_effect=RuntimeError("synthetic download failure")):
                attachments = asyncio.run(module.extract_attachments(event, "current", counts))
            self.assertEqual(counts, {"current": 4, "quoted": 4})
            self.assertEqual(len(attachments), 2)
            self.assertEqual([att["message_id"] for att in attachments], ["current", "quoted"])
            for attachment in attachments:
                self.assertEqual(attachment["image_indices"], [2, 3])

    def test_failed_normalized_sticker_consumes_only_one_original_slot(self):
        source = "https://example.invalid/sticker.gif"
        component = FailingInboundImage()
        component.url = source
        segment = {"type": "mface", "data": {"url": source}}
        event = InboundEvent(
            [component, Reply("quoted", [component])],
            raw_segments=[segment],
            bot=FakeBot({"message": [segment]}),
        )
        with load_plugin_module() as module:
            counts = {}
            with patch.object(module, "download_sticker_as_base64", side_effect=RuntimeError("synthetic download failure")) as download:
                attachments = asyncio.run(module.extract_attachments(event, "current", counts))
            self.assertEqual(attachments, [])
            self.assertEqual(counts, {"current": 1, "quoted": 1})
            download.assert_not_called()

    def test_classification_uses_current_raw_chain(self):
        cases = [
            ([{"type": "mface"}], True),
            ([{"type": "sticker"}], True),
            ([{"type": "image", "data": {"subType": "1"}}], True),
            ([{"type": "image", "data": {"is_sticker": True}}], True),
            ([{"type": "image", "data": {"sub_type": 1.5}}], False),
            ([{"type": "image", "data": {"sub_type": True}}], False),
            ([{"type": "image", "data": {"file": "photo.gif", "sub_type": 0}}], False),
            ([{"type": "mface"}, {"type": "image", "data": {}}], False),
            ([{"type": "mface"}, {"type": "text", "data": {"text": "hello"}}], False),
            ([{"type": "mface"}, {"type": "text", "data": {"text": " \n"}}], True),
            ([{"type": "mface"}, {"type": "unknown"}], False),
            ([{"type": "reply", "data": {"message": [{"type": "mface"}]}}], False),
            ([{"type": "reply"}, {"type": "mface"}], True),
        ]
        with load_plugin_module() as module:
            for segments, want in cases:
                with self.subTest(segments=segments):
                    self.assertEqual(module.is_sticker_only_message(InboundEvent([], raw_segments=segments)), want)

    def test_component_fallback(self):
        cases = [
            ([InboundImage(b"sticker")], True),
            ([InboundImage(b"photo", sub_type=0)], False),
            ([InboundImage(b"sticker"), FakePlain("hello")], False),
            ([InboundImage(b"sticker"), FailingInboundImage()], False),
            ([Reply("quoted", [InboundImage(b"sticker")])], False),
            ([Reply("quoted", [InboundImage(b"photo", sub_type=0)]), InboundImage(b"sticker")], True),
        ]
        with load_plugin_module() as module:
            for components, want in cases:
                with self.subTest(components=components):
                    self.assertEqual(module.is_sticker_only_message(InboundEvent(components)), want)

    def test_payload_classification_survives_loss_and_deduplication(self):
        with load_plugin_module() as module:
            # Identical bytes sent once as ordinary image and once as sticker
            # become one sticker attachment, but must remain a mixed message.
            event = InboundEvent([InboundImage(b"same", 0), InboundImage(b"same", 1)])
            event.message_str = ""
            payload = asyncio.run(module.build_frostagent_payload(event))
            self.assertEqual(len(payload["attachments"]), 1)
            self.assertEqual(payload["attachments"][0]["sub_type"], 1)
            self.assertFalse(payload["metadata"]["sticker_only"])

            event = InboundEvent([InboundImage(b"sticker"), FailingInboundImage()])
            event.message_str = ""
            payload = asyncio.run(module.build_frostagent_payload(event))
            self.assertFalse(payload["metadata"]["sticker_only"])

            event = InboundEvent([], raw_segments=[{"type": "mface", "data": {"emoji_id": "synthetic"}}])
            event.message_str = "[图片]"
            with patch.object(module, "download_sticker_as_base64", side_effect=RuntimeError("synthetic download failure")):
                payload = asyncio.run(module.build_frostagent_payload(event))
            self.assertEqual(payload["attachments"], [])
            self.assertTrue(payload["metadata"]["sticker_only"])
