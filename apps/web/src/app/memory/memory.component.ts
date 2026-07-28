import { Component, OnInit, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatChipsModule } from '@angular/material/chips';
import { MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatSnackBar, MatSnackBarModule } from '@angular/material/snack-bar';
import { MatTableModule } from '@angular/material/table';
import { MatToolbarModule } from '@angular/material/toolbar';
import { MatTooltipModule } from '@angular/material/tooltip';
import type { MemoryEntry, GetMemoryStatsResponse } from '@frostagent/proto';
import { FrostagentApiService } from '../core/frostagent-api.service';
import { formatDateTime, PageTokenStack } from '../shared/dashboard-utils';
import { MemoryDetailDialog } from './memory-detail-dialog';
import { MemoryAddDialog, type AddMemoryResult } from './memory-add-dialog';

@Component({
  selector: 'app-memory',
  imports: [
    CommonModule,
    FormsModule,
    MatButtonModule,
    MatCardModule,
    MatCheckboxModule,
    MatChipsModule,
    MatDialogModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatPaginatorModule,
    MatProgressBarModule,
    MatSelectModule,
    MatSnackBarModule,
    MatTableModule,
    MatToolbarModule,
    MatTooltipModule,
  ],
  templateUrl: './memory.component.html',
  styleUrl: './memory.component.scss',
})
export class MemoryComponent implements OnInit {
  private readonly api = inject(FrostagentApiService);
  private readonly snackBar = inject(MatSnackBar);
  private readonly dialog = inject(MatDialog);
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
  readonly sourceFilter = signal('');
  readonly visibilityFilter = signal('');
  readonly searchQuery = signal('');
  readonly selectedIds = signal<Set<string>>(new Set());

  readonly displayedColumns = [
    'select',
    'source',
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
    this.sourceFilter.set('');
    this.visibilityFilter.set('');
    this.searchQuery.set('');
    this.selectedIds.set(new Set());
    await this.loadStats();
    await this.loadCurrentPage();
  }

  async search(): Promise<void> {
    this.pageTokens.reset();
    this.pageIndex.set(0);
    this.selectedIds.set(new Set());
    await this.loadCurrentPage();
  }

  async filterByOwner(owner: string): Promise<void> {
    this.pageTokens.reset();
    this.pageIndex.set(0);
    this.ownerFilter.set(owner);
    this.searchQuery.set('');
    await this.loadCurrentPage();
  }

  clearOwnerFilter(): void {
    this.ownerFilter.set('');
    void this.search();
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

  toggleSelect(id: string): void {
    const set = new Set(this.selectedIds());
    if (set.has(id)) {
      set.delete(id);
    } else {
      set.add(id);
    }
    this.selectedIds.set(set);
  }

  toggleSelectAll(): void {
    const all = this.memories().map(m => m.id);
    const selected = this.selectedIds();
    const allSelected = all.every(id => selected.has(id));
    if (allSelected) {
      this.selectedIds.set(new Set());
    } else {
      this.selectedIds.set(new Set(all));
    }
  }

  isAllSelected(): boolean {
    const mems = this.memories();
    if (mems.length === 0) return false;
    return mems.every(m => this.selectedIds().has(m.id));
  }

  isSomeSelected(): boolean {
    return this.selectedIds().size > 0 && !this.isAllSelected();
  }

  async deleteMemory(id: string): Promise<void> {
    try {
      const result = await this.api.deleteMemory(id);
      if (result.success) {
        this.snackBar.open($localize`:@@memoryDeleted:记忆已删除`, '', { duration: 3000 });
        await this.loadStats();
        await this.loadCurrentPage();
      } else {
        this.snackBar.open($localize`:@@memoryDeleteError:删除失败: ${result.error}`, '', { duration: 5000 });
      }
    } catch (error) {
      this.snackBar.open($localize`:@@memoryDeleteError:删除失败: ${error instanceof Error ? error.message : String(error)}`, '', { duration: 5000 });
    }
  }

  async deleteSelected(): Promise<void> {
    const ids = [...this.selectedIds()];
    if (ids.length === 0) return;

    let success = 0;
    let fail = 0;
    for (const id of ids) {
      try {
        const result = await this.api.deleteMemory(id);
        if (result.success) success++; else fail++;
      } catch {
        fail++;
      }
    }

    this.selectedIds.set(new Set());
    this.snackBar.open($localize`:@@memoryBatchDeleted:已删除 ${success} 条记忆${fail ? `, ${fail} 条失败` : ''}`, '', { duration: 3000 });
    await this.loadStats();
    await this.loadCurrentPage();
  }

  openDetail(memory: MemoryEntry): void {
    const ref = this.dialog.open(MemoryDetailDialog, {
      width: '600px',
      data: { memory },
    });

    ref.afterClosed().subscribe(async (result: MemoryEntry | undefined) => {
      if (!result) return;
      try {
        const r = await this.api.updateMemory(
          result.id,
          result.content,
          result.tags,
          result.visibility,
          result.importance,
        );
        if (r.success) {
          this.snackBar.open($localize`:@@memoryUpdated:记忆已更新`, '', { duration: 3000 });
          await this.loadCurrentPage();
        } else {
          this.snackBar.open($localize`:@@memoryUpdateError:更新失败: ${r.error}`, '', { duration: 5000 });
        }
      } catch (error) {
        this.snackBar.open($localize`:@@memoryUpdateError:更新失败: ${error instanceof Error ? error.message : String(error)}`, '', { duration: 5000 });
      }
    });
  }

  openAddDialog(): void {
    const ref = this.dialog.open(MemoryAddDialog, {
      width: '500px',
    });

    ref.afterClosed().subscribe(async (result: AddMemoryResult | undefined) => {
      if (!result) return;
      try {
        const r = await this.api.addMemory(result.owner, result.content, result.tags, result.visibility);
        if (r.memory) {
          this.snackBar.open($localize`:@@memoryAdded:记忆已添加`, '', { duration: 3000 });
          await this.loadStats();
          await this.loadCurrentPage();
        } else {
          this.snackBar.open($localize`:@@memoryAddError:添加失败: ${r.error}`, '', { duration: 5000 });
        }
      } catch (error) {
        this.snackBar.open($localize`:@@memoryAddError:添加失败: ${error instanceof Error ? error.message : String(error)}`, '', { duration: 5000 });
      }
    });
  }

  async exportMemories(): Promise<void> {
    try {
      const result = await this.api.exportMemories();
      if (result.error) {
        this.snackBar.open($localize`:@@memoryExportError:导出失败: ${result.error}`, '', { duration: 5000 });
        return;
      }
      const blob = new Blob([result.jsonContent], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `memories-export-${new Date().toISOString().slice(0, 10)}.json`;
      a.click();
      URL.revokeObjectURL(url);
      this.snackBar.open($localize`:@@memoryExported:导出成功`, '', { duration: 3000 });
    } catch (error) {
      this.snackBar.open($localize`:@@memoryExportError:导出失败: ${error instanceof Error ? error.message : String(error)}`, '', { duration: 5000 });
    }
  }

  importMemories(event: Event): void {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;

    const reader = new FileReader();
    reader.onload = async () => {
      try {
        const result = await this.api.importMemories(reader.result as string, false);
        if (result.error) {
          this.snackBar.open($localize`:@@memoryImportError:导入失败: ${result.error}`, '', { duration: 5000 });
        } else {
          this.snackBar.open($localize`:@@memoryImported:已导入 ${result.imported} 条，跳过 ${result.skipped} 条`, '', { duration: 3000 });
          await this.loadStats();
          await this.loadCurrentPage();
        }
      } catch (error) {
        this.snackBar.open($localize`:@@memoryImportError:导入失败: ${error instanceof Error ? error.message : String(error)}`, '', { duration: 5000 });
      }
    };
    reader.readAsText(file);
    input.value = '';
  }

  getSourceIcon(source: string): string {
    switch (source) {
      case 'extract': return 'auto_awesome';
      case 'manual': return 'edit_note';
      case 'reflect': return 'psychology';
      default: return 'help_outline';
    }
  }

  getSourceLabel(source: string): string {
    switch (source) {
      case 'extract': return '自动提取';
      case 'manual': return '手动添加';
      case 'reflect': return '反思生成';
      default: return source;
    }
  }

  formatDateTime(value: string): string {
    return formatDateTime(value);
  }

  trackById(_index: number, item: MemoryEntry): string {
    return item.id;
  }

  private async loadCurrentPage(): Promise<void> {
    this.loading.set(true);
    this.error.set('');

    try {
      const query = this.searchQuery();
      if (query) {
        const response = await this.api.searchMemories(query, this.pageSize(), this.pageTokens.current());
        this.memories.set(response.memories);
        this.nextToken.set(response.pagination?.pageToken ?? '');
        this.total.set(response.pagination?.total ?? response.memories.length);
      } else {
        const response = await this.api.listMemories(
          this.pageSize(),
          this.pageTokens.current(),
          this.ownerFilter(),
        );
        this.memories.set(response.memories);
        this.nextToken.set(response.pagination?.pageToken ?? '');
        this.total.set(response.pagination?.total ?? response.memories.length);
      }
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