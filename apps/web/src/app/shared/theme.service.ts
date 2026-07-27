import { Injectable, inject, signal, computed, effect } from '@angular/core';
import { DOCUMENT } from '@angular/common';

export type ThemeMode = 'system' | 'light' | 'dark';

@Injectable({
  providedIn: 'root',
})
export class ThemeService {
  private readonly document = inject(DOCUMENT);
  private readonly mediaQuery: MediaQueryList;
  private readonly localStorageKey = 'user-theme';

  private readonly isSystemDark = signal(false);

  readonly currentMode = signal<ThemeMode>(this.loadPreference());

  readonly effectiveMode = computed(() => {
    const mode = this.currentMode();
    if (mode === 'system') {
      // 当这个 Signal 改变时，computed 才会真正联动
      return this.isSystemDark() ? 'dark' : 'light';
    }
    return mode;
  });

  constructor() {
    this.mediaQuery = (this.document.defaultView ?? window).matchMedia(
      '(prefers-color-scheme: dark)',
    );

    // 初始化同步当前的系统状态
    this.isSystemDark.set(this.mediaQuery.matches);

    // 当浏览器/系统主题改变时，直接更新这个 Signal
    this.mediaQuery.addEventListener('change', (e) => {
      this.isSystemDark.set(e.matches);
    });

    effect(() => {
      this.applyMode(this.currentMode());
    });
  }

  setMode(mode: ThemeMode): void {
    this.currentMode.set(mode);
    localStorage.setItem(this.localStorageKey, mode);
  }

  private applyMode(mode: ThemeMode): void {
    const html = this.document.documentElement;
    if (mode === 'light') {
      html.style.colorScheme = 'light';
    } else if (mode === 'dark') {
      html.style.colorScheme = 'dark';
    } else {
      html.style.colorScheme = 'light dark';
    }
  }

  private loadPreference(): ThemeMode {
    const stored = localStorage.getItem(this.localStorageKey);
    if (stored === 'light' || stored === 'dark' || stored === 'system') {
      return stored;
    }
    return 'system';
  }
}
