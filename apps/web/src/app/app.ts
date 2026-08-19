import { Component, OnDestroy, computed, inject, signal } from '@angular/core';
import { NavigationEnd, Router, RouterModule } from '@angular/router';
import { HlmButton } from '@spartan-ng/helm/button';
import { HlmDialogService } from '@spartan-ng/helm/dialog';
import { HlmSidebarImports } from '@spartan-ng/helm/sidebar';
import { HlmToaster } from '@spartan-ng/helm/sonner';
import { Subscription, filter } from 'rxjs';
import { AddEnvVarDialogComponent } from './shared/add-env-var-dialog.component';
import { AppIconComponent } from './shared/app-icon.component';
import { ThemeService } from './shared/theme.service';

@Component({
  imports: [
    HlmButton,
    HlmSidebarImports,
    HlmToaster,
    RouterModule,
    AppIconComponent,
  ],
  selector: 'app-root',
  templateUrl: './app.html',
  styleUrl: './app.scss',
})
export class App implements OnDestroy {
  private readonly router = inject(Router);
  private readonly dialog = inject(HlmDialogService);
  readonly themeService = inject(ThemeService);
  private readonly routerEvents: Subscription;
  readonly currentUrl = signal(this.router.url);

  readonly isSettingsBackendPage = computed(() =>
    this.currentUrl().startsWith('/settings/backend'),
  );

  readonly shouldShowFab = computed(() => this.isSettingsBackendPage());

  readonly destinations = [
    {
      path: '/overview',
      icon: 'dashboard',
      label: 'Bot状态',
    },
    {
      path: '/sessions',
      icon: 'forum',
      label: '会话',
    },
    {
      path: '/memory',
      icon: 'memory',
      label: '记忆',
    },
    {
      path: '/logs',
      icon: 'terminal',
      label: '日志',
    },
    {
      path: '/settings',
      icon: 'settings',
      label: '设置',
    },
  ];

  constructor() {
    this.routerEvents = this.router.events
      .pipe(filter((event): event is NavigationEnd => event instanceof NavigationEnd))
      .subscribe((event) => this.currentUrl.set(event.urlAfterRedirects));
  }

  ngOnDestroy(): void {
    this.routerEvents.unsubscribe();
  }

  isSelected(path: string): boolean {
    return this.currentUrl().startsWith(path);
  }

  openAddEnvVarDialog(): void {
    this.dialog.open(AddEnvVarDialogComponent, {
      contentClass: 'sm:max-w-md',
    });
  }
}
