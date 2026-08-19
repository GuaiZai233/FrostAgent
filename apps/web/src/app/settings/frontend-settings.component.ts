import { Component, inject } from '@angular/core';
import { RouterModule } from '@angular/router';
import { HlmButton } from '@spartan-ng/helm/button';
import { HlmCardImports } from '@spartan-ng/helm/card';
import { HlmToggleGroupImports } from '@spartan-ng/helm/toggle-group';
import { AppIconComponent } from '../shared/app-icon.component';
import { ThemeService, ThemeMode } from '../shared/theme.service';

@Component({
  selector: 'app-frontend-settings',
  imports: [
    HlmButton,
    HlmCardImports,
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
      label: '跟随系统',
    },
    { mode: 'light', icon: 'light_mode', label: '亮色' },
    { mode: 'dark', icon: 'dark_mode', label: '暗色' },
  ];

  setTheme(mode: string | string[] | null | undefined): void {
    if (typeof mode === 'string') {
      this.themeService.setMode(mode as ThemeMode);
    }
  }
}
