import { Component, computed, inject } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { NavigationEnd, Router, RouterModule } from '@angular/router';
import { HlmCardImports } from '@spartan-ng/helm/card';
import { filter, map } from 'rxjs';
import { AppIconComponent } from '../shared/app-icon.component';

@Component({
  selector: 'app-settings',
  imports: [
    HlmCardImports,
    RouterModule,
    AppIconComponent,
  ],
  templateUrl: './settings.component.html',
})
export class SettingsComponent {
  private readonly router = inject(Router);
  private readonly currentUrl = toSignal(
    this.router.events.pipe(
      filter((event): event is NavigationEnd => event instanceof NavigationEnd),
      map((event) => event.urlAfterRedirects),
    ),
    { initialValue: this.router.url },
  );

  readonly isChildRoute = computed(() => this.currentUrl() !== '/settings');
}
