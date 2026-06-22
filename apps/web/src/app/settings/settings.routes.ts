import { Route } from '@angular/router';

export const settingsRoutes: Route[] = [
  {
    path: '',
    loadComponent: () =>
      import('./settings.component').then((m) => m.SettingsComponent),
  },
  {
    path: 'backend',
    loadComponent: () =>
      import('./backend-settings.component').then((m) => m.BackendSettingsComponent),
  },
  {
    path: 'frontend',
    loadComponent: () =>
      import('./frontend-settings.component').then((m) => m.FrontendSettingsComponent),
  },
];
