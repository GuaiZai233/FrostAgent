export const COMPACT_WHITELIST_ENABLED_KEY = 'ENABLE_GROUP_COMPACT_WHITELIST';
export const COMPACT_WHITELIST_GROUPS_KEY = 'GROUP_COMPACT_WHITELIST';

export interface CompactWhitelistChange {
  key: string;
  value: string;
}

/**
 * Plans the draft writes for a new group list. Mirrors the proactive whitelist rule that an
 * unset switch must be pinned to its current effective state before the list changes, so that
 * adding the first group does not implicitly flip compaction on (or removing the last one off).
 */
export function planCompactGroupChange(
  current: { enabledRaw: string | undefined; effectiveEnabled: boolean },
  nextGroups: string[],
): CompactWhitelistChange[] {
  const changes: CompactWhitelistChange[] = [];
  if (!current.enabledRaw) {
    changes.push({ key: COMPACT_WHITELIST_ENABLED_KEY, value: current.effectiveEnabled ? 'true' : 'false' });
  }
  changes.push({ key: COMPACT_WHITELIST_GROUPS_KEY, value: nextGroups.join(',') });
  return changes;
}
