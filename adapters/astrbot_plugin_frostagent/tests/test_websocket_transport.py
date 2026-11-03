from __future__ import annotations

import asyncio
import json
import unittest
from dataclasses import replace

from adapters.astrbot_plugin_frostagent.test_main import (
    FakeContext,
    FrostAgentWSClient,
    load_settings,
)

try:
    from websockets.exceptions import ConnectionClosed, InvalidStatusCode
    from websockets.legacy.client import connect
    from websockets.legacy.server import serve
except ImportError:
    connect = None


TOKEN = "synthetic-transport-token"


@unittest.skipIf(connect is None, "install the plugin websockets dependency for transport tests")
class ForwardTransportTest(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        settings = replace(load_settings({
            "ws_mode": "forward", "ws_access_token": TOKEN,
        }), ws_listen_port=0)
        self.context = FakeContext()
        self.client = FrostAgentWSClient(settings, self.context)
        await self.client.start()
        self.port = self.client._server.sockets[0].getsockname()[1]
        self.url = f"ws://127.0.0.1:{self.port}/"

    async def asyncTearDown(self):
        await asyncio.wait_for(self.client.stop(), 4)

    def authenticated(self, url=None, **kwargs):
        return connect(
            url or self.url, extra_headers={"Authorization": "Bearer " + TOKEN},
            close_timeout=1, **kwargs,
        )

    async def test_authentication_and_bidirectional_event_action(self):
        for headers in [None, {"Authorization": "Bearer wrong-token"}, [
            ("Authorization", "Bearer " + TOKEN),
            ("Authorization", "Bearer " + TOKEN),
        ]]:
            with self.subTest(headers=headers), self.assertRaises(InvalidStatusCode) as raised:
                async with connect(self.url, extra_headers=headers):
                    self.fail("unauthorized handshake succeeded")
            self.assertEqual(raised.exception.status_code, 401)

        async with self.authenticated() as ws:
            queue = self.client.register_waiter("synthetic-message")
            event = {"type": "event", "message_id": "synthetic-message", "content": "hello"}
            self.assertTrue(await self.client.send_event(event))
            self.assertEqual(json.loads(await asyncio.wait_for(ws.recv(), 2)), event)
            action = {"action": "noop", "echo": "reply_synthetic-message"}
            await ws.send(json.dumps(action))
            self.assertEqual(await asyncio.wait_for(queue.get(), 2), action)

    async def test_browser_origin_and_wrong_path_are_rejected(self):
        with self.assertRaises(InvalidStatusCode) as raised:
            async with self.authenticated(origin="http://malicious.invalid"):
                self.fail("browser handshake succeeded")
        self.assertEqual(raised.exception.status_code, 403)
        with self.assertRaises(InvalidStatusCode) as raised:
            async with self.authenticated(self.url + "wrong"):
                self.fail("wrong endpoint succeeded")
        self.assertEqual(raised.exception.status_code, 404)

    async def test_single_active_client_and_reconnection(self):
        async with self.authenticated() as first:
            old_queue = self.client.register_waiter("old-message")
            with self.assertRaises(InvalidStatusCode) as raised:
                async with self.authenticated():
                    self.fail("second FA client succeeded")
            self.assertEqual(raised.exception.status_code, 409)
            await first.close()
            self.assertEqual(await asyncio.wait_for(old_queue.get(), 2), {"type": "connection_closed"})
        self.assertEqual(self.client._pending_queues, {})
        async with self.authenticated() as replacement:
            event = {"type": "event", "message_id": "replacement-message"}
            self.assertTrue(await self.client.send_event(event))
            self.assertEqual(json.loads(await asyncio.wait_for(replacement.recv(), 2)), event)

    async def test_stop_closes_connection_tasks_listener_and_waiters(self):
        async with self.authenticated() as ws:
            queue = self.client.register_waiter("pending-message")
            heartbeat = self.client._heartbeat_task
            await asyncio.wait_for(self.client.stop(), 4)
            await asyncio.wait_for(ws.wait_closed(), 2)
            self.assertEqual(await asyncio.wait_for(queue.get(), 2), {"type": "connection_closed"})
            self.assertTrue(heartbeat.done())
            self.assertFalse(self.client._connection_tasks)
            self.assertIsNone(self.client.ws)
            self.assertIsNone(self.client._server)
            self.assertFalse(await self.client.send_event({"type": "event"}))
        listener = await asyncio.start_server(lambda _reader, writer: writer.close(), "127.0.0.1", self.port)
        listener.close()
        await listener.wait_closed()

    async def test_start_is_idempotent_and_restart_after_stop_works(self):
        server = self.client._server
        heartbeat = self.client._heartbeat_task
        await self.client.start()
        self.assertIs(self.client._server, server)
        self.assertIs(self.client._heartbeat_task, heartbeat)
        await self.client.stop()
        await self.client.start()
        port = self.client._server.sockets[0].getsockname()[1]
        async with self.authenticated(f"ws://127.0.0.1:{port}/"):
            self.assertIsNotNone(self.client.ws)

    async def test_stop_cancels_a_busy_action_dispatch(self):
        entered = asyncio.Event()

        async def slow_send(_umo, _chain):
            entered.set()
            await asyncio.Future()

        self.context.send_message = slow_send
        async with self.authenticated() as ws:
            await ws.send(json.dumps({
                "action": "send_message", "platform": "test",
                "message_type": "private", "user_id": "synthetic-user",
                "content": "synthetic reply",
            }))
            await asyncio.wait_for(entered.wait(), 2)
            await asyncio.wait_for(self.client.stop(), 4)
            self.assertFalse(self.client._connection_tasks)
            await asyncio.wait_for(ws.wait_closed(), 2)

    async def test_loopback_without_token_still_rejects_browser_origin(self):
        await self.client.stop()
        self.client = FrostAgentWSClient(replace(load_settings({"ws_mode": "forward"}), ws_listen_port=0), self.context)
        await self.client.start()
        port = self.client._server.sockets[0].getsockname()[1]
        url = f"ws://127.0.0.1:{port}/"
        with self.assertRaises(InvalidStatusCode) as raised:
            async with connect(url, origin="http://untrusted.invalid"):
                self.fail("unauthenticated browser origin succeeded")
        self.assertEqual(raised.exception.status_code, 403)
        async with connect(url):
            self.assertIsNotNone(self.client.ws)


@unittest.skipIf(connect is None, "install the plugin websockets dependency for transport tests")
class ReverseTransportTest(unittest.IsolatedAsyncioTestCase):
    async def test_default_reverse_round_trip_reconnect_and_stop(self):
        accepted = asyncio.Queue()
        received = asyncio.Queue()

        async def handle(ws):
            await accepted.put(ws)
            try:
                async for message in ws:
                    await received.put(json.loads(message))
            except ConnectionClosed:
                pass

        async with serve(handle, "127.0.0.1", 0) as server:
            port = server.sockets[0].getsockname()[1]
            settings = load_settings({
                "ws_url": f"ws://127.0.0.1:{port}/instances/a1b2c3d4/ws/astrbot",
                "reconnect_interval": 1,
            })
            client = FrostAgentWSClient(settings, FakeContext())
            try:
                await client.start()
                first = await asyncio.wait_for(accepted.get(), 2)
                queue = client.register_waiter("reverse-message")
                await first.send(json.dumps({"action": "noop", "echo": "reply_reverse-message"}))
                self.assertEqual((await asyncio.wait_for(queue.get(), 2))["action"], "noop")
                event = {"type": "event", "message_id": "reverse-message"}
                self.assertTrue(await client.send_event(event))
                self.assertEqual(await asyncio.wait_for(received.get(), 2), event)
                await first.close()
                self.assertEqual((await asyncio.wait_for(queue.get(), 2))["type"], "connection_closed")
                second = await asyncio.wait_for(accepted.get(), 3)
                self.assertIsNot(first, second)
                worker = client._worker_task
                heartbeat = client._heartbeat_task
                await asyncio.wait_for(client.stop(), 4)
                self.assertTrue(worker.done())
                self.assertTrue(heartbeat.done())
                self.assertIsNone(client.ws)
            finally:
                await client.stop()

    async def test_stop_cancels_an_incomplete_outbound_handshake(self):
        accepted = asyncio.Queue()

        async def handle(reader, writer):
            await accepted.put(writer)
            await reader.read()
            writer.close()
            await writer.wait_closed()

        listener = await asyncio.start_server(handle, "127.0.0.1", 0)
        port = listener.sockets[0].getsockname()[1]
        client = FrostAgentWSClient(load_settings({
            "ws_url": f"ws://127.0.0.1:{port}/instances/a1b2c3d4/ws/astrbot",
        }), FakeContext())
        writer = None
        try:
            await client.start()
            writer = await asyncio.wait_for(accepted.get(), 2)
            worker = client._worker_task
            await asyncio.wait_for(client.stop(), 2)
            self.assertTrue(worker.done())
            self.assertIsNone(client.ws)
        finally:
            await client.stop()
            if writer is not None:
                writer.close()
                await writer.wait_closed()
            listener.close()
            await listener.wait_closed()


if __name__ == "__main__":
    unittest.main()
