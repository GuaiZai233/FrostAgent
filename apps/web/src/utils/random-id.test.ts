import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from './random-id.ts';

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function withCrypto(value: unknown, fn: () => void) {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'crypto');
  Object.defineProperty(globalThis, 'crypto', { value, configurable: true });
  try {
    fn();
  } finally {
    if (descriptor) Object.defineProperty(globalThis, 'crypto', descriptor);
  }
}

test('randomUUID uses crypto.randomUUID when it is available', () => {
  const fixed = '11111111-2222-4333-8444-555555555555';
  withCrypto({ randomUUID: () => fixed, getRandomValues: () => { throw new Error('unused'); } }, () => {
    assert.equal(randomUUID(), fixed);
  });
});

test('randomUUID falls back to getRandomValues when randomUUID is missing (insecure context)', () => {
  const bytes = Uint8Array.from({ length: 16 }, (_, i) => i * 17);
  withCrypto(
    {
      getRandomValues: <T extends ArrayBufferView>(array: T): T => {
        new Uint8Array(array.buffer, array.byteOffset, array.byteLength).set(bytes);
        return array;
      },
    },
    () => {
      const id = randomUUID();
      assert.match(id, UUID_V4);
      assert.equal(id, '00112233-4455-4677-8899-aabbccddeeff');
    },
  );
});

test('randomUUID fallback produces distinct values', () => {
  const realGetRandomValues = globalThis.crypto.getRandomValues.bind(globalThis.crypto);
  withCrypto({ getRandomValues: realGetRandomValues }, () => {
    const ids = new Set(Array.from({ length: 50 }, () => randomUUID()));
    assert.equal(ids.size, 50);
    for (const id of ids) assert.match(id, UUID_V4);
  });
});
