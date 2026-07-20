import { Component, inject } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatCardModule } from '@angular/material/card';
import { MatDividerModule } from '@angular/material/divider';
import { MatIconModule } from '@angular/material/icon';
import { MatListModule } from '@angular/material/list';
import { MatSelectModule } from '@angular/material/select';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatToolbarModule } from '@angular/material/toolbar';
import { RouterModule } from '@angular/router';
import { ThemeService, ThemeMode } from '../shared/theme.service';

@Component({
  selector: 'app-frontend-settings',
  imports: [
    FormsModule,
    MatButtonModule,
    MatButtonToggleModule,
    MatCardModule,
    MatDividerModule,
    MatFormFieldModule,
    MatIconModule,
    MatListModule,
    MatSelectModule,
    MatToolbarModule,
    RouterModule,
  ],
  templateUrl: './frontend-settings.component.html',
  styleUrl: './frontend-settings.component.scss',
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
}
