import { Component, OnDestroy, inject, signal, computed } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatDividerModule } from '@angular/material/divider';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatToolbarModule } from '@angular/material/toolbar';
import { MatMenuModule } from '@angular/material/menu';
import { MatDialog, MatDialogModule } from '@angular/material/dialog';
import { NavigationEnd, Router, RouterModule } from '@angular/router';
import { MAT_NAVIGATION_SUITE_MODULES, MatNavigationSuiteScaffoldState, MatNavigationSuiteScaffoldDefaults } from '@fairylights-studio/ngx-m3-navigation-suite';
import { Subscription, filter } from 'rxjs';
import { localeSwitchPath } from './shared/dashboard-utils';
import { MatExtendedFabCollapsedDirective } from '@fairylights-studio/ngx-m3-button';
import { AddEnvVarDialogComponent } from './shared/add-env-var-dialog.component';

@Component({
  imports: [
    MAT_NAVIGATION_SUITE_MODULES,
    MatExtendedFabCollapsedDirective,
    MatButtonModule,
    MatCardModule,
    MatChipsModule,
    MatDividerModule,
    MatIconModule,
    MatMenuModule,
    MatDialogModule,
    MatProgressBarModule,
    MatToolbarModule,
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
  private readonly routerEvents: Subscription;
  readonly currentUrl = signal(this.router.url);

  readonly scaffoldState = new MatNavigationSuiteScaffoldState();

  readonly isSettingsPage = computed(() =>
    this.currentUrl().startsWith('/settings'),
  );

  readonly isRailMode = computed(() => {
    const type = this.scaffoldDefaults.navSuiteType()();
    return type === 'RailCollapsed' || type === 'RailExpanded';
  });

  readonly shouldShowFab = computed(() => this.isRailMode() || this.isSettingsPage());

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

  switchLocalePath(): string {
    return localeSwitchPath(window.location.pathname);
  }

  switchLocaleLabel(): string {
    return $localize.locale === 'en'
      ? $localize`:@@switchToChinese:简体中文`
      : $localize`:@@switchToEnglish:English`;
  }
}
