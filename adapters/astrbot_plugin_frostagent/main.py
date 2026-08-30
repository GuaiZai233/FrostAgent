from __future__ import annotations

import asyncio
import base64
import inspect
import json
import os
import re
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, AsyncGenerator, Dict, Optional
from urllib.parse import urljoin, urlparse
from urllib.request import Request, urlopen

import websockets
from astrbot.api import logger
from astrbot.api.event import AstrMessageEvent, filter
from astrbot.api.star import Context, Star, register

__all__ = ["FrostAgentAdapter", "StickerImage"]

PLUGIN_DIR = Path(__file__).resolve().parent


@dataclass(frozen=True)
class Settings:
    ws_url: str
    http_base_url: str
    forward_all_group_messages: bool
    heartbeat_interval: int
    reconnect_interval: int


def load_settings(config: dict = None) -> Settings:
    config = config or {}
    return Settings(
        ws_url=config.get("ws_url") or os.getenv("FROSTAGENT_WS_URL", "ws://127.0.0.1:1234/ws/astrbot"),
        http_base_url=config.get("http_base_url")
        or os.getenv("FROSTAGENT_HTTP_BASE_URL", "http://127.0.0.1:8080"),
        forward_all_group_messages=config.get("forward_all_group_messages", True),
        heartbeat_interval=int(config.get("heartbeat_interval", 30)),
        reconnect_interval=int(config.get("reconnect_interval", 5)),
    )


def is_ws_open(ws: Any) -> bool:
    """兼容不同版本 websockets 的连接开启状态检查。"""
    if ws is None:
        return False
    if hasattr(ws, "state"):
        try:
            return ws.state.name == "OPEN"
        except Exception:
            pass
    if hasattr(ws, "open"):
        return bool(ws.open)
    if hasattr(ws, "closed"):
        return not bool(ws.closed)
    return True


class StickerImage:
    """OneBot image segment with sub_type=1 (sticker).

    NOT a subclass of AstrBot's Image: aiocqhttp's _from_segment_to_dict
    checks isinstance(segment, Image) and force-converts to base64 with only
    ``file`` in data, discarding any extra fields.  By being a plain class we
    bypass that branch and land in the generic ``segment.toDict()`` fallback,
    which preserves sub_type in the serialized OneBot segment.
    """

    def __init__(self, file: str):
        self.file = file
        self.type = "image"

    def toDict(self) -> dict:
        return {"type": "image", "data": {"file": self.file, "sub_type": 1}}

    async def to_dict(self) -> dict:
        return self.toDict()


class StickerFetchError(RuntimeError):
    """Raised when a FrostAgent sticker cannot be safely loaded."""


MAX_STICKER_DOWNLOAD_BYTES = 10 * 1024 * 1024
REPLY_LOOKUP_TIMEOUT_SECONDS = 5
STICKER_IMAGE_PATH_PREFIX = "/api/sticker/"
MARKET_FACE_URL_PREFIX = "https://gxh.vip.qq.com/club/item/parcel/item/"
MARKET_FACE_ID_PATTERN = re.compile(r"[A-Za-z0-9_-]{2,128}")


def sticker_download_url(source: str, http_base_url: str) -> str:
    base = urlparse(http_base_url)
    if base.scheme not in ("http", "https") or not base.netloc:
        raise StickerFetchError("FrostAgent http_base_url must be an absolute HTTP(S) URL")

    parsed_source = urlparse(source)
    if parsed_source.scheme:
        if parsed_source.scheme not in ("http", "https"):
            raise StickerFetchError("sticker source must use HTTP(S) or base64")
        absolute_url = source
    else:
        if not source.startswith(STICKER_IMAGE_PATH_PREFIX):
            raise StickerFetchError("sticker source is not a FrostAgent image endpoint")
        absolute_url = urljoin(http_base_url.rstrip("/") + "/", source)

    target = urlparse(absolute_url)
    if (target.scheme, target.netloc) != (base.scheme, base.netloc):
        raise StickerFetchError("sticker source origin does not match FrostAgent http_base_url")
    if not target.path.startswith(STICKER_IMAGE_PATH_PREFIX):
        raise StickerFetchError("sticker source is not a FrostAgent image endpoint")
    return absolute_url


def download_sticker_as_base64(image_url: str) -> str:
    request = Request(image_url, headers={"Accept": "image/*"})
    try:
        with urlopen(request, timeout=30) as response:
            status = getattr(response, "status", None)
            if status is None:
                status = response.getcode()
            if status < 200 or status >= 300:
                raise StickerFetchError(f"sticker endpoint returned HTTP {status}")
            data = response.read(MAX_STICKER_DOWNLOAD_BYTES + 1)
    except StickerFetchError:
        raise
    except Exception as exc:
        raise StickerFetchError(f"failed to fetch sticker: {exc}") from exc

    if not data:
        raise StickerFetchError("sticker endpoint returned an empty file")
    if len(data) > MAX_STICKER_DOWNLOAD_BYTES:
        raise StickerFetchError("sticker endpoint response exceeds 10 MiB")
    return "base64://" + base64.b64encode(data).decode("ascii")


async def resolve_sticker_sources(action: dict[str, Any], http_base_url: str) -> dict[str, Any]:
    messages = action.get("messages") or []
    if not messages:
        return action

    resolved_messages = []
    for message in messages:
        resolved = dict(message)
        if str(message.get("type") or "") == "image" and message.get("is_sticker"):
            source = str(message.get("url") or message.get("path") or "")
            if not source:
                raise StickerFetchError("sticker message has no image source")
            if source.startswith("base64://"):
                encoded = source
            else:
                image_url = sticker_download_url(source, http_base_url)
                encoded = await asyncio.to_thread(download_sticker_as_base64, image_url)
            resolved["url"] = encoded
            resolved.pop("path", None)
        resolved_messages.append(resolved)

    resolved_action = dict(action)
    resolved_action["messages"] = resolved_messages
    return resolved_action


class FrostAgentWSClient:
    """FrostAgent WebSocket 双向通信客户端，支持自动重连、心跳维持及消息分发。"""

    def __init__(self, settings: Settings, context: Context):
        self.settings = settings
        self.context = context
        self.ws: Optional[Any] = None
        self._running = False
        self._worker_task: Optional[asyncio.Task] = None
        self._heartbeat_task: Optional[asyncio.Task] = None
        # 针对每个消息事件的响应队列：message_id -> asyncio.Queue[dict]
        self._pending_queues: Dict[str, asyncio.Queue] = {}
        self._lock = asyncio.Lock()

    async def start(self) -> None:
        self._running = True
        self._worker_task = asyncio.create_task(self._connection_loop())
        self._heartbeat_task = asyncio.create_task(self._heartbeat_loop())

    async def stop(self) -> None:
        self._running = False
        if self._heartbeat_task:
            self._heartbeat_task.cancel()
        if self._worker_task:
            self._worker_task.cancel()
        if self.ws:
            await self.ws.close()

    async def _connection_loop(self) -> None:
        while self._running:
            try:
                logger.info(f"[frostagent-adapter] 正在连接 FrostAgent: {self.settings.ws_url}")
                async with websockets.connect(self.settings.ws_url) as ws:
                    self.ws = ws
                    logger.info("[frostagent-adapter] 已成功建立与 FrostAgent 的 WebSocket 连接")
                    async for raw_msg in ws:
                        await self._handle_incoming_action(raw_msg)
            except asyncio.CancelledError:
                break
            except Exception as e:
                logger.warning(
                    f"[frostagent-adapter] WebSocket 连接异常: {e}，将在 {self.settings.reconnect_interval}s 后重试"
                )
            finally:
                self.ws = None
            if self._running:
                await asyncio.sleep(self.settings.reconnect_interval)

    async def _heartbeat_loop(self) -> None:
        while self._running:
            await asyncio.sleep(self.settings.heartbeat_interval)
            if is_ws_open(self.ws):
                try:
                    heartbeat_payload = {
                        "type": "heartbeat",
                        "timestamp": int(time.time()),
                    }
                    await self.ws.send(json.dumps(heartbeat_payload))
                except Exception as e:
                    logger.debug(f"[frostagent-adapter] 发送心跳失败: {e}")

    async def send_event(self, event_data: dict[str, Any]) -> None:
        if is_ws_open(self.ws):
            await self.ws.send(json.dumps(event_data))
        else:
            logger.warning("[frostagent-adapter] WebSocket 未连接，丢弃入站事件")

    async def _handle_incoming_action(self, raw_msg: str) -> None:
        try:
            action = json.loads(raw_msg)
        except Exception as e:
            logger.error(f"[frostagent-adapter] 解析出站动作失败: {e}, 原始数据: {raw_msg}")
            return

        echo = action.get("echo", "")
        # 尝试匹配 message_id (格式形如 reply_<msg_id> 或 hook_<msg_id>)
        msg_id = ""
        if echo.startswith("reply_"):
            msg_id = echo[len("reply_"):]
        elif echo.startswith("hook_"):
            msg_id = echo[len("hook_"):]

        queue = self._pending_queues.get(msg_id)
        if queue:
            await queue.put(action)
        else:
            # 主动推送或非阻塞分发（如 Dispatcher 产生的出站消息）
            await self._dispatch_proactive_action(action)

    async def _dispatch_proactive_action(self, action: dict) -> None:
        group_id = str(action.get("group_id") or "")
        user_id = str(action.get("user_id") or "")
        platform = str(action.get("platform") or "")
        message_type = str(action.get("message_type") or "")
        target_id = str(action.get("target_id") or group_id or user_id)

        logger.info(f"[frostagent-adapter] 主动推送消息 -> platform={platform}, target={target_id}")

        if not target_id:
            logger.warning("[frostagent-adapter] 主动推送目标 ID 为空，丢弃")
            return

        try:
            from astrbot.api.message import MessageChain

            action = await resolve_sticker_sources(action, self.settings.http_base_url)
            parts = action_to_message_components(action)
            if not parts:
                return

            if message_type == "group" or group_id:
                umo = f"{platform}:GroupMessage:{group_id or target_id}"
            else:
                umo = f"{platform}:FriendMessage:{user_id or target_id}"

            await self.context.send_message(umo, MessageChain(parts))
        except Exception as e:
            logger.warning(f"[frostagent-adapter] 主动推送失败: {e}")

    def register_waiter(self, msg_id: str) -> asyncio.Queue:
        queue: asyncio.Queue = asyncio.Queue()
        self._pending_queues[msg_id] = queue
        return queue

    def unregister_waiter(self, msg_id: str) -> None:
        self._pending_queues.pop(msg_id, None)


@register(
    "frostagent_adapter",
    "frostfallx",
    "FrostAgent 智能体核心适配器插件，通过 WebSocket 连接实现多平台会话、记忆反思与中间工具输出流转。",
    "0.1.0",
)
class FrostAgentAdapter(Star):
    def __init__(self, context: Context, config: dict = None):
        super().__init__(context)
        self.settings = load_settings(config)
        self.client = FrostAgentWSClient(self.settings, context)
        self._init_task = asyncio.create_task(self.client.start())

    @filter.event_message_type(filter.EventMessageType.ALL)
    async def forward_to_frostagent(self, event: AstrMessageEvent) -> AsyncGenerator[Any, None]:
        payload = await build_frostagent_payload(event)
        msg_id = payload["message_id"]

        if not payload.get("content") and not payload.get("attachments"):
            is_group_interaction = payload.get("message_type") == "group" and (
                payload.get("is_wake") or payload.get("is_at")
            )
            if not is_group_interaction:
                return

        if payload.get("message_type") == "group" and not self.settings.forward_all_group_messages:
            if not payload.get("is_wake") and not payload.get("is_at"):
                return

        # 注册该事件的响应队列
        queue = self.client.register_waiter(msg_id)
        try:
            await self.client.send_event(payload)

            # 等待接收 FrostAgent 的回复（包括 sendHook 中间消息与最终回复）
            while True:
                try:
                    action = await asyncio.wait_for(queue.get(), timeout=120.0)
                except asyncio.TimeoutError:
                    logger.warning(f"[frostagent-adapter] 等待 FrostAgent 响应超时 (msg_id: {msg_id})")
                    break

                # 如果收到 noop 动作，说明后端已将该群聊消息捕获进 compact 但无需回复，结束等待
                if action.get("action") == "noop":
                    break

                try:
                    action = await resolve_sticker_sources(action, self.settings.http_base_url)
                except StickerFetchError as e:
                    logger.error(f"[frostagent-adapter] 表情包获取失败，已取消发送: {e}")
                    if not action.get("is_intermediate", False):
                        break
                    continue

                for response in action_to_astrbot_result(event, action):
                    yield response

                # 如果不是中间消息（即最终回复），则本次交互轮次结束
                if not action.get("is_intermediate", False):
                    break
        finally:
            self.client.unregister_waiter(msg_id)

    async def terminate(self):
        await self.client.stop()


async def build_frostagent_payload(event: AstrMessageEvent) -> dict[str, Any]:
    """从 AstrBot 事件中构造符合 FrostAgent 专有协议的 Event 结构。"""
    user_id = extract_sender_id(event)
    group_id = extract_group_id(event)
    message_type = "group" if group_id else "private"
    content = extract_message_text(event)
    is_wake, is_at = check_is_at_or_wake(event)

    msg_id = str(getattr(event, "message_id", "") or f"ast_{int(time.time() * 1000)}")
    attachments = await extract_attachments(event, msg_id)
    reply_message_id = extract_reply_message_id(event)
    platform = _extract_platform_name(event)

    sender_name = ""
    sender_card = ""
    sender = getattr_chain(event, "message_obj", "sender")
    if sender:
        sender_name = str(getattr(sender, "nickname", "") or getattr(sender, "name", "") or "")
        sender_card = str(getattr(sender, "card", "") or getattr(sender, "remark", "") or "")

    group_name = ""
    if group_id:
        group_obj = getattr_chain(event, "message_obj", "group")
        if group_obj:
            group_name = str(getattr(group_obj, "group_name", "") or getattr(group_obj, "name", "") or "")

    session_id = f"{platform}:group:{group_id}" if message_type == "group" else f"{platform}:private:{user_id}"

    payload = {
        "type": "event",
        "event_type": "message",
        "message_id": msg_id,
        "session_id": session_id,
        "platform": platform,
        "message_type": message_type,
        "user_id": user_id,
        "sender_name": sender_name,
        "sender_card": sender_card,
        "group_id": group_id,
        "group_name": group_name,
        "content": content,
        "attachments": attachments,
        "is_wake": is_wake,
        "is_at": is_at,
        "timestamp": int(time.time()),
    }
    if reply_message_id:
        payload["metadata"] = {"reply_message_id": reply_message_id}
    return payload


def _extract_platform_name(event: AstrMessageEvent) -> str:
    """从 PlatformMetadata 对象或字符串中提取纯净的平台名称。"""
    obj = getattr(event, "platform", None)
    if obj is None:
        return "astrbot"
    # PlatformMetadata 对象，优先取 .name，其次 .id
    name = getattr(obj, "name", None) or getattr(obj, "id", None)
    if name:
        return str(name)
    # 兜底：如果已经是字符串且长度合理就直接用，否则截断
    raw = str(obj)
    return raw[:32] if raw else "astrbot"


def check_is_at_or_wake(event: AstrMessageEvent) -> tuple[bool, bool]:
    """检测当前事件是否带有唤醒词、@机器人 或 引用了机器人。

    注意：不读取 event.is_wake，因为 AstrBot 的 WakingCheckStage 会在
    handler filter 匹配时将其置为 True（我们注册了 EventMessageType.ALL，
    导致所有消息的 is_wake 均为 True）。仅使用 is_at_or_wake_command，
    它只在真正的 @机器人、唤醒词前缀或私聊时为 True。
    """
    is_wake = bool(getattr(event, "is_at_or_wake_command", False))
    is_at = False

    self_id = str(call_noargs(event, "get_self_id") or "")
    components = getattr_chain(event, "message_obj", "message") or getattr(event, "message", [])
    if isinstance(components, list):
        for comp in components:
            comp_type = type(comp).__name__.lower()
            if comp_type == "at":
                target_qq = str(first_attr(comp, "qq", "target_id", "user_id") or "")
                if self_id and target_qq == self_id:
                    is_at = True
                    is_wake = True
            elif comp_type == "reply":
                sender_id = str(first_attr(comp, "sender_id", "user_id") or "")
                if self_id and sender_id == self_id:
                    is_wake = True
    return is_wake, is_at


def extract_sender_id(event: AstrMessageEvent) -> str:
    value = call_noargs(event, "get_sender_id")
    if value:
        return str(value)
    sender = getattr_chain(event, "message_obj", "sender")
    value = first_attr(sender, "user_id", "id", "sender_id")
    if value:
        return str(value)
    value = first_attr(event, "sender_id", "user_id")
    return "" if value is None else str(value)


def extract_group_id(event: AstrMessageEvent) -> str:
    value = call_noargs(event, "get_group_id")
    if value:
        return str(value)
    value = first_attr(event, "group_id")
    if value:
        return str(value)
    value = getattr_chain(event, "message_obj", "group_id")
    if value:
        return str(value)
    return ""


def extract_message_text(event: AstrMessageEvent) -> str:
    value = call_noargs(event, "get_message_str")
    if value is not None:
        return str(value)
    value = first_attr(event, "message_str", "raw_message", "raw_msg")
    if value is not None:
        return str(value)
    msg_obj = getattr_chain(event, "message_obj")
    value = first_attr(msg_obj, "message_str", "raw_message", "raw_msg")
    if value is not None:
        return str(value)
    return ""


def extract_reply_message_id(event: AstrMessageEvent) -> str:
    message_obj = getattr_chain(event, "message_obj")
    components = getattr(message_obj, "message", []) or getattr(event, "message", [])
    if not isinstance(components, list):
        return ""
    for comp in components:
        if type(comp).__name__.lower() != "reply":
            continue
        value = first_attr(comp, "id", "message_id")
        if value is not None and str(value).strip():
            return str(value).strip()
    return ""


def market_face_url(emoji_id: Any) -> str:
    value = str(emoji_id or "").strip()
    if not MARKET_FACE_ID_PATTERN.fullmatch(value):
        return ""
    return f"{MARKET_FACE_URL_PREFIX}{value[:2]}/{value}/raw300.gif"


def image_source_from_data(data: Any) -> str:
    for name in ("url", "file", "path"):
        source = str(first_attr(data, name) or "").strip()
        if source.startswith(("http://", "https://", "base64://", "data:")):
            return source
    return market_face_url(first_attr(data, "emoji_id", "emojiId"))


def is_market_face_data(data: Any, segment_type: str = "image") -> bool:
    if segment_type == "mface":
        return True
    if segment_type != "image":
        return False
    if first_attr(data, "emoji_id", "emojiId", "emoji_package_id", "emojiPackageId") is not None:
        return True
    if str(first_attr(data, "file") or "").strip().lower() == "marketface":
        return True
    source = str(first_attr(data, "url", "path") or "").strip().lower()
    return source.startswith(MARKET_FACE_URL_PREFIX)


def is_sticker_sub_type(value: Any) -> bool:
    try:
        return int(value) == 1
    except (TypeError, ValueError):
        return False


async def source_image_attachment(
    source: str,
    message_id: str,
    sub_type: int,
) -> dict[str, Any] | None:
    source = source.strip()
    encoded = ""
    if source.startswith("base64://"):
        encoded = source.removeprefix("base64://")
    elif source.startswith("data:") and ";base64," in source:
        encoded = source.split(",", 1)[1]
    elif source.startswith(("http://", "https://")):
        encoded = await asyncio.to_thread(download_sticker_as_base64, source)
        encoded = encoded.removeprefix("base64://")
    return encoded_image_attachment(encoded, message_id, sub_type)


def encoded_image_attachment(
    encoded: str,
    message_id: str,
    sub_type: Any,
) -> dict[str, Any] | None:
    encoded = encoded.removeprefix("base64://")
    if not encoded:
        return None
    try:
        image_bytes = base64.b64decode(encoded, validate=True)
    except Exception:
        logger.warning("[frostagent-adapter] 入站图片包含非法 Base64，已忽略")
        return None
    if not image_bytes or len(image_bytes) > MAX_STICKER_DOWNLOAD_BYTES:
        logger.warning("[frostagent-adapter] 入站图片为空或超过 10 MiB，已忽略")
        return None

    attachment: dict[str, Any] = {
        "type": "image",
        "message_id": message_id,
        "content": base64.b64encode(image_bytes).decode("ascii"),
    }
    if sub_type is not None:
        try:
            attachment["sub_type"] = int(sub_type)
        except (ValueError, TypeError):
            pass
    return attachment


def component_image_data(comp: Any) -> dict[str, Any]:
    raw_data = first_attr(comp, "raw", "data")
    data = dict(raw_data) if isinstance(raw_data, dict) else {}
    aliases = {
        "url": ("url",),
        "file": ("file",),
        "path": ("path",),
        "sub_type": ("sub_type", "subtype", "subType"),
        "emoji_id": ("emoji_id", "emojiId"),
        "emoji_package_id": ("emoji_package_id", "emojiPackageId"),
    }
    for target, names in aliases.items():
        value = first_attr(comp, *names)
        if value is not None and target not in data:
            data[target] = value
    return data


async def image_attachment(comp: Any, message_id: str) -> dict[str, Any] | None:
    data = component_image_data(comp)
    sub_type = first_attr(data, "sub_type", "subtype", "subType")
    source = image_source_from_data(data)
    comp_type = type(comp).__name__.lower()
    if is_market_face_data(data, "mface" if comp_type == "mface" else "image"):
        sub_type = 1

    encoded = ""
    converter = getattr(comp, "convert_to_base64", None)
    try:
        if callable(converter):
            converted = converter()
            if inspect.isawaitable(converted):
                converted = await converted
            encoded = str(converted or "")
        elif source:
            return await source_image_attachment(source, message_id, int(sub_type or 0))
    except Exception as exc:
        logger.warning(f"[frostagent-adapter] 入站图片转换 Base64 失败: {exc}")
        return None

    return encoded_image_attachment(encoded, message_id, sub_type)


def onebot_segments_from_payload(payload: Any) -> list[dict[str, Any]]:
    segments = first_attr(payload, "message")
    if segments is None:
        segments = first_attr(first_attr(payload, "data"), "message")
    if isinstance(segments, str):
        try:
            segments = json.loads(segments)
        except json.JSONDecodeError:
            return []
    if not isinstance(segments, list):
        return []
    return [segment for segment in segments if isinstance(segment, dict)]


def raw_onebot_segments(event: AstrMessageEvent) -> list[dict[str, Any]]:
    message_obj = getattr_chain(event, "message_obj")
    raw_event = first_attr(message_obj, "raw_message", "raw_msg")
    if raw_event is None:
        raw_event = first_attr(event, "raw_message", "raw_msg")
    return onebot_segments_from_payload(raw_event)


async def lookup_reply_onebot_segments(
    event: AstrMessageEvent,
    message_id: str,
) -> list[dict[str, Any]]:
    bot = first_attr(event, "bot")
    call_action = getattr(bot, "call_action", None)
    if not callable(call_action):
        return []

    lookup_id: int | str = message_id
    if re.fullmatch(r"-?\d+", message_id):
        lookup_id = int(message_id)
    params: dict[str, Any] = {
        "action": "get_msg",
        "message_id": lookup_id,
    }
    message_obj = getattr_chain(event, "message_obj")
    raw_event = first_attr(message_obj, "raw_message", "raw_msg")
    self_id = first_attr(raw_event, "self_id") or call_noargs(event, "get_self_id")
    if self_id is not None and str(self_id).strip():
        params["self_id"] = self_id

    try:
        response = call_action(**params)
        if inspect.isawaitable(response):
            response = await asyncio.wait_for(
                response,
                timeout=REPLY_LOOKUP_TIMEOUT_SECONDS,
            )
    except Exception as exc:
        logger.warning(
            f"[frostagent-adapter] 回查引用消息 {message_id} 失败，"
            f"无法恢复可能被 AstrBot 丢弃的 mface: {exc}"
        )
        return []
    return onebot_segments_from_payload(response)


async def sticker_attachments_from_segments(
    segments: list[dict[str, Any]],
    message_id: str,
    handled_sources: set[str],
) -> list[dict[str, Any]]:
    attachments = []
    for segment in segments:
        segment_type = str(first_attr(segment, "type") or "").lower()
        data = first_attr(segment, "data")
        if not isinstance(data, dict):
            continue
        is_sticker = segment_type == "mface" or (
            segment_type == "image"
            and (
                is_sticker_sub_type(first_attr(data, "sub_type", "subtype", "subType"))
                or is_market_face_data(data, segment_type)
            )
        )
        if not is_sticker:
            continue
        source = image_source_from_data(data)
        if not source:
            logger.warning("[frostagent-adapter] mface 消息缺少可读取的数据，已忽略")
            continue
        if source in handled_sources:
            continue
        try:
            attachment = await source_image_attachment(source, message_id, 1)
        except Exception as exc:
            logger.warning(f"[frostagent-adapter] mface 转换 Base64 失败: {exc}")
            continue
        if attachment:
            attachments.append(attachment)
    return attachments


async def raw_sticker_attachments(
    event: AstrMessageEvent,
    message_id: str,
    handled_sources: set[str],
) -> list[dict[str, Any]]:
    return await sticker_attachments_from_segments(
        raw_onebot_segments(event),
        message_id,
        handled_sources,
    )


def append_unique_attachment(
    attachments: list[dict[str, Any]],
    attachment: dict[str, Any] | None,
) -> None:
    if attachment is None:
        return
    for existing in attachments:
        if (
            existing.get("message_id") == attachment.get("message_id")
            and existing.get("content") == attachment.get("content")
        ):
            if attachment.get("sub_type") == 1:
                existing["sub_type"] = 1
            return
    attachments.append(attachment)


async def extract_attachments(event: AstrMessageEvent, message_id: str) -> list[dict[str, Any]]:
    attachments = []
    handled_sticker_sources: set[str] = set()
    reply_sticker_sources: dict[str, set[str]] = {}
    message_obj = getattr_chain(event, "message_obj")
    components = getattr(message_obj, "message", []) or getattr(event, "message", [])
    if isinstance(components, list):
        for comp in components:
            comp_type = type(comp).__name__.lower()
            if "image" in comp_type or comp_type == "mface":
                attachment = await image_attachment(comp, message_id)
                append_unique_attachment(attachments, attachment)
                if attachment and attachment.get("sub_type") == 1:
                    source = image_source_from_data(component_image_data(comp))
                    if source:
                        handled_sticker_sources.add(source)
            elif comp_type == "reply":
                reply_id = str(first_attr(comp, "id", "message_id") or "").strip()
                if not reply_id:
                    continue
                quoted_sources = reply_sticker_sources.setdefault(reply_id, set())
                chain = first_attr(comp, "chain", "message", "origin", "content")
                if not isinstance(chain, list):
                    continue
                for quoted_comp in chain:
                    quoted_type = type(quoted_comp).__name__.lower()
                    if "image" not in quoted_type and quoted_type != "mface":
                        continue
                    attachment = await image_attachment(quoted_comp, reply_id)
                    append_unique_attachment(attachments, attachment)
                    if attachment and attachment.get("sub_type") == 1:
                        source = image_source_from_data(component_image_data(quoted_comp))
                        if source:
                            quoted_sources.add(source)
    for attachment in await raw_sticker_attachments(
        event,
        message_id,
        handled_sticker_sources,
    ):
        append_unique_attachment(attachments, attachment)
    for reply_id, handled_sources in reply_sticker_sources.items():
        reply_segments = await lookup_reply_onebot_segments(event, reply_id)
        for attachment in await sticker_attachments_from_segments(
            reply_segments,
            reply_id,
            handled_sources,
        ):
            append_unique_attachment(attachments, attachment)
    return attachments


def action_to_astrbot_result(event: AstrMessageEvent, action: dict[str, Any]) -> list[Any]:
    messages = action.get("messages") or []
    if messages:
        parts = action_to_message_components(action)
        return [event.chain_result(parts)] if parts else []

    content = action.get("content", "")
    attachments = action.get("attachments") or []

    results = []
    if content:
        results.append(event.plain_result(str(content)))
    for att in attachments:
        att_type = att.get("type", "")
        if att_type == "image" and att.get("url"):
            results.append(event.image_result(str(att["url"])))
        elif att_type in ("audio", "video"):
            logger.debug(f"[frostagent-adapter] 跳过不支持的附件类型: {att_type}")
    return results


def action_to_message_components(action: dict[str, Any]) -> list[Any]:
    from astrbot.api.message_components import At, Image, Plain

    messages = action.get("messages") or []
    parts: list[Any] = []
    if messages:
        for message in messages:
            message_type = str(message.get("type") or "")
            if message_type == "mention_user":
                mention_user_id = str(message.get("mention_user_id") or "")
                if mention_user_id:
                    parts.append(At(qq=mention_user_id))
            elif message_type == "plain":
                text = str(message.get("text") or "")
                if text:
                    parts.append(Plain(text))
            elif message_type == "image":
                source = str(message.get("url") or message.get("path") or "")
                if source:
                    if message.get("is_sticker"):
                        parts.append(StickerImage(source))
                    else:
                        parts.append(Image(source))
            elif message_type in ("record", "video", "file", "quote"):
                logger.debug(f"[frostagent-adapter] 跳过暂不支持的消息组件: {message_type}")
        return parts

    content = action.get("content", "")
    if content:
        parts.append(Plain(str(content)))
    for attachment in action.get("attachments") or []:
        if attachment.get("type") == "image" and attachment.get("url"):
            parts.append(Image(str(attachment["url"])))
    return parts


def call_noargs(obj: Any, name: str) -> Any:
    fn = getattr(obj, name, None)
    if callable(fn):
        try:
            return fn()
        except Exception:
            return None
    return None


def first_attr(obj: Any, *names: str) -> Any:
    if obj is None:
        return None
    for name in names:
        if isinstance(obj, dict) and name in obj:
            value = obj[name]
            if value is not None:
                return value
        if hasattr(obj, name):
            value = getattr(obj, name)
            if value is not None:
                return value
    return None


def getattr_chain(obj: Any, *names: str) -> Any:
    current = obj
    for name in names:
        if current is None or not hasattr(current, name):
            return None
        current = getattr(current, name)
    return current
