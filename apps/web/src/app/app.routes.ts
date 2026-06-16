import { Route } from '@angular/router';

export const appRoutes: Route[] = [
  {
    path: '',
    pathMatch: 'full',
    redirectTo: 'overview',
  },
  {
    path: 'overview',
    loadComponent: () =>
      import('./overview/overview.component').then((m) => m.OverviewComponent),
  },
  {
    path: 'sessions',
    loadComponent: () =>
      import('./sessions/sessions.component').then((m) => m.SessionsComponent),
  },
  {
    path: 'logs',
    loadComponent: () =>
      import('./logs/logs.component').then((m) => m.LogsComponent),
  },
  {
    path: 'settings',
    loadComponent: () =>
      import('./settings/settings.component').then((m) => m.SettingsComponent),
  },
  {
    path: '**',
    redirectTo: 'overview',
  },
];
