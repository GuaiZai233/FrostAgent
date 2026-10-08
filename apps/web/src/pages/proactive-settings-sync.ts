export interface ProactiveAPI {
  updateEnvVar: (params: {
    key: string;
    value: string;
    isSecret: boolean;
  }) => Promise<{ success: boolean; error?: string }>;
}

export interface ProactiveState {
  enabled: boolean;
  probability: number;
}

export interface ProactiveSyncListener {
  onStateChange: (state: ProactiveState & { isSaving: boolean }) => void;
  onError: (err: Error) => void;
  onSuccess: (msg: string) => void;
  onReloadNeeded: () => Promise<void>;
}

export class ProactiveSettingsSync {
  private api: ProactiveAPI;
  private listeners: ProactiveSyncListener;
  private current: ProactiveState;
  private isSaving = false;
  private pendingTarget: ProactiveState | null = null;
  private opPromise: Promise<void> = Promise.resolve();
  private latestLoadSeq = 0;

  constructor(
    api: ProactiveAPI,
    initial: ProactiveState,
    listeners: ProactiveSyncListener,
  ) {
    this.api = api;
    this.current = {
      enabled: initial.enabled,
      probability: ProactiveSettingsSync.clampProb(initial.probability),
    };
    this.listeners = listeners;
  }

  static clampProb(val: number): number {
    let prob = Math.round(val * 100) / 100;
    if (isNaN(prob) || prob < 0.01) {
      prob = 0.01;
    }
    if (prob > 1.0) {
      prob = 1.0;
    }
    return Math.round(prob * 100) / 100;
  }

  getState(): ProactiveState & { isSaving: boolean } {
    return { ...this.current, isSaving: this.isSaving };
  }

  nextLoadSeq(): number {
    return ++this.latestLoadSeq;
  }

  applyServerConfig(
    enabled: boolean,
    probability: number,
    seq: number,
  ): boolean {
    if (seq < this.latestLoadSeq) {
      return false; // Stale load response
    }
    this.latestLoadSeq = seq;
    if (this.isSaving || this.pendingTarget !== null) {
      // Don't overwrite active user edits while save is in flight
      return false;
    }
    this.current = {
      enabled,
      probability: ProactiveSettingsSync.clampProb(probability),
    };
    this.listeners.onStateChange(this.getState());
    return true;
  }

  async setTarget(enabled: boolean, prob: number): Promise<void> {
    const target: ProactiveState = {
      enabled,
      probability: ProactiveSettingsSync.clampProb(prob),
    };

    // Optimistically update current state and notify UI
    this.current = { ...target };
    this.pendingTarget = target;
    this.listeners.onStateChange(this.getState());

    if (this.isSaving) {
      return this.opPromise;
    }

    this.opPromise = this.drainPending();
    return this.opPromise;
  }

  private async drainPending(): Promise<void> {
    this.isSaving = true;
    while (this.pendingTarget !== null) {
      const target = this.pendingTarget;
      this.pendingTarget = null;
      this.listeners.onStateChange(this.getState());

      try {
        await this.executeSave(target);
        this.current = target;
      } catch (err) {
        const error = err instanceof Error ? err : new Error(String(err));
        this.listeners.onError(error);
        try {
          await this.listeners.onReloadNeeded();
        } catch {
          // ignore reload error
        }
        break;
      }
    }
    this.isSaving = false;
    this.listeners.onStateChange(this.getState());
  }

  private async executeSave(target: ProactiveState): Promise<void> {
    if (target.enabled) {
      // Sequential writes: first probability, then enabled flag
      const probStr = target.probability.toFixed(2);
      const resProb = await this.api.updateEnvVar({
        key: 'PROACTIVE_REPLY_PROBABILITY',
        value: probStr,
        isSecret: false,
      });
      if (!resProb.success) {
        throw new Error(resProb.error || '更新主动回复概率失败');
      }

      const resEn = await this.api.updateEnvVar({
        key: 'ENABLE_PROACTIVE_REPLY',
        value: 'true',
        isSecret: false,
      });
      if (!resEn.success) {
        throw new Error(resEn.error || '开启主动回复失败');
      }

      this.listeners.onSuccess(`主动回复已开启 (触发概率: ${probStr})`);
    } else {
      const resEn = await this.api.updateEnvVar({
        key: 'ENABLE_PROACTIVE_REPLY',
        value: 'false',
        isSecret: false,
      });
      if (!resEn.success) {
        throw new Error(resEn.error || '停用主动回复失败');
      }

      this.listeners.onSuccess('主动回复已停用');
    }
  }
}
