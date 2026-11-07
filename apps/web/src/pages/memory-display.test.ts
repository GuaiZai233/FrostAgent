import assert from 'node:assert/strict';
import test from 'node:test';
import { memoryDisplayContent, memoryEvidenceText } from './memory-display.ts';

test('memory preview uses summary with legacy and manual revision fallbacks', () => {
  const entry = {
    source: 'extract',
    content: '我每个周末都会去游泳',
    summary: '群友喜欢周末游泳',
  };
  assert.equal(memoryDisplayContent(entry), '群友喜欢周末游泳');
  assert.equal(
    memoryDisplayContent({ ...entry, summary: '  ' }),
    entry.content,
  );
  assert.equal(
    memoryDisplayContent({ source: 'distill', content: '旧记忆' }),
    '旧记忆',
  );
  assert.equal(
    memoryDisplayContent({ ...entry, source: 'manual', content: '已改为跑步' }),
    '已改为跑步',
  );
});

test('image evidence shows the source notice while text evidence stays verbatim', () => {
  const entry = {
    source: 'extract',
    content: '图片上写着活动周六举行',
    evidence: '活动周六举行',
  };
  assert.equal(
    memoryEvidenceText({ ...entry, sourceMedia: 'image' }),
    '（原source为图片形式）',
  );
  assert.equal(memoryEvidenceText(entry), '活动周六举行');
  assert.equal(
    memoryEvidenceText({ ...entry, evidence: '【图片内容】：这只是用户输入' }),
    '【图片内容】：这只是用户输入',
  );
});
