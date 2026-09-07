import test from 'node:test';
import assert from 'node:assert/strict';
import {
  cleanJSON,
  createDefaultDraft,
  draftToJSON,
  parseJSONToDraft,
  parseEnvText,
  formatEnvMap,
  parseHeadersText,
  formatHeadersMap,
} from './mcp-config.ts';
import type { DraftServerConfig } from './mcp-config.ts';

test('cleanJSON preserves URLs and comments inside strings while stripping outside comments', () => {
  const input = `// Top-level comment
{
  "url": "https://example.com/mcp//test", /* inline comment */
  "comment_test": "a /* not a comment */ b // neither",
  "trailing_string": "test, } not trailing",
  "escaped": "quote \\" inside",
  "args": [
    "item1",
    "item2", // trailing comma below
  ],
}
`;

  const cleaned = cleanJSON(input);
  const parsed = JSON.parse(cleaned);

  assert.equal(parsed.url, 'https://example.com/mcp//test');
  assert.equal(parsed.comment_test, 'a /* not a comment */ b // neither');
  assert.equal(parsed.trailing_string, 'test, } not trailing');
  assert.equal(parsed.escaped, 'quote " inside');
  assert.deepEqual(parsed.args, ['item1', 'item2']);
});

test('cleanJSON strips trailing commas before closing braces', () => {
  const input = '{"a": 1, "b": [2, 3, ], }';
  const cleaned = cleanJSON(input);
  const parsed = JSON.parse(cleaned);
  assert.deepEqual(parsed, { a: 1, b: [2, 3] });
});

test('args are completely lossless and reversible across round-trips', () => {
  const originalArgs = [
    '--flag',
    'value with spaces',
    'value with "embedded quotes"',
    '', // empty string arg
    'https://example.com/path?foo=1&bar=2',
  ];

  const draft: DraftServerConfig = {
    id: 'test_srv',
    name: 'Test Server',
    enabled: true,
    transportType: 'stdio',
    command: 'run',
    args: originalArgs,
    workingDir: '/workspace/dir',
    env: { KEY: 'val' },
    url: '',
    headers: {},
  };

  const json = draftToJSON(draft);
  const result = parseJSONToDraft(json, draft, false);

  assert.equal(result.success, true);
  assert.ok(result.draft);
  assert.deepEqual(result.draft.args, originalArgs);
  assert.equal(result.draft.args[1], 'value with spaces');
  assert.equal(result.draft.args[2], 'value with "embedded quotes"');
  assert.equal(result.draft.args[3], '');
});

test('parseJSONToDraft resets deleted fields to clean defaults instead of retaining stale state', () => {
  const initialDraft: DraftServerConfig = {
    id: 'weather_svc',
    name: 'Weather Service',
    enabled: true,
    transportType: 'stdio',
    command: 'python',
    args: ['server.py', '--port', '8080'],
    workingDir: '/tmp/weather',
    env: { API_KEY: '******', DEBUG: '1' },
    url: 'http://localhost:8080',
    headers: { Authorization: 'Bearer test' },
  };

  // User deletes env, args, workingDir in JSON editor
  const minimalJSON = `{
    "id": "weather_svc",
    "name": "Weather Service Updated",
    "command": "python3"
  }`;

  const result = parseJSONToDraft(minimalJSON, initialDraft, true);
  assert.equal(result.success, true);
  assert.ok(result.draft);

  // Field reset verification
  assert.equal(result.draft.id, 'weather_svc');
  assert.equal(result.draft.name, 'Weather Service Updated');
  assert.equal(result.draft.command, 'python3');
  assert.deepEqual(result.draft.args, []); // args reset to empty
  assert.equal(result.draft.workingDir, ''); // workingDir reset to empty
  assert.deepEqual(result.draft.env, {}); // env reset to empty!
  assert.equal(result.draft.url, ''); // url reset
  assert.deepEqual(result.draft.headers, {}); // headers reset
});

test('parseJSONToDraft recognizes Claude Desktop mcpServers format', () => {
  const claudeDesktopJSON = `{
    "mcpServers": {
      "everything": {
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-everything"],
        "env": {
          "PORT": "3000"
        }
      },
      "second_server": {
        "command": "node"
      }
    }
  }`;

  const currentDraft = createDefaultDraft();
  const result = parseJSONToDraft(claudeDesktopJSON, currentDraft, false);

  assert.equal(result.success, true);
  assert.ok(result.draft);
  assert.equal(result.draft.id, 'everything');
  assert.equal(result.draft.command, 'npx');
  assert.deepEqual(result.draft.args, ['-y', '@modelcontextprotocol/server-everything']);
  assert.deepEqual(result.draft.env, { PORT: '3000' });
  assert.equal(result.draft.transportType, 'stdio');
  assert.match(result.message || '', /2 个服务器/);
});

test('parseJSONToDraft recognizes single-key wrapped object', () => {
  const wrappedJSON = `{
    "github_mcp": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "github-mcp"]
    }
  }`;

  const currentDraft = createDefaultDraft();
  const result = parseJSONToDraft(wrappedJSON, currentDraft, false);

  assert.equal(result.success, true);
  assert.ok(result.draft);
  assert.equal(result.draft.id, 'github_mcp');
  assert.equal(result.draft.command, 'docker');
  assert.deepEqual(result.draft.args, ['run', '-i', '--rm', 'github-mcp']);
});

test('parseJSONToDraft infers transport types accurately', () => {
  const draft = createDefaultDraft();

  // 1. Stdio
  const res1 = parseJSONToDraft('{"command": "npx"}', draft, false);
  assert.equal(res1.draft?.transportType, 'stdio');

  // 2. Streamable HTTP
  const res2 = parseJSONToDraft('{"url": "https://api.example.com/mcp"}', draft, false);
  assert.equal(res2.draft?.transportType, 'streamable_http');

  // 3. SSE
  const res3 = parseJSONToDraft('{"url": "https://api.example.com/sse"}', draft, false);
  assert.equal(res3.draft?.transportType, 'sse');

  // 4. Explicit transportType
  const res4 = parseJSONToDraft('{"transportType": "sse", "url": "https://api.example.com"}', draft, false);
  assert.equal(res4.draft?.transportType, 'sse');
});

test('env and headers serialization and parsing', () => {
  const envText = `
    # Comment line
    FOO=bar
    EMPTY=
    COMPLEX=a=b=c
  `;
  const envMap = parseEnvText(envText);
  assert.deepEqual(envMap, {
    FOO: 'bar',
    EMPTY: '',
    COMPLEX: 'a=b=c',
  });

  const formattedEnv = formatEnvMap(envMap);
  assert.match(formattedEnv, /FOO=bar/);
  assert.match(formattedEnv, /COMPLEX=a=b=c/);

  const headersText = `
    Authorization: Bearer my-token
    Content-Type: application/json
  `;
  const headersMap = parseHeadersText(headersText);
  assert.deepEqual(headersMap, {
    Authorization: 'Bearer my-token',
    'Content-Type': 'application/json',
  });

  const formattedHeaders = formatHeadersMap(headersMap);
  assert.match(formattedHeaders, /Authorization: Bearer my-token/);
});
