import { Component } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatIconModule } from '@angular/material/icon';
import { MatListModule } from '@angular/material/list';
import { MatToolbarModule } from '@angular/material/toolbar';
import { RouterModule } from '@angular/router';

@Component({
  selector: 'app-frontend-settings',
  imports: [
    MatButtonModule,
    MatCardModule,
    MatIconModule,
    MatListModule,
    MatToolbarModule,
    RouterModule,
  ],
  templateUrl: './frontend-settings.component.html',
})
export class FrontendSettingsComponent {
  currentLocaleLabel(): string {
    return $localize.locale === 'en'
      ? $localize`:@@currentLanguageEn:当前: English`
      : $localize`:@@currentLanguageZh:当前: 简体中文`;
  }

  switchLocaleLabel(): string {
    return $localize.locale === 'en'
      ? $localize`:@@switchToChinese:简体中文`
      : $localize`:@@switchToEnglish:English`;
  }

  /** Link to the root of the other locale's build; triggers a full page reload */
  switchLocaleHref(): string {
    return $localize.locale === 'en' ? '/' : '/en/';
  }
}
