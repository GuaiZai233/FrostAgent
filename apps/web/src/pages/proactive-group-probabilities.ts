export const groupProbabilitiesKey = 'PROACTIVE_REPLY_GROUP_PROBABILITIES';

export function normalizeGroupProbability(value: unknown): number {
  const text = typeof value === 'string' ? value.trim() : typeof value === 'number' ? String(value) : '';
  const probability = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(text) ? Number(text) : Number.NaN;
  if (!Number.isFinite(probability) || probability < 0) return 0.01;
  return Math.min(probability, 1);
}

export function groupProbabilityIdentity(id: string): string {
  id = id.trim();
  const colon = id.indexOf(':');
  const platform = colon < 0 ? 'qq' : id.slice(0, colon).toLowerCase();
  const group = colon < 0 ? id : id.slice(colon + 1);
  return `${['onebot', 'aiocqhttp'].includes(platform) ? 'qq' : platform}:${group}`;
}

export function parseGroupProbabilities(raw: string): Map<string, number> {
  if (!raw.trim()) return new Map();
  const parsed: unknown = JSON.parse(raw);
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('群聊主动回复概率必须为 JSON 对象');
  }
  const result = new Map<string, number>();
  for (const [id, value] of Object.entries(parsed)) {
    const trimmed = id.trim();
    if (!trimmed || /[,;\s\0]/.test(trimmed) || trimmed.startsWith(':') || trimmed.indexOf(':') === trimmed.length - 1) {
      throw new Error('群聊主动回复概率包含无效群标识');
    }
    const key = groupProbabilityIdentity(trimmed);
    if (result.has(key)) throw new Error('群聊主动回复概率包含重复群标识');
    result.set(key, normalizeGroupProbability(value));
  }
  return result;
}

export function serializeGroupProbabilities(values: Map<string, number>): string {
  return values.size ? JSON.stringify(Object.fromEntries([...values].sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0))) : '';
}
