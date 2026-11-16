import test from 'node:test';
import assert from 'node:assert/strict';
import { balanceQueryOrigin, parseBalanceQueryTarget } from './balance-query-request.ts';

const endpoint = 'https://newapi.example/api/user/self';
const target = { origin: 'https://newapi.example', path: '/api/user/self' };

test('identifies the request URL instead of URLs in headers or data', () => {
  for (const curl of [
    `curl -H 'Referer: https://old.example/api/usage/token/' ${endpoint}`,
    `curl --header='Referer: https://old.example/api/usage/token/' --url '${endpoint}'`,
    `curl -H'Referer: https://old.example/api/usage/token/' --url=${endpoint}`,
    `curl --data '{"url":"https://old.example/api/usage/token/"}' -X POST ${endpoint}`,
    `curl --data-raw='https://old.example/api/usage/token/' -XPOST ${endpoint}`,
    `curl --data-binary 'https://old.example/api/usage/token/' --request=POST ${endpoint}`,
    `curl -dhttps://old.example/api/usage/token/ --get ${endpoint}`,
  ]) assert.deepEqual(parseBalanceQueryTarget(curl), target, curl);
});

test('supports quoted targets, curl.exe, flags and line continuations', () => {
  for (const curl of [
    `curl '${endpoint}' -H 'Authorization: Bearer {{API_KEY}}'`,
    `curl.exe -s -S -G --url "${endpoint}"`,
    `curl --silent --show-error --get ${endpoint}`,
    `curl \\\n  --url ${endpoint} \\\n -H 'Authorization: Bearer {{API_KEY}}'`,
    `curl \\\r\n  --url ${endpoint} \\\r\n -H 'Authorization: Bearer {{API_KEY}}'`,
    `curl https://newapi.example/api/'user'/self`,
  ]) assert.deepEqual(parseBalanceQueryTarget(curl), target, curl);
});

test('matches backend escape and concatenated quote rules', () => {
  assert.deepEqual(parseBalanceQueryTarget(`curl https://newapi.example/api/user/se\\lf`), target);
  assert.deepEqual(parseBalanceQueryTarget(`curl "https://newapi.example/api/user/se\\lf"`), {
    origin: target.origin, path: '/api/user/se\\lf',
  });
  assert.deepEqual(parseBalanceQueryTarget(`curl 'https://newapi.example/api/user/se\\lf'`), {
    origin: target.origin, path: '/api/user/se\\lf',
  });
  assert.deepEqual(parseBalanceQueryTarget(`curl -H "X-Example: say \\"hello\\"" ${endpoint}`), target);
});

test('preserves explicit ports and raw IPv6 authority while ignoring case and base paths', () => {
  for (const [url, origin] of [
    ['HTTPS://NewAPI.Example:443/v1', 'https://newapi.example:443'],
    ['http://NewAPI.Example:80/v1?query=1', 'http://newapi.example:80'],
    ['https://newapi.example:8443/v1', 'https://newapi.example:8443'],
    ['https://[2001:DB8::1]:443/v1', 'https://[2001:db8::1]:443'],
    ['https://newapi.example', 'https://newapi.example'],
  ]) {
    assert.equal(balanceQueryOrigin(url), origin);
    assert.equal(parseBalanceQueryTarget(`curl '${url}'`)?.origin, origin);
  }
  assert.equal(balanceQueryOrigin('  https://newapi.example/v1  '), target.origin);
  assert.notEqual(balanceQueryOrigin('https://newapi.example:443/v1'), target.origin);
});

test('excludes queries, decodes path escapes and does not clean dot segments', () => {
  assert.deepEqual(parseBalanceQueryTarget(`curl '${endpoint}?redirect=https://other.example/api/usage/token/&key={{API_KEY}}'`), target);
  assert.deepEqual(parseBalanceQueryTarget('curl https://newapi.example/api/user/%73elf'), target);
  assert.deepEqual(parseBalanceQueryTarget('curl https://newapi.example/api/other/../user/self'), {
    origin: target.origin, path: '/api/other/../user/self',
  });
  assert.deepEqual(parseBalanceQueryTarget('curl https://newapi.example'), { origin: target.origin, path: '' });
});

test('rejects missing, duplicate, unsupported or incomplete request targets', () => {
  for (const curl of [
    '', 'curl', `wget ${endpoint}`, 'curl -H Referer:https://newapi.example',
    `curl ${endpoint} https://other.example`, `curl --url ${endpoint} --url ${endpoint}`,
    `curl ${endpoint} --url=${endpoint}`, `curl --url= ${endpoint}`,
    `curl -L ${endpoint}`, `curl --proxy https://proxy.example ${endpoint}`,
    `curl ${endpoint} -H`, `curl ${endpoint} --url`, `curl '${endpoint}`,
    `curl ${endpoint}\\`, `curl ${endpoint}; echo secret`, `curl ${endpoint} | cat`,
    `curl ${endpoint}\0`, `curl ${endpoint} ${'-s '.repeat(256)}`,
    `curl ${endpoint} -d '${'x'.repeat(65536)}'`,
  ]) assert.equal(parseBalanceQueryTarget(curl), null, curl.slice(0, 200));
});

test('rejects non-HTTP, credential-bearing and malformed URL targets', () => {
  for (const url of [
    'ftp://newapi.example/self', '/api/user/self', 'https:///api/user/self',
    'https://user:password@newapi.example/self', `${endpoint}#fragment`,
    'https://newapi.example:bad/self', 'https://newapi.example/%zz',
    'https://newapi.example\\other.example/self', 'https://{{API_KEY}}/self',
    'https://newapi.example/{{API_KEY}}', 'https://newapi.example/\nself',
  ]) {
    assert.equal(balanceQueryOrigin(url), '', url);
    assert.equal(parseBalanceQueryTarget(`curl '${url}'`), null, url);
  }
});
