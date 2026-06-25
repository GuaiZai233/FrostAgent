import { Component, OnDestroy, inject, signal, computed } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatDialog, MatDialogModule } from '@angular/material/dialog';
import { NavigationEnd, Router, RouterModule } from '@angular/router';
import { MAT_NAVIGATION_SUITE_MODULES, MatNavigationSuiteScaffoldState, MatNavigationSuiteScaffoldDefaults } from '@fairylights-studio/ngx-m3-navigation-suite';
import { Subscription, filter } from 'rxjs';
import { MatExtendedFabCollapsedDirective } from '@fairylights-studio/ngx-m3-button';
import { AddEnvVarDialogComponent } from './shared/add-env-var-dialog.component';
import { ThemeService } from './shared/theme.service';

@Component({
  imports: [
    MAT_NAVIGATION_SUITE_MODULES,
    MatExtendedFabCollapsedDirective,
    MatButtonModule,
    MatIconModule,
    MatMenuModule,
    MatDialogModule,
    RouterModule,
  ],
  selector: 'app-root',
  templateUrl: './app.html',
  styleUrl: './app.scss',
})
export class App implements OnDestroy {
  private readonly router = inject(Router);
  private readonly dialog = inject(MatDialog);
  private readonly scaffoldDefaults = inject(MatNavigationSuiteScaffoldDefaults);
  private readonly themeService = inject(ThemeService);
  private readonly routerEvents: Subscription;
  readonly currentUrl = signal(this.router.url);

  readonly scaffoldState = new MatNavigationSuiteScaffoldState();

  readonly isSettingsBackendPage = computed(() =>
    this.currentUrl().startsWith('/settings/backend'),
  );

  readonly shouldShowFab = computed(() => this.isSettingsBackendPage());

  readonly destinations = [
    {
      path: '/overview',
      icon: 'dashboard',
      label: $localize`:@@navOverview:Bot状态`,
    },
    {
      path: '/sessions',
      icon: 'forum',
      label: $localize`:@@navSessions:会话`,
    },
    {
      path: '/logs',
      icon: 'terminal',
      label: $localize`:@@navLogs:日志`,
    },
    {
      path: '/settings',
      icon: 'settings',
      label: $localize`:@@navSettings:设置`,
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

  navigate(path: string): void {
    void this.router.navigateByUrl(path);
  }

  openAddEnvVarDialog(): void {
    this.dialog.open(AddEnvVarDialogComponent, {
      width: '400px',
    });
  }
}
