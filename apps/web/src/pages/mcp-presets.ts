import { createDefaultDraft, type DraftServerConfig } from './mcp-config.ts';

export const MCP_PRESETS = [
  {
    id: 'anysearch',
    name: 'AnySearch',
    description: '联网搜索与网页内容提取，填写自己的 AnySearch API Key。',
    url: 'https://api.anysearch.com/mcp',
    headers: { 'X-Anysearch-Client': 'mcp/1.0.0' },
  },
  {
    id: 'github',
    name: 'GitHub',
    description:
      'GitHub 官方远程 MCP，填写自己的 Personal Access Token，仅授予所需权限。',
    url: 'https://api.githubcopilot.com/mcp/',
    headers: {},
  },
] as const;

export type MCPPresetId = (typeof MCP_PRESETS)[number]['id'];

export function createMCPPresetDraft(
  presetId: MCPPresetId,
  bearerToken: string,
  existingIds: readonly string[] = [],
): DraftServerConfig {
  const preset = MCP_PRESETS.find((entry) => entry.id === presetId);
  if (!preset) throw new Error('未知 MCP 预设');

  const token = bearerToken
    .trim()
    .replace(/^Bearer(?:\s+|$)/i, '')
    .trim();
  if (!token) throw new Error('请填写自己的 Bearer Token');
  if (
    /\s/.test(token) ||
    [...token].some(
      (char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127,
    )
  ) {
    throw new Error('Bearer Token 不能包含空白或控制字符');
  }

  let id: string = preset.id;
  const occupiedIds = new Set(existingIds);
  for (let suffix = 2; occupiedIds.has(id); suffix++) {
    id = `${preset.id}_${suffix}`;
  }

  return createDefaultDraft({
    id,
    name: preset.name,
    transportType: 'streamable_http',
    url: preset.url,
    headers: { ...preset.headers, Authorization: `Bearer ${token}` },
  });
}
