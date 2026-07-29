import { Component, OnInit, inject, signal } from '@angular/core';
import type { SessionInfo } from '@frostagent/proto';
import { HlmButton } from '@spartan-ng/helm/button';
import { HlmCardImports } from '@spartan-ng/helm/card';
import { HlmPaginationImports } from '@spartan-ng/helm/pagination';
import { HlmSelectImports } from '@spartan-ng/helm/select';
import { HlmSpinner } from '@spartan-ng/helm/spinner';
import { HlmTableImports } from '@spartan-ng/helm/table';
import { FrostagentApiService } from '../core/frostagent-api.service';
import { AppIconComponent } from '../shared/app-icon.component';
import {
  PageTokenStack,
  formatDateTime,
  formatPlatform,
} from '../shared/dashboard-utils';

@Component({
  selector: 'app-sessions',
  imports: [
    HlmButton,
    HlmCardImports,
    HlmPaginationImports,
    HlmSelectImports,
    HlmSpinner,
    HlmTableImports,
    AppIconComponent,
  ],
  templateUrl: './sessions.component.html',
})
export class SessionsComponent implements OnInit {
  private readonly api = inject(FrostagentApiService);
  private readonly pageTokens = new PageTokenStack();

  readonly sessions = signal<SessionInfo[]>([]);
  readonly loading = signal(false);
  readonly error = signal('');
  readonly pageSize = signal(20);
  readonly nextToken = signal('');
  readonly total = signal(0);
  readonly pageIndex = signal(0);

  ngOnInit(): void {
    void this.loadCurrentPage();
  }

  async refresh(): Promise<void> {
    this.pageTokens.reset();
    this.pageIndex.set(0);
    await this.loadCurrentPage();
  }

  async changePage(direction: 'previous' | 'next'): Promise<void> {
    if (direction === 'next') {
      if (!this.nextToken()) return;
      this.pageTokens.push(this.nextToken());
      this.pageIndex.update((value) => value + 1);
    } else {
      if (this.pageIndex() === 0) return;
      this.pageTokens.back();
      this.pageIndex.update((value) => value - 1);
    }
    await this.loadCurrentPage();
  }

  async changePageSize(value: number | null | undefined): Promise<void> {
    if (!value || value === this.pageSize()) return;
    this.pageSize.set(value);
    await this.refresh();
  }

  formatDateTime(value: string): string {
    return formatDateTime(value);
  }

  formatPlatform(value: string): string {
    return formatPlatform(value);
  }

  private async loadCurrentPage(): Promise<void> {
    this.loading.set(true);
    this.error.set('');

    try {
      const response = await this.api.getSessions(
        this.pageSize(),
        this.pageTokens.current(),
      );
      this.sessions.set(response.sessions);
      this.nextToken.set(response.pagination?.pageToken ?? '');
      this.total.set(response.pagination?.total ?? response.sessions.length);
    } catch (error) {
      this.error.set(error instanceof Error ? error.message : String(error));
    } finally {
      this.loading.set(false);
    }
  }
}
