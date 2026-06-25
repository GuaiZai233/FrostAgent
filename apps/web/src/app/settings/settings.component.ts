import { Component, inject, computed } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatIconModule } from '@angular/material/icon';
import { MatToolbarModule } from '@angular/material/toolbar';
import { Router, RouterModule } from '@angular/router';

@Component({
  selector: 'app-settings',
  imports: [
    MatButtonModule,
    MatCardModule,
    MatIconModule,
    MatToolbarModule,
    RouterModule,
  ],
  templateUrl: './settings.component.html',
})
export class SettingsComponent {
  private readonly router = inject(Router);

  readonly isChildRoute = computed(() => {
    return this.router.url !== '/settings';
  });

  navigateTo(path: string): void {
    void this.router.navigate([path]);
  }
}
