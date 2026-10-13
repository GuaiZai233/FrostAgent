export interface SettingValue {
  key: string;
  value: string;
  isSecret: boolean;
  resetHint?: string;
}

export interface SettingChange {
  key: string;
  value: string | null;
  isSecret: boolean;
}

export class SettingsDraft {
  private saved = new Map<string, SettingValue>();
  private changes = new Map<string, SettingChange>();
  private savedExtra = new Map<string, string>();
  private extra = new Map<string, string>();

  constructor(values: SettingValue[], extra: { key: string; value: string }[]) {
    values.forEach((value) => this.saved.set(value.key, { ...value }));
    extra.forEach(({ key, value }) => this.savedExtra.set(key, value));
    this.extra = new Map(this.savedExtra);
  }

  get(key: string): string {
    const change = this.changes.get(key);
    return change ? change.value ?? '' : this.saved.get(key)?.value ?? '';
  }

  set(key: string, value: string): void {
    if (value === (this.saved.get(key)?.value ?? '')) this.changes.delete(key);
    else this.changes.set(key, { key, value, isSecret: this.saved.get(key)?.isSecret ?? false });
  }

  clear(key: string): void {
    if (this.isCleared(key)) this.changes.delete(key);
    else this.changes.set(key, { key, value: null, isSecret: this.saved.get(key)?.isSecret ?? false });
  }

  isCleared(key: string): boolean { return this.changes.get(key)?.value === null; }
  values(): SettingValue[] { return [...this.saved.values()].map((value) => ({ ...value, value: this.get(value.key) })); }
  pending(): SettingChange[] { return [...this.changes.values()].map((value) => ({ ...value })); }
  extraEntries(): { key: string; value: string }[] { return [...this.extra].sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => ({ key, value })); }
  setExtra(key: string, value: string): void { this.extra.set(key, value); }
  deleteExtra(key: string): void { this.extra.delete(key); }

  extraChanges(): { key: string; value: string | null }[] {
    return [...new Set([...this.savedExtra.keys(), ...this.extra.keys()])]
      .filter((key) => this.savedExtra.get(key) !== this.extra.get(key) || this.savedExtra.has(key) !== this.extra.has(key))
      .map((key) => ({ key, value: this.extra.get(key) ?? null }));
  }

  acknowledgeExtra(key: string, value: string | null): void {
    if (value === null) this.savedExtra.delete(key);
    else this.savedExtra.set(key, value);
  }

  acknowledgeSettings(changes: SettingChange[]): void {
    changes.forEach(({ key, value, isSecret }) => {
      this.saved.set(key, { ...this.saved.get(key), key, value: value ?? '', isSecret });
      this.changes.delete(key);
    });
  }

  get dirty(): boolean { return this.changes.size > 0 || this.extraChanges().length > 0; }

  restore(): void {
    this.changes.clear();
    this.extra = new Map(this.savedExtra);
  }
}
