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

// Image placeholders become hover previews only when keys match one-to-one.
import { renderMessageContent } from '../src/components/prompt-inspector';

const hashA = 'a'.repeat(64);
const hashB = 'b'.repeat(64);
const twoImages = renderMessageContent('看 [图片] 和 [image https://x/y.png] <b>', [hashA, hashB]);
assert.equal((twoImages.match(/class="msg-image-ref"/g) ?? []).length, 2);
assert.ok(twoImages.includes(`/api/log-images/${hashA}`) && twoImages.includes(`/api/log-images/${hashB}`));
assert.ok(twoImages.includes('&lt;b&gt;'), 'text must stay escaped');
// Missing key for one image: that placeholder stays plain, others still preview.
const partial = renderMessageContent('[图片][图片]', ['', hashB]);
assert.equal((partial.match(/class="msg-image-ref"/g) ?? []).length, 1);
// Count mismatch or no hashes: no previews at all.
assert.equal(renderMessageContent('[图片]', [hashA, hashB]), '[图片]');
assert.equal(renderMessageContent('[图片]'), '[图片]');
assert.equal(renderMessageContent('[bad]', ['x']), '[bad]');

// Transcriptions render under the message only when a transcription model ran.
import { renderMessageTranscriptions, renderSingleMessageRow } from '../src/components/prompt-inspector';

assert.equal(renderMessageTranscriptions({}), '');
assert.equal(renderMessageTranscriptions({ imageDescription: '  ', mediaDescription: '' }), '');
const transcribed = renderMessageTranscriptions({ imageDescription: '一张海报', mediaDescription: '说话内容：<明天见>' });
assert.equal((transcribed.match(/class="msg-transcription /g) ?? []).length, 2);
assert.ok(transcribed.includes('图片识别结果') && transcribed.includes('音视频转述结果'));
assert.ok(transcribed.includes('说话内容：&lt;明天见&gt;'), 'transcription must stay escaped');
const voiceRow = renderSingleMessageRow({ role: 'user', content: '[语音]', rawText: '[语音]', mediaDescription: '说话内容：明天见' }, false);
assert.ok(voiceRow.includes('[语音]') && voiceRow.includes('说话内容：明天见'));
