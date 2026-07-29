import { Component, OnDestroy, inject, signal } from '@angular/core';
import { LogLevel, type LogEntry } from '@frostagent/proto';
import { toast } from '@spartan-ng/brain/sonner';
import { HlmBadge } from '@spartan-ng/helm/badge';
import { HlmButton } from '@spartan-ng/helm/button';
import { HlmCardImports } from '@spartan-ng/helm/card';
import { HlmField, HlmFieldLabel } from '@spartan-ng/helm/field';
import { HlmInput } from '@spartan-ng/helm/input';
import { HlmPaginationImports } from '@spartan-ng/helm/pagination';
import { HlmSelectImports } from '@spartan-ng/helm/select';
import { HlmSpinner } from '@spartan-ng/helm/spinner';
import { HlmTableImports } from '@spartan-ng/helm/table';

import { FrostagentApiService } from '../core/frostagent-api.service';
import {
  ConfirmDialogService,
  type ConfirmDialogData,
} from '../shared/confirm-dialog.component';
import { AppIconComponent } from '../shared/app-icon.component';
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
    HlmBadge,
    HlmButton,
    HlmCardImports,
    HlmField,
    HlmFieldLabel,
    HlmInput,
    HlmPaginationImports,
    HlmSelectImports,
    HlmSpinner,
    HlmTableImports,
    AppIconComponent,
  ],
  templateUrl: './logs.component.html',
})
export class LogsComponent implements OnDestroy {
  private readonly api = inject(FrostagentApiService);
  private readonly confirmDialog = inject(ConfirmDialogService);
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

  async changeLevel(value: LogLevel | null | undefined): Promise<void> {
    if (value === null || value === undefined) return;
    this.minLevel.set(value);
    await this.refresh();
  }

  async changePage(direction: 'previous' | 'next'): Promise<void> {
    if (direction === 'next') {
      if (!this.nextToken()) return;
      this.pageTokens.push(this.nextToken());
      this.pageIndex.update((value) => value + 1);
    } else {
      if (!this.canGoBack()) return;
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
    const confirmed = await this.confirmDialog.confirm(data);

    if (!confirmed) {
      return;
    }

    const success = await this.api.clearLogs();
    if (success) {
      this.entries.set([]);
      this.streamEntries.set([]);
      this.selectedEntry.set(null);
      toast.success($localize`:@@logsCleared:日志已清理`, {
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

  logLevelClass(value: LogLevel): string {
    switch (this.logLevelTone(value)) {
      case 'error':
        return 'border-destructive/30 bg-destructive/10 text-destructive';
      case 'warn':
      case 'warning':
        return 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300';
      case 'debug':
        return 'border-violet-500/30 bg-violet-500/10 text-violet-700 dark:text-violet-300';
      default:
        return 'border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300';
    }
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
