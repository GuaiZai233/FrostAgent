export function groupIdFromSessionId(sessionId: string): string {
  const lower = sessionId.toLowerCase();
  if (lower.startsWith('group:')) return sessionId.slice('group:'.length);
  const marker = ':group:';
  const index = lower.lastIndexOf(marker);
  return index >= 0 ? sessionId.slice(index + marker.length) : '';
}

export function parseWhitelistGroups(raw: string): string[] {
  if (!raw) return [];
  const parts = raw
    .split(/[,;\s]+/)
    .map((s) => s.trim())
    .filter(Boolean);
  return Array.from(new Set(parts));
}

export function formatGroupOption(groupId: string, groupName?: string): string {
  if (groupName && groupName.trim()) {
    return `${groupId}（${groupName.trim()}）`;
  }
  return groupId;
}
