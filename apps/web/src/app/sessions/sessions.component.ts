import { Component, OnInit, inject, signal } from '@angular/core';
import type { SessionInfo } from '@frostagent/proto';
import { toast } from '@spartan-ng/brain/sonner';
import { HlmButton } from '@spartan-ng/helm/button';
import { HlmCardImports } from '@spartan-ng/helm/card';
import { HlmDialogService } from '@spartan-ng/helm/dialog';
import { HlmPaginationImports } from '@spartan-ng/helm/pagination';
import { HlmSelectImports } from '@spartan-ng/helm/select';
import { HlmSpinner } from '@spartan-ng/helm/spinner';
import { HlmTableImports } from '@spartan-ng/helm/table';
import { FrostagentApiService } from '../core/frostagent-api.service';
import { AppIconComponent } from '../shared/app-icon.component';
import {
  ConfirmDialogService,
  type ConfirmDialogData,
} from '../shared/confirm-dialog.component';
import {
  PageTokenStack,
  formatDateTime,
  formatPlatform,
} from '../shared/dashboard-utils';
import { SessionSummaryDialogComponent } from './session-summary-dialog.component';

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
  private readonly confirmDialog = inject(ConfirmDialogService);
  private readonly dialog = inject(HlmDialogService);
  private readonly pageTokens = new PageTokenStack();

  readonly sessions = signal<SessionInfo[]>([]);
  readonly loading = signal(false);
  readonly error = signal('');
  readonly pageSize = signal(20);
  readonly nextToken = signal('');
  readonly total = signal(0);
  readonly pageIndex = signal(0);
  readonly deleting = signal('');

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

  openSummary(session: SessionInfo): void {
    this.dialog.open(SessionSummaryDialogComponent, {
      contentClass: 'sm:max-w-2xl',
      context: { session },
    });
  }

  async deleteSummary(session: SessionInfo): Promise<void> {
    const data: ConfirmDialogData = {
      title: $localize`:@@deleteGroupSummaryTitle:删除群聊总结`,
      message: $localize`:@@deleteGroupSummaryMessage:确认删除 ${session.sessionId}:INTERPOLATION: 的群聊总结吗？内存总结和待压缩上下文也会清空。`,
      cancelLabel: $localize`:@@cancel:取消`,
      confirmLabel: $localize`:@@delete:删除`,
    };
    if (!(await this.confirmDialog.confirm(data))) {
      return;
    }

    this.deleting.set(session.sessionId);
    try {
      const response = await this.api.deleteGroupSummary(session.sessionId);
      if (!response.success) {
        const message = response.error || $localize`:@@deleteFailed:删除失败`;
        this.error.set(message);
        toast.error(message, { duration: 3000 });
        return;
      }
      toast.success($localize`:@@groupSummaryDeleted:群聊总结已删除`, {
        duration: 2500,
      });
      await this.refresh();
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.error.set(message);
      toast.error(message, { duration: 3000 });
    } finally {
      this.deleting.set('');
    }
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
