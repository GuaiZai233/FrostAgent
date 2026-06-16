import { Component, OnInit, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatTableModule } from '@angular/material/table';
import type { SessionInfo } from '@frostagent/proto';
import {MatTooltipModule} from '@angular/material/tooltip';
import { FrostagentApiService } from '../core/frostagent-api.service';
import {
  PageTokenStack,
  formatDateTime,
  formatPlatform,
} from '../shared/dashboard-utils';

@Component({
  selector: 'app-sessions',
  imports: [
    MatButtonModule,
    MatTooltipModule,
    MatCardModule,
    MatFormFieldModule,
    MatIconModule,
    MatPaginatorModule,
    MatProgressBarModule,
    MatSelectModule,
    MatTableModule,
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

  readonly displayedColumns = [
    'sessionId',
    'platform',
    'messageCount',
    'createdAt',
    'lastActive',
  ];

  ngOnInit(): void {
    void this.loadCurrentPage();
  }

  async refresh(): Promise<void> {
    this.pageTokens.reset();
    this.pageIndex.set(0);
    await this.loadCurrentPage();
  }

  async handlePageEvent(event: PageEvent): Promise<void> {
    if (event.pageSize !== this.pageSize()) {
      this.pageSize.set(event.pageSize);
      await this.refresh();
      return;
    }

    if (event.pageIndex > this.pageIndex()) {
      this.pageTokens.push(this.nextToken());
    } else if (event.pageIndex < this.pageIndex()) {
      this.pageTokens.back();
    }
    this.pageIndex.set(event.pageIndex);
    await this.loadCurrentPage();
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
