import { beforeAll, describe, expect, it } from 'vitest';

let utils: typeof import('./dashboard-utils');

beforeAll(async () => {
  const localizeGlobal = globalThis as typeof globalThis & {
    $localize: typeof $localize;
  };
  localizeGlobal.$localize = ((messageParts, ...expressions) =>
    String.raw({ raw: messageParts as unknown as string[] }, ...expressions)) as
    typeof $localize;
  localizeGlobal.$localize.locale = 'zh-CN';
  utils = await import('./dashboard-utils');
});

describe('dashboard utilities', () => {
  it('masks secrets without double masking server-provided masks', () => {
    expect(utils.maskSecret('abcdef1234')).toBe('******1234');
    expect(utils.maskSecret('abc')).toBe('****');
    expect(utils.maskSecret('******1234')).toBe('******1234');
  });

  it('tracks previous and next pagination tokens', () => {
    const stack = new utils.PageTokenStack();

    expect(stack.current()).toBe('');
    expect(stack.canGoBack()).toBe(false);

    stack.push('20');
    expect(stack.current()).toBe('20');
    expect(stack.canGoBack()).toBe(true);

    stack.back();
    expect(stack.current()).toBe('');

    stack.reset();
    expect(stack.canGoBack()).toBe(false);
  });
});
