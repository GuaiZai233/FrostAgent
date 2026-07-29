import { Component, inject } from '@angular/core';
import { RouterModule } from '@angular/router';
import { HlmButton } from '@spartan-ng/helm/button';
import { HlmCardImports } from '@spartan-ng/helm/card';
import { HlmField, HlmFieldLabel } from '@spartan-ng/helm/field';
import { HlmSelectImports } from '@spartan-ng/helm/select';
import { HlmToggleGroupImports } from '@spartan-ng/helm/toggle-group';
import { AppIconComponent } from '../shared/app-icon.component';
import { ThemeService, ThemeMode } from '../shared/theme.service';

@Component({
  selector: 'app-frontend-settings',
  imports: [
    HlmButton,
    HlmCardImports,
    HlmField,
    HlmFieldLabel,
    HlmSelectImports,
    HlmToggleGroupImports,
    RouterModule,
    AppIconComponent,
  ],
  templateUrl: './frontend-settings.component.html',
})
export class FrontendSettingsComponent {
  readonly themeService = inject(ThemeService);

  readonly themeOptions: { mode: ThemeMode; icon: string; label: string }[] = [
    {
      mode: 'system',
      icon: 'brightness_auto',
      label: $localize`:@@themeSystem:跟随系统`,
    },
    { mode: 'light', icon: 'light_mode', label: $localize`:@@themeLight:亮色` },
    { mode: 'dark', icon: 'dark_mode', label: $localize`:@@themeDark:暗色` },
  ];

  readonly currentLocale = $localize.locale;

  readonly localeOptions: { value: string; label: string }[] = [
    { value: 'zh-Hans', label: $localize`:@@localeZhCN:简体中文` },
    { value: 'en-US', label: $localize`:@@localeEn:English` },
  ];

  switchLocale(locale: string): void {
    if (locale === this.currentLocale) return;
    // Keep the current path, but change the locale prefix.
    const path = window.location.pathname;
    const stripped = path.replace(
      new RegExp(`^/(${this.currentLocale})(/|$)`),
      '/$2',
    );
    const target = '/' + locale + stripped;
    window.location.href = target;
  }

  setLocale(locale: string | null | undefined): void {
    if (locale) this.switchLocale(locale);
  }

  setTheme(mode: string | string[] | null | undefined): void {
    if (typeof mode === 'string') {
      this.themeService.setMode(mode as ThemeMode);
    }
  }
}
