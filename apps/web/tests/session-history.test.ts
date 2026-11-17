import assert from 'node:assert/strict';
import { createTestDOM, emit, query, settle } from './dom-fixture';
import { historyDateRange, openSessionHistoryDialog, supportsSessionHistory } from '../src/components/session-history';
import type { createInstanceAPI } from '../src/api/client';

for (const id of [
  'group:synthetic-group', 'private:synthetic-user',
  'qq:group:synthetic-group', 'onebot:private:synthetic-user',
  'fixture:group:synthetic-group', 'fixture:private:synthetic-user:group:opaque',
  'group:opaque:private:target', ' private:synthetic-user ',
  'mock:private:synthetic-user', 'mock:group:synthetic-group', 'MOCK:group:synthetic-group',
]) assert.equal(supportsSessionHistory(id), true, id);
for (const id of [
  '', 'discord_group_synthetic-group', 'onebot_private_synthetic-user', 'unstructured_fixture',
  'fixture:thread:synthetic-thread', 'group:', 'private:  ', 'fixture:private:',
  ':group:synthetic-group', '  :private:synthetic-user',
  'fixture:prefix:private:synthetic-user', 'private:' + 'x'.repeat(2048),
  'private:' + '文'.repeat(700),
]) assert.equal(supportsSessionHistory(id), false, id);

assert.deepEqual(historyDateRange('', ''), { after: '', before: '' });
assert.throws(() => historyDateRange('2026-01-03', '2026-01-02'), /开始日期/);
assert.throws(() => historyDateRange('2026-02-30', ''), /有效日期/);
assert.throws(() => historyDateRange('invalid', ''), /有效日期/);
const local = historyDateRange('2026-01-02', '2026-01-02');
assert.equal(new Date(local.after).getDate(), 2);
assert.equal(new Date(local.after).getHours(), 0);
assert.equal(new Date(local.before).getDate(), 3);
assert.equal(new Date(local.before).getHours(), 0);
const oldTZ = process.env.TZ;
try {
  process.env.TZ = 'America/New_York';
  const dst = historyDateRange('2026-03-08', '2026-03-08');
  assert.equal(Date.parse(dst.before) - Date.parse(dst.after), 23 * 60 * 60 * 1000, 'DST day must use calendar boundaries');
} finally {
  if (oldTZ === undefined) delete process.env.TZ;
  else process.env.TZ = oldTZ;
}

const dom = createTestDOM();
try {
  const calls: Array<{ id: string; after: string; before: string; token: string; signal?: AbortSignal }> = [];
  let fail = false;
  let pending: ((value: unknown) => void) | undefined;
  const record = {
    id: 'record-fixture', messageId: 'msg-fixture', senderId: 'synthetic-user', senderName: '<b>Fixture</b>',
    role: 'user', content: '<script>untrusted</script>\n第二行', occurredAt: '2026-01-02T03:00:00Z',
    deliveryStatus: 'received', replyTo: '<source>',
  };
  const toolLikeOriginal = '【工具执行记录】\n- 工具调用: send_message (ref_id: tool_fixture)';
  const api = {
    async getSessionHistory(id: string, after: string, before: string, token: string, signal?: AbortSignal) {
      calls.push({ id, after, before, token, signal });
      if (fail) throw new Error('synthetic archive unavailable');
      if (pending) return new Promise(resolve => { pending = resolve; });
      if (after) return { records: [], nextPageToken: '' };
      return token
        ? { records: [{ ...record, id: 'older', content: 'older original' }], nextPageToken: '' }
        : { records: [record, { ...record, role: 'assistant', senderName: '', senderId: '', content: toolLikeOriginal, deliveryStatus: 'sent' }], nextPageToken: 'next-fixture' };
    },
  } as unknown as Pick<ReturnType<typeof createInstanceAPI>, 'getSessionHistory'>;

  const close = openSessionHistoryDialog(api, 'private:synthetic-user', 'Configured Bot');
  const dialog = dom.dialogs.at(-1)!;
  await settle();
  assert.equal(calls[0].id, 'private:synthetic-user');
  assert.equal(calls[0].after, '');
  const records = query(dialog, '#history-records');
  assert.match(records.innerHTML, /group-msg-row is-uncompacted is-user/);
  assert.match(records.innerHTML, /group-msg-row is-uncompacted is-assistant/);
  assert.match(records.innerHTML, /&lt;script&gt;untrusted&lt;\/script&gt;/);
  assert.match(records.innerHTML, /&lt;b&gt;Fixture&lt;\/b&gt;/);
  assert.match(records.innerHTML, /回复 #&lt;source&gt;/);
  assert.match(records.textContent!, /Configured Bot/);
  assert.match(records.textContent!, /未经平台确认/);
  assert.match(records.textContent!, /【工具执行记录】/, 'archive original must not become a tool annotation');
  assert.equal(records.querySelector('script'), null);
  assert.equal(records.querySelector('[data-tool-activity]'), null);
  assert.equal(query(dialog, '#history-prev').disabled, true);
  assert.equal(query(dialog, '#history-next').disabled, false);

  query(dialog, '.dialog-body').scrollTop = 100;
  query(dialog, '#history-next').click();
  await settle();
  assert.equal(calls.at(-1)!.token, 'next-fixture');
  assert.match(records.textContent!, /older original/);
  assert.equal(query(dialog, '.dialog-body').scrollTop, 0, 'page navigation must keep date filters in view');
  assert.equal(query(dialog, '#history-next').disabled, true);
  assert.equal(query(dialog, '#history-prev').disabled, false);
  query(dialog, '#history-prev').click();
  await settle();
  assert.equal(calls.at(-1)!.token, '');

  const start = query(dialog, '#history-start');
  const end = query(dialog, '#history-end');
  start.value = '2026-01-03';
  end.value = '2026-01-02';
  const beforeInvalid = calls.length;
  emit(query(dialog, '#history-filter'), 'submit');
  assert.equal(calls.length, beforeInvalid);
  assert.match(query(dialog, '#history-status').textContent!, /开始日期/);
  start.value = '2026-01-02';
  emit(query(dialog, '#history-filter'), 'submit');
  await settle();
  assert.deepEqual({ after: calls.at(-1)!.after, before: calls.at(-1)!.before }, historyDateRange(start.value, end.value));
  assert.equal(calls.at(-1)!.token, '', 'date query must reset pagination');
  assert.match(query(dialog, '#history-status').textContent!, /暂无已保存/);
  query(dialog, '#history-clear').click();
  await settle();
  assert.equal(start.value, '');
  assert.equal(end.value, '');
  assert.equal(calls.at(-1)!.after, '');

  fail = true;
  query(dialog, '#history-next').click();
  await settle();
  assert.match(query(dialog, '#history-status').textContent!, /synthetic archive unavailable/);
  fail = false;
  query(dialog, '#history-retry').click();
  await settle();
  assert.match(records.textContent!, /older original/);

  pending = () => {};
  query(dialog, '#history-clear').click();
  await settle();
  const oldResolve = pending!;
  const oldSignal = calls.at(-1)!.signal!;
  pending = undefined;
  start.value = '2026-01-02';
  emit(query(dialog, '#history-filter'), 'submit');
  await settle();
  assert.equal(oldSignal.aborted, true, 'changed date must cancel the previous read');
  oldResolve({ records: [{ ...record, content: 'stale content' }], nextPageToken: 'stale' });
  await settle();
  assert.doesNotMatch(records.textContent!, /stale content/);

  pending = () => {};
  query(dialog, '#history-clear').click();
  await settle();
  const lateResolve = pending!;
  const lateSignal = calls.at(-1)!.signal!;
  close();
  assert.equal(dialog.open, false);
  assert.equal(lateSignal.aborted, true, 'closing modal must cancel the read');
  const closedHTML = records.innerHTML;
  lateResolve({ records: [record], nextPageToken: '' });
  await settle();
  assert.equal(records.innerHTML, closedHTML, 'late response mutated closed modal');
  process.stdout.write('PASS: history date range, DST, pagination, originals, retry and cancellation\n');
} finally {
  dom.cleanup();
}
