import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { toSignal } from '@angular/core/rxjs-interop';
import { timer, from, of, combineLatest } from 'rxjs';
import { switchMap, catchError, shareReplay, map, startWith, takeUntil, share } from 'rxjs/operators';
import { BotStatus } from '@frostagent/proto';
import { FrostagentApiService } from '../core/frostagent-api.service';
import { formatCount, formatStatus, formatUptime } from '../shared/dashboard-utils';

@Component({
  selector: 'app-overview',
  imports: [
    CommonModule,
    MatButtonModule,
    MatCardModule,
    MatChipsModule,
    MatIconModule,
    MatProgressBarModule,
  ],
  templateUrl: './overview.component.html',
  styleUrl: './overview.component.scss',
})
export class OverviewComponent {
  private readonly api = inject(FrostagentApiService);

  private readonly state$ = timer(0, 3000).pipe(
    switchMap(() => {
      const apiCall$ = from(this.api.getOverview()).pipe(
        map(data => ({ data, error: '' })),
        catchError(err => of({ data: null, error: err instanceof Error ? err.message : String(err) })),
        share()
      );

      const loading$ = timer(2000).pipe(
        map(() => true),
        takeUntil(apiCall$),
        startWith(false)
      );

      return combineLatest([apiCall$, loading$]).pipe(
        map(([res, loading]) => ({ ...res, loading }))
      );
    }),
    shareReplay(1)
  );

  readonly overview = toSignal(this.state$.pipe(map(s => s.data)), { initialValue: null });
  readonly loading = toSignal(this.state$.pipe(map(s => s.loading)), { initialValue: false });
  readonly error = toSignal(this.state$.pipe(map(s => s.error)), { initialValue: '' });
  
  readonly BotStatus = BotStatus;

  formatCount(value: bigint | number): string {
    return formatCount(value);
  }

  formatUptime(value: bigint | number): string {
    return formatUptime(value);
  }

  formatStatus(value: BotStatus): string {
    return formatStatus(value);
  }
}
