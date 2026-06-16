import { Component, OnInit, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatDividerModule } from '@angular/material/divider';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatToolbarModule } from '@angular/material/toolbar';
import { RouterModule } from '@angular/router';
import { DashboardService, type DashboardState } from './dashboard.service';

@Component({
  imports: [
    MatButtonModule,
    MatCardModule,
    MatChipsModule,
    MatDividerModule,
    MatIconModule,
    MatProgressBarModule,
    MatToolbarModule,
    RouterModule,
  ],
  selector: 'app-root',
  templateUrl: './app.html',
  styleUrl: './app.scss',
})
export class App implements OnInit {
  private readonly dashboard = inject(DashboardService);

  protected readonly state = signal<DashboardState>({ status: 'loading' });

  async ngOnInit(): Promise<void> {
    await this.refresh();
  }

  protected async refresh(): Promise<void> {
    this.state.set({ status: 'loading' });

    try {
      const overview = await this.dashboard.getOverview();
      this.state.set({ status: 'ready', overview });
    } catch (error) {
      const message =
        error instanceof Error ? error.message : 'Unable to reach FrostAgent';
      this.state.set({ status: 'error', message });
    }
  }

  protected formatCount(value: bigint | number): string {
    return value.toLocaleString();
  }
}
