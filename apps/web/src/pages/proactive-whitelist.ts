export interface WhitelistAPI {
  updateEnvVar: (params: {
    key: string;
    value: string;
    isSecret: boolean;
  }) => Promise<{ success: boolean; error?: string }>;
}

export interface WhitelistState {
  enabled: boolean;
  groups: string[];
  explicitSwitch?: 'true' | 'false' | null;
}

export interface WhitelistSyncListener {
  onStateChange: (
    state: WhitelistState & { isSaving: boolean; isUnverified: boolean },
  ) => void;
  onError: (err: Error) => void;
  onSuccess: (msg: string) => void;
  onReloadNeeded: () => Promise<void>;
}

export function parseExplicitWhitelistSwitch(
  raw: string | undefined | null,
): 'true' | 'false' | null {
  if (raw === undefined || raw === null) return null;
  const trimmed = raw.trim().toLowerCase();
  if (trimmed === 'true') return 'true';
  if (trimmed === 'false') return 'false';
  return null;
}

export function isWhitelistEnabled(
  enabledVal: string | undefined | null,
  groupCount: number,
): boolean {
  if (enabledVal === undefined || enabledVal === null) {
    return groupCount > 0;
  }
  const norm = enabledVal.trim().toLowerCase();
  if (norm === 'true') {
    return true;
  }
  if (norm === 'false') {
    return false;
  }
  return groupCount > 0;
}

export function groupIdFromSessionId(sessionId: string): string {
  const lower = sessionId.toLowerCase();
  if (lower.startsWith('group:')) return sessionId.slice('group:'.length);
  const marker = ':group:';
  const index = lower.lastIndexOf(marker);
  if (index < 0) return '';
  const prefix = sessionId.slice(0, index).toLowerCase();
  const rawId = sessionId.slice(index + marker.length);
  // Normalize QQ/OneBot platforms to bare IDs
  if (
    prefix === 'onebot' ||
    prefix === 'qq' ||
    prefix === 'aiocqhttp' ||
    prefix === ''
  ) {
    return rawId;
  }
  // Keep platform-qualified identity for other platforms (e.g. telegram, discord)
  return `${prefix}:${rawId}`;
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

function areGroupArraysEqual(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

export class ProactiveWhitelistSync {
  private api: WhitelistAPI;
  private listeners: WhitelistSyncListener;
  private current: WhitelistState;
  private lastSaved: WhitelistState;
  private explicitSwitch: 'true' | 'false' | null = null;
  private lastSavedExplicitSwitch: 'true' | 'false' | null = null;
  private isSaving = false;
  private isUnverified = false;
  private pendingTarget: WhitelistState | null = null;
  private opPromise: Promise<void> = Promise.resolve();
  private loadSeqCounter = 0;
  private minValidLoadSeq = 0;
  private latestAcceptedLoadSeq = 0;

  constructor(
    api: WhitelistAPI,
    initial: WhitelistState,
    listeners: WhitelistSyncListener,
  ) {
    this.api = api;
    this.current = {
      enabled: initial.enabled,
      groups: [...initial.groups],
    };
    this.lastSaved = {
      enabled: initial.enabled,
      groups: [...initial.groups],
    };
    this.explicitSwitch = initial.explicitSwitch ?? null;
    this.lastSavedExplicitSwitch = initial.explicitSwitch ?? null;
    this.listeners = listeners;
  }

  getState(): WhitelistState & { isSaving: boolean; isUnverified: boolean } {
    return {
      enabled: this.current.enabled,
      groups: [...this.current.groups],
      isSaving: this.isSaving,
      isUnverified: this.isUnverified,
    };
  }

  nextLoadSeq(): number {
    return ++this.loadSeqCounter;
  }

  private invalidatePreEditLoads(): void {
    this.minValidLoadSeq = this.loadSeqCounter + 1;
  }

  applyServerConfig(
    enabled: boolean,
    groups: string[],
    seq: number,
    explicitSwitch?: 'true' | 'false' | null,
  ): boolean {
    if (seq < this.minValidLoadSeq || seq < this.latestAcceptedLoadSeq) {
      return false; // Stale load response
    }
    this.latestAcceptedLoadSeq = seq;
    if (this.isSaving || this.pendingTarget !== null) {
      // Don't overwrite active user edits while save is in flight
      return false;
    }
    this.explicitSwitch = explicitSwitch !== undefined ? explicitSwitch : null;
    this.lastSavedExplicitSwitch = this.explicitSwitch;
    this.isUnverified = false;
    this.current = {
      enabled,
      groups: [...groups],
    };
    this.lastSaved = {
      enabled,
      groups: [...groups],
    };
    this.listeners.onStateChange(this.getState());
    return true;
  }

  async toggleEnabled(enabled: boolean): Promise<void> {
    const baseGroups = this.pendingTarget !== null
      ? this.pendingTarget.groups
      : this.current.groups;
    return this.setTarget({
      enabled,
      groups: [...baseGroups],
    });
  }

  async addGroup(groupId: string): Promise<void> {
    const trimmed = groupId.trim();
    if (!trimmed) return;
    const base = this.pendingTarget !== null ? this.pendingTarget : this.current;
    if (base.groups.includes(trimmed)) return;
    return this.setTarget({
      enabled: base.enabled,
      groups: [...base.groups, trimmed],
    });
  }

  async removeGroup(groupId: string): Promise<void> {
    const trimmed = groupId.trim();
    const base = this.pendingTarget !== null ? this.pendingTarget : this.current;
    if (!base.groups.includes(trimmed)) return;
    return this.setTarget({
      enabled: base.enabled,
      groups: base.groups.filter((g) => g !== trimmed),
    });
  }

  async setTarget(target: WhitelistState): Promise<void> {
    const nextTarget: WhitelistState = {
      enabled: target.enabled,
      groups: [...target.groups],
    };

    this.invalidatePreEditLoads();

    // Optimistically update current state and notify UI
    this.current = {
      enabled: nextTarget.enabled,
      groups: [...nextTarget.groups],
    };
    this.pendingTarget = nextTarget;
    this.listeners.onStateChange(this.getState());

    if (this.isSaving) {
      return this.opPromise;
    }

    this.opPromise = this.drainPending();
    return this.opPromise;
  }

  private async drainPending(): Promise<void> {
    this.isSaving = true;
    this.listeners.onStateChange(this.getState());

    while (this.pendingTarget !== null) {
      const target = this.pendingTarget;
      this.pendingTarget = null;
      this.listeners.onStateChange(this.getState());

      try {
        await this.executeSave(target);
        this.current = {
          enabled: target.enabled,
          groups: [...target.groups],
        };
        this.lastSaved = {
          enabled: target.enabled,
          groups: [...target.groups],
        };
        this.lastSavedExplicitSwitch = this.explicitSwitch;
        this.isUnverified = false;
        this.invalidatePreEditLoads();
      } catch (err) {
        const error = err instanceof Error ? err : new Error(String(err));
        // Definite rejected writes: restore last known-confirmed saved state immediately (N4)
        this.current = {
          enabled: this.lastSaved.enabled,
          groups: [...this.lastSaved.groups],
        };
        this.explicitSwitch = this.lastSavedExplicitSwitch;
        this.pendingTarget = null;
        this.isSaving = false;
        this.isUnverified = true;
        this.listeners.onStateChange(this.getState());
        this.listeners.onError(error);
        try {
          await this.listeners.onReloadNeeded();
        } catch {
          // Transport failure prevented confirming server state;
          // UI visibly retains unverified state with lastSaved restored
        }
        return;
      }
    }

    this.isSaving = false;
    this.invalidatePreEditLoads();
    this.listeners.onStateChange(this.getState());
  }

  private async executeSave(target: WhitelistState): Promise<void> {
    const desiredSwitch: 'true' | 'false' = target.enabled ? 'true' : 'false';
    const switchNeedsUpdate = this.explicitSwitch !== desiredSwitch;
    const groupsChanged = !areGroupArraysEqual(target.groups, this.lastSaved.groups);

    // N1: Persist the explicit desired switch BEFORE changes that cross the implicit enable boundary
    // or when the switch state changed, guaranteeing backend invariant is never inverted.
    if (switchNeedsUpdate) {
      const res = await this.api.updateEnvVar({
        key: 'ENABLE_PROACTIVE_REPLY_WHITELIST',
        value: desiredSwitch,
        isSecret: false,
      });
      if (!res.success) {
        throw new Error(res.error || '更新白名单开关失败');
      }
      this.explicitSwitch = desiredSwitch;
    }

    if (groupsChanged) {
      const groupsStr = target.groups.join(', ');
      const res = await this.api.updateEnvVar({
        key: 'PROACTIVE_REPLY_GROUP_WHITELIST',
        value: groupsStr,
        isSecret: false,
      });
      if (!res.success) {
        throw new Error(res.error || '更新白名单群聊列表失败');
      }
    }

    const toggleChanged = target.enabled !== this.lastSaved.enabled;
    if (toggleChanged && groupsChanged) {
      this.listeners.onSuccess(
        target.enabled
          ? '已开启白名单模式并更新群聊列表'
          : '已停用白名单模式并更新群聊列表',
      );
    } else if (toggleChanged) {
      this.listeners.onSuccess(
        target.enabled ? '已开启主动回复群聊白名单模式' : '已停用主动回复群聊白名单模式',
      );
    } else if (groupsChanged) {
      this.listeners.onSuccess('白名单群聊列表已更新');
    }
  }
}
