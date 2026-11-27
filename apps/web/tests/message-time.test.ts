import assert from 'node:assert/strict';
import { parseMessageLine } from '../src/components/prompt-inspector';
import { formatMessageTime } from '../src/utils/formatters';

const now = new Date(2001, 1, 3, 18, 0, 0);

assert.equal(formatMessageTime('2001-02-03 15:04:05', now), '今天 15:04:05');
assert.equal(formatMessageTime('2001-02-02 15:04:05', now), '2001-02-02 15:04:05');
assert.equal(formatMessageTime('2000-02-03 15:04:05', now), '2000-02-03 15:04:05');
// Legacy persisted records only carry the clock part.
assert.equal(formatMessageTime('15:04:05', now), '15:04:05');

// Date-qualified time tags in text history lines are still recognised as time.
const parsed = parseMessageLine('[user] [2001-02-03 15:04:05] 张三 (123): 你好');
assert.equal(parsed.time, '2001-02-03 15:04:05');
assert.equal(parsed.content, '你好');
