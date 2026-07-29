import { Component, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { toSignal } from '@angular/core/rxjs-interop';
import { HlmBadge } from '@spartan-ng/helm/badge';
import { HlmCardImports } from '@spartan-ng/helm/card';
import { HlmSpinner } from '@spartan-ng/helm/spinner';
import { timer, from, of, combineLatest } from 'rxjs';
import { switchMap, catchError, shareReplay, map, startWith, takeUntil, share } from 'rxjs/operators';
import { BotStatus } from '@frostagent/proto';
import { FrostagentApiService } from '../core/frostagent-api.service';
import { AppIconComponent } from '../shared/app-icon.component';
import { formatCount, formatStatus, formatUptime } from '../shared/dashboard-utils';

@Component({
  selector: 'app-overview',
  imports: [
    CommonModule,
    HlmBadge,
    HlmCardImports,
    HlmSpinner,
    AppIconComponent,
  ],
  templateUrl: './overview.component.html',
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

  greet(name: string): string {
    return $localize`:@@overviewGreeting:你好👋！我是 ${name}`;
  }

  backendVersion(version: string): string {
    return $localize`:@@backendVersion:后端版本 ${version}`;
  }

  statusClass(status: BotStatus): string {
    switch (status) {
      case BotStatus.RUNNING:
        return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300';
      case BotStatus.INITIALIZING:
        return 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300';
      case BotStatus.ERROR:
        return 'border-destructive/30 bg-destructive/10 text-destructive';
      default:
        return '';
    }
  }
}
