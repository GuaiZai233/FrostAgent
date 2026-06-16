import {
  ApplicationConfig,
  provideBrowserGlobalErrorListeners,inject,provideAppInitializer
} from '@angular/core';
import { provideRouter } from '@angular/router';
import { appRoutes } from './app.routes';
import { MatIconRegistry } from '@angular/material/icon'; 

export const appConfig: ApplicationConfig = {
  providers: [
    provideBrowserGlobalErrorListeners(),
    provideRouter(appRoutes),
    provideAppInitializer(() => {
      const iconRegistry = inject(MatIconRegistry);
      iconRegistry.setDefaultFontSetClass('material-symbols-rounded');
    }),

  ],
  
};
