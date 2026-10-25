import type {
  ComputerConfigUpdate,
  ComputerContent,
} from '../api/computer-use';

export function parseProjectEnvironment(value: string): Record<string, string> {
  if (!value.trim()) return {};
  const parsed: unknown = JSON.parse(value);
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object')
    throw new Error(
      '环境变量须为 JSON 对象，例如 {"PATH":"C:\\\\Tools\\\\bin"}',
    );
  const result: Record<string, string> = Object.create(null);
  for (const [key, entry] of Object.entries(parsed)) {
    if (
      !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key) ||
      typeof entry !== 'string' ||
      entry.includes('\0') ||
      ['__proto__', 'constructor', 'prototype'].includes(key)
    )
      throw new Error('环境变量名须为字母、数字或下划线，值须为字符串');
    result[key] = entry;
  }
  return result;
}

export function withTokenUpdate(
  config: ComputerConfigUpdate,
  input: string,
  clear: boolean,
  adapterInput = '',
  clearAdapter = false,
): ComputerConfigUpdate {
  const result = { ...config };
  delete result.token;
  if (clear) result.token = '';
  else if (input.trim()) result.token = input.trim();
  delete result.adapter_token;
  if (clearAdapter) result.adapter_token = '';
  else if (adapterInput.trim()) result.adapter_token = adapterInput.trim();
  return result;
}

export function taskStateLabel(state: string): string {
  const labels: Record<string, string> = {
    queued: '排队中',
    running: '执行中',
    pausing: '暂停确认中',
    paused: '已暂停',
    succeeded: '已完成',
    completed: '已完成',
    failed: '失败',
    unknown: '执行结果未知',
    cancelled: '已取消',
  };
  return Object.hasOwn(labels, state) ? labels[state] : state;
}

export function validatedPreview(
  content: ComputerContent,
  now = Date.now(),
): { text: string; image: string; expires: number } {
  const expires = Date.parse(content.expires_at);
  if (!Number.isFinite(expires) || expires <= now)
    throw new Error('临时内容已过期或不可用，不会重新执行操作。');
  if (content.text && content.text.length > 1024 * 1024)
    throw new Error('临时文本超过预览大小限制');
  if (
    content.png &&
    (content.png.length > 12 * 1024 * 1024 ||
      !content.png.startsWith('iVBORw0KGgo') ||
      !/^[A-Za-z0-9+/]+={0,2}$/.test(content.png))
  )
    throw new Error('截图格式无效');
  return {
    text: content.text || '',
    image: content.png ? 'data:image/png;base64,' + content.png : '',
    expires,
  };
}
