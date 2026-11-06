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
        cases = [(segment, components) for segment in cases for components in (
            [], [FailingInboundImage()],
        )]
        with load_plugin_module() as module:
            for segment, components in cases:
                with self.subTest(segment=segment, normalized=bool(components)):
                    event = InboundEvent(components, raw_segments=[segment])
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
        with load_plugin_module() as module:
            for kind in ("mface", "sticker"):
                for raw_source, component_source in (
                    ("https://example.invalid/sticker.gif", "https://example.invalid/sticker.gif"),
                    ("", ""),
                    ("https://example.invalid/sticker.gif", ""),
                    ("https://example.invalid/sticker.gif", "https://example.invalid/rewritten.gif"),
                ):
                    with self.subTest(kind=kind, raw_source=raw_source, component_source=component_source):
                        component = FailingInboundImage()
                        component.url = component_source
                        segment = {"type": kind, "data": {"url": raw_source}}
                        event = InboundEvent(
                            [component, Reply("quoted", [component])],
                            raw_segments=[segment],
                            bot=FakeBot({"message": [segment]}),
                        )
                        counts = {}
                        with patch.object(module, "download_sticker_as_base64", side_effect=RuntimeError("synthetic download failure")) as download:
                            attachments = asyncio.run(module.extract_attachments(event, "current", counts))
                        self.assertEqual(attachments, [])
                        self.assertEqual(counts, {"current": 1, "quoted": 1})
                        download.assert_not_called()

    def test_normalized_images_follow_raw_order_without_sources(self):
        segments = [
            {"type": "text", "data": {"text": "caption"}},
            {"type": "mface", "data": {}},
            {"type": "image", "data": {"sub_type": 0}},
            {"type": "sticker", "data": {}},
        ]
        components = [FailingInboundImage(), InboundImage(b"photo", 0), InboundImage(b"sticker", 0)]
        event = InboundEvent(components + [Reply("quoted", components)], raw_segments=segments, bot=FakeBot({"message": segments}))
        with load_plugin_module() as module:
            counts = {}
            attachments = asyncio.run(module.extract_attachments(event, "current", counts))
            self.assertEqual(counts, {"current": 3, "quoted": 3})
            for message_id in ("current", "quoted"):
                actual = [att for att in attachments if att["message_id"] == message_id]
                self.assertEqual([att["image_indices"] for att in actual], [[1], [2]])
                self.assertEqual([att["sub_type"] for att in actual], [0, 1])
            self.assertFalse(module.is_sticker_only_message(event))

    def test_raw_only_slots_and_repeated_sources_keep_original_ordinals(self):
        source = "base64://c3RpY2tlcg=="
        cases = [
            ([{"type": "mface", "data": {}}, {"type": "mface", "data": {"file": source}}], [1]),
            ([{"type": "mface", "data": {"file": source}}, {"type": "mface", "data": {"file": source}}], [0, 1]),
            ([{"type": "mface", "data": {"file": "data:image/png;base64,c3RpY2tlcg=="}}, {"type": "mface", "data": {"file": source}}], [0, 1]),
        ]
        with load_plugin_module() as module:
            for segments, indices in cases:
                with self.subTest(segments=segments):
                    event = InboundEvent([InboundImage(b"sticker", 1, source)], raw_segments=segments)
                    counts = {}
                    attachments = asyncio.run(module.extract_attachments(event, "current", counts))
                    self.assertEqual(counts, {"current": 2})
                    self.assertEqual(len(attachments), 1)
                    self.assertEqual(attachments[0]["image_indices"], indices)

    def test_partial_normalization_keeps_ambiguous_photo_separate(self):
        raw_photo_source = "https://example.invalid/photo.png"
        with load_plugin_module() as module:
            for kind in ("mface", "sticker", "image"):
                for sticker_source in ("", "base64://c3RpY2tlcg=="):
                    for photo_source in ("", "https://example.invalid/rewritten.png"):
                        with self.subTest(kind=kind, sticker_source=sticker_source, photo_source=photo_source):
                            sticker_data = {"file": sticker_source} if sticker_source else {}
                            if kind == "image":
                                sticker_data["sub_type"] = 1
                            segments = [
                                {"type": kind, "data": sticker_data},
                                {"type": "image", "data": {"sub_type": 0, "url": raw_photo_source}},
                            ]
                            photo = InboundImage(b"photo", 0, photo_source)
                            event = InboundEvent(
                                [photo, Reply("quoted", [photo])],
                                raw_segments=segments, bot=FakeBot({"message": segments}),
                            )
                            event.message_obj.message_id = "current"
                            event.message_str = ""
                            payload = asyncio.run(module.build_frostagent_payload(event))
                            self.assertFalse(payload["metadata"]["sticker_only"])
                            for message_id in ("current", "quoted"):
                                actual = [att for att in payload["attachments"] if att["message_id"] == message_id]
                                photo_attachment = next(att for att in actual if att["content"] == "cGhvdG8=")
                                self.assertEqual(photo_attachment["sub_type"], 0)
                                # Neither raw ordinal is identifiable: retain both and
                                # allocate a separate slot instead of guessing identity.
                                self.assertEqual(photo_attachment["image_indices"], [2])
                                self.assertEqual(payload["image_slot_counts"][message_id], 3)
                                if sticker_source:
                                    self.assertEqual(actual[0]["image_indices"], [0])
                                    self.assertEqual(actual[0]["sub_type"], 1)
                                    self.assertEqual(actual[0]["content"], "c3RpY2tlcg==")

    def test_omitted_leading_sticker_and_sourceless_raw_photo(self):
        segments = [
            {"type": "sticker", "data": {}},
            {"type": "image", "data": {"sub_type": 0}},
        ]
        photo = InboundImage(b"photo", 0)
        event = InboundEvent(
            [photo, Reply("quoted", [photo])],
            raw_segments=segments, bot=FakeBot({"message": segments}),
        )
        with load_plugin_module() as module:
            counts = {}
            attachments = asyncio.run(module.extract_attachments(event, "current", counts))
            self.assertFalse(module.is_sticker_only_message(event))
            self.assertEqual(counts, {"current": 3, "quoted": 3})
            self.assertEqual([att["message_id"] for att in attachments], ["current", "quoted"])
            for attachment in attachments:
                self.assertEqual(attachment["sub_type"], 0)
                self.assertEqual(attachment["image_indices"], [2])

    def test_source_matches_are_reserved_before_positional_matching(self):
        source = "base64://c3RpY2tlcg=="
        segments = [
            {"type": "sticker", "data": {"file": source}},
            {"type": "image", "data": {"sub_type": 0}},
        ]
        components = [InboundImage(b"photo", 0), InboundImage(b"sticker", 0, source)]
        event = InboundEvent(
            components + [Reply("quoted", components)],
            raw_segments=segments, bot=FakeBot({"message": segments}),
        )
        with load_plugin_module() as module:
            counts = {}
            attachments = asyncio.run(module.extract_attachments(event, "current", counts))
            self.assertEqual(counts, {"current": 3, "quoted": 3})
            for message_id in ("current", "quoted"):
                actual = [att for att in attachments if att["message_id"] == message_id]
                self.assertEqual([att["content"] for att in actual], ["c3RpY2tlcg==", "cGhvdG8="])
                self.assertEqual([att["image_indices"] for att in actual], [[0], [2]])
                self.assertEqual([att["sub_type"] for att in actual], [1, 0])

    def test_partial_normalization_with_source_keeps_photo_original_slot(self):
        source = "https://example.invalid/photo.png"
        segments = [
            {"type": "sticker", "data": {}},
            {"type": "image", "data": {"sub_type": 0, "url": source}},
        ]
        photo = InboundImage(b"photo", 0, source)
        event = InboundEvent(
            [photo, Reply("quoted", [photo])],
            raw_segments=segments, bot=FakeBot({"message": segments}),
        )
        with load_plugin_module() as module:
            counts = {}
            attachments = asyncio.run(module.extract_attachments(event, "current", counts))
            self.assertEqual(counts, {"current": 2, "quoted": 2})
            self.assertEqual([att["message_id"] for att in attachments], ["current", "quoted"])
            for attachment in attachments:
                self.assertEqual(attachment["sub_type"], 0)
                self.assertEqual(attachment["image_indices"], [1])

    def test_partial_normalization_keeps_attachment_order(self):
        event = InboundEvent([InboundImage(b"b", 1, "base64://Yg==")], raw_segments=[
            {"type": "mface", "data": {"file": "base64://YQ=="}},
            {"type": "mface", "data": {"file": "base64://Yg=="}},
        ])
        with load_plugin_module() as module:
            counts = {}
            attachments = asyncio.run(module.extract_attachments(event, "current", counts))
            self.assertEqual(counts, {"current": 2})
            self.assertEqual([att["image_indices"] for att in attachments], [[0], [1]])
            self.assertEqual([att["content"] for att in attachments], ["YQ==", "Yg=="])

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
