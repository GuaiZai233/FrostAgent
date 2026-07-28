import { Component, OnInit, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSnackBar, MatSnackBarModule } from '@angular/material/snack-bar';
import { MatTableModule } from '@angular/material/table';
import { MatToolbarModule } from '@angular/material/toolbar';
import { MatTooltipModule } from '@angular/material/tooltip';
import { MatSelectModule } from '@angular/material/select';
import type { MemoryEntry, GetMemoryStatsResponse } from '@frostagent/proto';
import { FrostagentApiService } from '../core/frostagent-api.service';
import { formatDateTime, PageTokenStack } from '../shared/dashboard-utils';

@Component({
  selector: 'app-memory',
  imports: [
    CommonModule,
    MatButtonModule,
    MatCardModule,
    MatChipsModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatPaginatorModule,
    MatProgressBarModule,
    MatSnackBarModule,
    MatTableModule,
    MatToolbarModule,
    MatTooltipModule,
    MatSelectModule,
  ],
  templateUrl: './memory.component.html',
  styleUrl: './memory.component.scss',
})
export class MemoryComponent implements OnInit {
  private readonly api = inject(FrostagentApiService);
  private readonly snackBar = inject(MatSnackBar);
  private readonly pageTokens = new PageTokenStack();

  readonly memories = signal<MemoryEntry[]>([]);
  readonly stats = signal<GetMemoryStatsResponse | null>(null);
  readonly loading = signal(false);
  readonly error = signal('');
  readonly pageSize = signal(20);
  readonly nextToken = signal('');
  readonly total = signal(0);
  readonly pageIndex = signal(0);
  readonly ownerFilter = signal('');

  readonly displayedColumns = [
    'id',
    'owner',
    'content',
    'tags',
    'visibility',
    'importance',
    'createdAt',
    'actions',
  ];

  ngOnInit(): void {
    void this.loadStats();
    void this.loadCurrentPage();
  }

  async refresh(): Promise<void> {
    this.pageTokens.reset();
    this.pageIndex.set(0);
    this.ownerFilter.set('');
    await this.loadStats();
    await this.loadCurrentPage();
  }

  async filterByOwner(owner: string): Promise<void> {
    this.pageTokens.reset();
    this.pageIndex.set(0);
    this.ownerFilter.set(owner);
    await this.loadCurrentPage();
  }

  async handlePageEvent(event: PageEvent): Promise<void> {
    if (event.pageSize !== this.pageSize()) {
      this.pageSize.set(event.pageSize);
      this.pageTokens.reset();
      this.pageIndex.set(0);
      await this.loadCurrentPage();
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

  async deleteMemory(id: string): Promise<void> {
    try {
      const result = await this.api.deleteMemory(id);
      if (result.success) {
        this.snackBar.open(
          $localize`:@@memoryDeleted:记忆已删除`,
          $localize`:@@dismiss:关闭`,
          { duration: 3000 },
        );
        await this.loadStats();
        await this.loadCurrentPage();
      } else {
        this.snackBar.open(
          $localize`:@@memoryDeleteError:删除失败: ${result.error}`,
          $localize`:@@dismiss:关闭`,
          { duration: 5000 },
        );
      }
    } catch (error) {
      this.snackBar.open(
        $localize`:@@memoryDeleteError:删除失败: ${error instanceof Error ? error.message : String(error)}`,
        $localize`:@@dismiss:关闭`,
        { duration: 5000 },
      );
    }
  }

  formatDateTime(value: string): string {
    return formatDateTime(value);
  }

  getSourceIcon(source: string): string {
    switch (source) {
      case 'extract':
        return 'auto_awesome';
      case 'manual':
        return 'edit_note';
      case 'reflect':
        return 'psychology';
      default:
        return 'help_outline';
    }
  }

  trackById(_index: number, item: MemoryEntry): string {
    return item.id;
  }

  private async loadCurrentPage(): Promise<void> {
    this.loading.set(true);
    this.error.set('');

    try {
      const response = await this.api.listMemories(
        this.pageSize(),
        this.pageTokens.current(),
        this.ownerFilter(),
      );
      this.memories.set(response.memories);
      this.nextToken.set(response.pagination?.pageToken ?? '');
      this.total.set(response.pagination?.total ?? response.memories.length);
    } catch (error) {
      this.error.set(error instanceof Error ? error.message : String(error));
    } finally {
      this.loading.set(false);
    }
  }

  private async loadStats(): Promise<void> {
    try {
      this.stats.set(await this.api.getMemoryStats());
    } catch {
      // Stats are non-critical, silently ignore
    }
  }
}