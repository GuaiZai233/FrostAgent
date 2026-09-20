import test from 'node:test';
import assert from 'node:assert/strict';
import { dashboardWebSocketURL, instanceWebSocketURL } from './websocket-url.ts';

test('dashboardWebSocketURL creates same-origin ws URL for http origin (e.g. dev server :4200)', () => {
  const url = dashboardWebSocketURL(
    'http://localhost:4200',
    'inst_test_1',
    'onebot',
    'mock=true',
  );
  assert.equal(
    url,
    'ws://localhost:4200/instances/inst_test_1/ws/onebot?mock=true',
  );
});

test('dashboardWebSocketURL creates same-origin ws URL for http origin on default port :8080', () => {
  const url = dashboardWebSocketURL(
    'http://localhost:8080',
    'inst_test_2',
    'astrbot',
    'mock=true',
  );
  assert.equal(
    url,
    'ws://localhost:8080/instances/inst_test_2/ws/astrbot?mock=true',
  );
});

test('dashboardWebSocketURL creates secure wss URL for https origin', () => {
  const url = dashboardWebSocketURL(
    'https://dashboard.example',
    'inst_test_3',
    'onebot',
    'mock=true',
  );
  assert.equal(
    url,
    'wss://dashboard.example/instances/inst_test_3/ws/onebot?mock=true',
  );
});

test('dashboardWebSocketURL handles custom https port, trailing paths and hash fragments', () => {
  const url = dashboardWebSocketURL(
    'https://bot.example.com:8443/#/chat',
    'inst_test_4',
    'astrbot',
    '?mock=true',
  );
  assert.equal(
    url,
    'wss://bot.example.com:8443/instances/inst_test_4/ws/astrbot?mock=true',
  );
});

test('dashboardWebSocketURL handles calls without query string', () => {
  const url = dashboardWebSocketURL(
    'http://127.0.0.1:8080',
    'inst_test_5',
    'onebot',
  );
  assert.equal(
    url,
    'ws://127.0.0.1:8080/instances/inst_test_5/ws/onebot',
  );
});

test('instanceWebSocketURL preserves external listen address formatting for overview copy', () => {
  const onebotURL = instanceWebSocketURL(
    '127.0.0.1:1234',
    'inst_test_1',
    'onebot',
    'http://localhost:8080',
  );
  assert.equal(
    onebotURL,
    'ws://127.0.0.1:1234/instances/inst_test_1/ws/onebot',
  );

  const astrbotURL = instanceWebSocketURL(
    '0.0.0.0:1234',
    'inst_test_1',
    'astrbot',
    'http://dashboard.local:8080',
  );
  assert.equal(
    astrbotURL,
    'ws://dashboard.local:1234/instances/inst_test_1/ws/astrbot',
  );
});
