import { Injectable, inject, signal, computed, effect } from '@angular/core';
import { DOCUMENT } from '@angular/common';

export type ThemeMode = 'system' | 'light' | 'dark';
export type SeasonMode = 'auto' | 'spring' | 'summer' | 'autumn' | 'winter';

@Injectable({
  providedIn: 'root',
})
export class ThemeService {
  private readonly document = inject(DOCUMENT);
  private readonly mediaQuery: MediaQueryList;
  private readonly localStorageKey = 'user-theme';
  private readonly seasonStorageKey = 'user-season';

  readonly currentMode = signal<ThemeMode>(this.loadPreference());
  readonly currentSeasonMode = signal<SeasonMode>(this.loadSeasonPreference());

  readonly effectiveMode = computed(() => {
    const mode = this.currentMode();
    if (mode === 'system') {
      return this.mediaQuery.matches ? 'dark' : 'light';
    }
    return mode;
  });

  readonly effectiveSeason = computed(() => {
    const mode = this.currentSeasonMode();
    if (mode === 'auto') {
      const month = new Date().getMonth() + 1; // 1-12
      if (month >= 3 && month <= 5) return 'spring';
      if (month >= 6 && month <= 8) return 'summer';
      if (month >= 9 && month <= 11) return 'autumn';
      return 'winter';
    }
    return mode;
  });

  constructor() {
    this.mediaQuery = (this.document.defaultView ?? window).matchMedia(
      '(prefers-color-scheme: dark)',
    );

    this.mediaQuery.addEventListener('change', () => {
      if (this.currentMode() === 'system') {
        // Trigger re-computation of effectiveMode
        this.currentMode.set('system');
      }
    });

    effect(() => {
      this.applyMode(this.currentMode());
    });

    effect(() => {
      this.applySeason(this.effectiveSeason() as Exclude<SeasonMode, 'auto'>);
    });
  }

  setMode(mode: ThemeMode): void {
    this.currentMode.set(mode);
    localStorage.setItem(this.localStorageKey, mode);
  }

  setSeasonMode(mode: SeasonMode): void {
    this.currentSeasonMode.set(mode);
    localStorage.setItem(this.seasonStorageKey, mode);
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

  private applySeason(season: Exclude<SeasonMode, 'auto'>): void {
    const html = this.document.documentElement;
    const seasons: SeasonMode[] = ['spring', 'summer', 'autumn', 'winter'];
    seasons.forEach((s) => html.classList.remove(`theme-${s}`));
    html.classList.add(`theme-${season}`);
  }

  private loadPreference(): ThemeMode {
    const stored = localStorage.getItem(this.localStorageKey);
    if (stored === 'light' || stored === 'dark' || stored === 'system') {
      return stored;
    }
    return 'system';
  }

  private loadSeasonPreference(): SeasonMode {
    const stored = localStorage.getItem(this.seasonStorageKey);
    if (
      stored === 'auto' ||
      stored === 'spring' ||
      stored === 'summer' ||
      stored === 'autumn' ||
      stored === 'winter'
    ) {
      return stored;
    }
    return 'auto';
  }
}
