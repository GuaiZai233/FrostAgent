from __future__ import annotations

import asyncio
import json
import os
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, AsyncGenerator, Dict, Optional

import websockets
from astrbot.api import logger
from astrbot.api.event import AstrMessageEvent, filter
from astrbot.api.star import Context, Star, register

__all__ = ["FrostAgentAdapter"]

PLUGIN_DIR = Path(__file__).resolve().parent


@dataclass(frozen=True)
class Settings:
    ws_url: str
    forward_all_group_messages: bool
    heartbeat_interval: int
    reconnect_interval: int


def load_settings(config: dict = None) -> Settings:
    config = config or {}
    return Settings(
        ws_url=config.get("ws_url") or os.getenv("FROSTAGENT_WS_URL", "ws://127.0.0.1:1234/ws/astrbot"),
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
        content = action.get("content", "")
        attachments = action.get("attachments") or []
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
            from astrbot.api.message_components import Image, Plain
            from astrbot.api.message import MessageChain

            parts: list = []
            if content:
                parts.append(Plain(content))
            for att in attachments:
                if att.get("type") == "image" and att.get("url"):
                    parts.append(Image(att["url"]))

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
        payload = build_frostagent_payload(event)
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

                for response in action_to_astrbot_result(event, action):
                    yield response

                # 如果不是中间消息（即最终回复），则本次交互轮次结束
                if not action.get("is_intermediate", False):
                    break
        finally:
            self.client.unregister_waiter(msg_id)

    async def terminate(self):
        await self.client.stop()


def build_frostagent_payload(event: AstrMessageEvent) -> dict[str, Any]:
    """从 AstrBot 事件中构造符合 FrostAgent 专有协议的 Event 结构。"""
    user_id = extract_sender_id(event)
    group_id = extract_group_id(event)
    message_type = "group" if group_id else "private"
    content = extract_message_text(event)
    attachments = extract_attachments(event)
    is_wake, is_at = check_is_at_or_wake(event)

    msg_id = str(getattr(event, "message_id", "") or f"ast_{int(time.time() * 1000)}")
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

    return {
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


def extract_attachments(event: AstrMessageEvent) -> list[dict[str, Any]]:
    attachments = []
    # 提取图片等组件
    message_obj = getattr_chain(event, "message_obj")
    components = getattr(message_obj, "message", []) or getattr(event, "message", [])
    if isinstance(components, list):
        for comp in components:
            comp_type = type(comp).__name__.lower()
            if "image" in comp_type:
                url = first_attr(comp, "url", "file", "path")
                if url:
                    attachments.append({
                        "type": "image",
                        "url": str(url),
                    })
    return attachments


def action_to_astrbot_result(event: AstrMessageEvent, action: dict[str, Any]) -> list[Any]:
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
