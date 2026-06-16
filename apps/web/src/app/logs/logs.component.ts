import { Component, OnDestroy, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTableModule } from '@angular/material/table';
import { LogLevel, type LogEntry } from '@frostagent/proto';import {MatTooltipModule} from '@angular/material/tooltip';

import { FrostagentApiService } from '../core/frostagent-api.service';
import {
  ConfirmDialogComponent,
  type ConfirmDialogData,
} from '../shared/confirm-dialog.component';
import {
  PageTokenStack,
  formatDateTime,
  formatLogLevel,
  logLevelOptions,
  logLevelTone,
} from '../shared/dashboard-utils';

@Component({
  selector: 'app-logs',
  imports: [
    MatButtonModule,
    MatCardModule,
    MatTooltipModule,
    MatChipsModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatPaginatorModule,
    MatProgressBarModule,
    MatSelectModule,
    MatTableModule,
  ],
  templateUrl: './logs.component.html',
})
export class LogsComponent implements OnDestroy {
  private readonly api = inject(FrostagentApiService);
  private readonly dialog = inject(MatDialog);
  private readonly snackBar = inject(MatSnackBar);
  private readonly pageTokens = new PageTokenStack();
  private streamAbortController: AbortController | null = null;

  readonly entries = signal<LogEntry[]>([]);
  readonly streamEntries = signal<LogEntry[]>([]);
  readonly selectedEntry = signal<LogEntry | null>(null);
  readonly loading = signal(false);
  readonly streaming = signal(false);
  readonly error = signal('');
  readonly minLevel = signal<LogLevel>(LogLevel.UNSPECIFIED);
  readonly sourceFilter = signal('');
  readonly pageSize = signal(50);
  readonly nextToken = signal('');
  readonly total = signal(0);
  readonly pageIndex = signal(0);
  readonly logLevelOptions = logLevelOptions;
  readonly displayedColumns = ['timestamp', 'level', 'source', 'summary'];

  constructor() {
    void this.refresh();
  }

  ngOnDestroy(): void {
    this.stopStream();
  }

  async refresh(): Promise<void> {
    this.pageTokens.reset();
    this.pageIndex.set(0);
    await this.loadCurrentPage();
  }

  async changeLevel(value: LogLevel): Promise<void> {
    this.minLevel.set(value);
    await this.refresh();
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

  async applySourceFilter(value: string): Promise<void> {
    this.sourceFilter.set(value.trim());
    await this.refresh();
  }

  canGoBack(): boolean {
    return this.pageTokens.canGoBack();
  }

  selectEntry(entry: LogEntry): void {
    this.selectedEntry.set(entry);
  }

  async toggleStream(): Promise<void> {
    if (this.streaming()) {
      this.stopStream();
      return;
    }

    this.streamAbortController = new AbortController();
    this.streaming.set(true);
    this.error.set('');

    try {
      for await (const entry of this.api.streamLogs(
        this.minLevel(),
        this.sourceFilter(),
        this.streamAbortController.signal,
      )) {
        this.streamEntries.update((entries) => [entry, ...entries].slice(0, 200));
      }
    } catch (error) {
      if (!this.streamAbortController?.signal.aborted) {
        this.error.set(error instanceof Error ? error.message : String(error));
      }
    } finally {
      this.streaming.set(false);
      this.streamAbortController = null;
    }
  }

  stopStream(): void {
    this.streamAbortController?.abort();
    this.streamAbortController = null;
    this.streaming.set(false);
  }

  async clearLogs(): Promise<void> {
    const data: ConfirmDialogData = {
      title: $localize`:@@clearLogsTitle:清理日志`,
      message: $localize`:@@clearLogsMessage:确认清理当前内存日志缓冲区吗？此操作无法撤销。`,
      cancelLabel: $localize`:@@cancel:取消`,
      confirmLabel: $localize`:@@clear:清理`,
    };
    const confirmed = await this.dialog
      .open(ConfirmDialogComponent, { data })
      .afterClosed()
      .toPromise();

    if (!confirmed) {
      return;
    }

    const success = await this.api.clearLogs();
    if (success) {
      this.entries.set([]);
      this.streamEntries.set([]);
      this.selectedEntry.set(null);
      this.snackBar.open($localize`:@@logsCleared:日志已清理`, undefined, {
        duration: 2500,
      });
      await this.refresh();
    }
  }

  formatDateTime(value: string): string {
    return formatDateTime(value);
  }

  formatLogLevel(value: LogLevel): string {
    return formatLogLevel(value);
  }

  logLevelTone(value: LogLevel): string {
    return logLevelTone(value);
  }

  streamActionLabel(): string {
    return this.streaming()
      ? $localize`:@@stopStream:停止`
      : $localize`:@@startStream:开始`;
  }

  private async loadCurrentPage(): Promise<void> {
    this.loading.set(true);
    this.error.set('');

    try {
      const response = await this.api.listLogs(
        this.pageSize(),
        this.pageTokens.current(),
        this.minLevel(),
        this.sourceFilter(),
      );
      this.entries.set(response.entries);
      this.nextToken.set(response.pagination?.pageToken ?? '');
      this.total.set(response.pagination?.total ?? response.entries.length);
      if (!response.entries.some((entry) => entry.id === this.selectedEntry()?.id)) {
        this.selectedEntry.set(response.entries[0] ?? null);
      }
    } catch (error) {
      this.error.set(error instanceof Error ? error.message : String(error));
    } finally {
      this.loading.set(false);
    }
  }
}
