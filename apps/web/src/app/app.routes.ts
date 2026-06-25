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
    loadChildren: () =>
      import('./settings/settings.routes').then((m) => m.settingsRoutes),
  },
  {
    path: '**',
    redirectTo: 'overview',
  },
];
