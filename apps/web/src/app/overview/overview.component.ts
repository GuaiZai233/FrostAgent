import { Component, OnInit, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import type { GetOverviewResponse } from '@frostagent/proto';
import { FrostagentApiService } from '../core/frostagent-api.service';
import { formatCount, formatStatus, formatUptime } from '../shared/dashboard-utils';

@Component({
  selector: 'app-overview',
  imports: [
    MatButtonModule,
    MatCardModule,
    MatChipsModule,
    MatIconModule,
    MatProgressBarModule,
  ],
  templateUrl: './overview.component.html',
})
export class OverviewComponent implements OnInit {
  private readonly api = inject(FrostagentApiService);

  readonly overview = signal<GetOverviewResponse | null>(null);
  readonly loading = signal(false);
  readonly error = signal('');

  ngOnInit(): void {
    void this.refresh();
  }

  async refresh(): Promise<void> {
    this.loading.set(true);
    this.error.set('');

    try {
      this.overview.set(await this.api.getOverview());
    } catch (error) {
      this.error.set(error instanceof Error ? error.message : String(error));
    } finally {
      this.loading.set(false);
    }
  }

  formatCount(value: bigint | number): string {
    return formatCount(value);
  }

  formatUptime(value: bigint | number): string {
    return formatUptime(value);
  }

  formatStatus(value: string): string {
    return formatStatus(value);
  }
}
