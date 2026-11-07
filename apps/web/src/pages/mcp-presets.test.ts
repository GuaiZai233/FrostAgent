import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createDefaultDraft,
  draftToJSON,
  parseJSONToDraft,
} from './mcp-config.ts';
import { MCP_PRESETS, createMCPPresetDraft } from './mcp-presets.ts';

test('MCP presets contain only AnySearch and GitHub official HTTP endpoints', () => {
  assert.deepEqual(
    MCP_PRESETS.map(({ id, name, url }) => ({ id, name, url })),
    [
      {
        id: 'anysearch',
        name: 'AnySearch',
        url: 'https://api.anysearch.com/mcp',
      },
      {
        id: 'github',
        name: 'GitHub',
        url: 'https://api.githubcopilot.com/mcp/',
      },
    ],
  );
});

for (const preset of MCP_PRESETS) {
  test(`${preset.id} fills a clean enabled HTTP draft and round-trips through JSON`, () => {
    const draft = createMCPPresetDraft(preset.id, 'synthetic-token');
    assert.deepEqual(draft, {
      ...createDefaultDraft(),
      id: preset.id,
      name: preset.name,
      transportType: 'streamable_http',
      url: preset.url,
      headers: { ...preset.headers, Authorization: 'Bearer synthetic-token' },
    });
    const result = parseJSONToDraft(
      draftToJSON(draft),
      createDefaultDraft(),
      false,
    );
    assert.equal(result.success, true);
    assert.deepEqual(result.draft, draft);
  });
}

test('bare and prefixed tokens are normalized without duplicating Bearer', () => {
  for (const input of [
    'synthetic-token',
    ' synthetic-token ',
    'Bearer synthetic-token',
    ' bearer   synthetic-token ',
  ]) {
    assert.equal(
      createMCPPresetDraft('anysearch', input).headers.Authorization,
      'Bearer synthetic-token',
    );
  }
});

test('missing and malformed tokens are rejected without disclosing the input', () => {
  for (const input of ['', '   ', 'Bearer', 'Bearer ', 'bearer\t']) {
    assert.throws(
      () => createMCPPresetDraft('github', input),
      /^Error: 请填写自己的 Bearer Token$/,
    );
  }
  for (const input of [
    'synthetic token',
    'synthetic\ttoken',
    'synthetic\r\nX-Injected: value',
    'synthetic\0token',
    'synthetic\u007ftoken',
  ]) {
    assert.throws(
      () => createMCPPresetDraft('github', input),
      /^Error: Bearer Token 不能包含空白或控制字符$/,
    );
  }
});

test('presets allocate an unused ID without overwriting an existing server', () => {
  const occupiedIds = ['anysearch', 'anysearch_2', 'anysearch_4', 'github'];
  assert.equal(
    createMCPPresetDraft('anysearch', 'synthetic-token', occupiedIds).id,
    'anysearch_3',
  );
  assert.equal(
    createMCPPresetDraft('github', 'synthetic-token', occupiedIds).id,
    'github_2',
  );
  assert.deepEqual(occupiedIds, [
    'anysearch',
    'anysearch_2',
    'anysearch_4',
    'github',
  ]);
});

test('fresh drafts never share credentials or mutable fields across applications', () => {
  const first = createMCPPresetDraft('anysearch', 'synthetic-first');
  first.headers.Authorization = 'Bearer changed';
  first.headers['X-Anysearch-Client'] = 'changed';
  first.headers['X-Private'] = 'synthetic-private';
  first.args.push('old-command-argument');
  first.env.PRIVATE_TOKEN = 'synthetic-private';

  const next = createMCPPresetDraft('anysearch', 'synthetic-next');
  const github = createMCPPresetDraft('github', 'synthetic-github');
  assert.deepEqual(next.headers, {
    'X-Anysearch-Client': 'mcp/1.0.0',
    Authorization: 'Bearer synthetic-next',
  });
  assert.deepEqual(github.headers, {
    Authorization: 'Bearer synthetic-github',
  });
  for (const draft of [next, github]) {
    assert.deepEqual(draft.args, []);
    assert.deepEqual(draft.env, {});
    assert.equal(draft.command, '');
    assert.equal(draft.workingDir, '');
  }
});
