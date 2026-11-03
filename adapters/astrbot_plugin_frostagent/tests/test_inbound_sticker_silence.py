import asyncio
import unittest
from unittest.mock import patch

from tests.test_message_components import (
    FailingInboundImage, FakePlain, InboundEvent, InboundImage, Reply,
    load_plugin_module,
)


class InboundStickerSilenceTests(unittest.TestCase):
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
