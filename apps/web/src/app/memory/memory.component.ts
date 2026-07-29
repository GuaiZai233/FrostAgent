import { Component, OnInit, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import type { MemoryEntry, GetMemoryStatsResponse } from '@frostagent/proto';
import { toast } from '@spartan-ng/brain/sonner';
import { HlmBadge } from '@spartan-ng/helm/badge';
import { HlmButton } from '@spartan-ng/helm/button';
import { HlmCardImports } from '@spartan-ng/helm/card';
import { HlmCheckbox } from '@spartan-ng/helm/checkbox';
import { HlmDialogService } from '@spartan-ng/helm/dialog';
import { HlmField, HlmFieldLabel } from '@spartan-ng/helm/field';
import { HlmInput } from '@spartan-ng/helm/input';
import { HlmPaginationImports } from '@spartan-ng/helm/pagination';
import { HlmSelectImports } from '@spartan-ng/helm/select';
import { HlmSpinner } from '@spartan-ng/helm/spinner';
import { HlmTableImports } from '@spartan-ng/helm/table';
import { firstValueFrom } from 'rxjs';
import { FrostagentApiService } from '../core/frostagent-api.service';
import { AppIconComponent } from '../shared/app-icon.component';
import { formatDateTime, PageTokenStack } from '../shared/dashboard-utils';
import { MemoryDetailDialog } from './memory-detail-dialog';
import { MemoryAddDialog, type AddMemoryResult } from './memory-add-dialog';

@Component({
  selector: 'app-memory',
  imports: [
    CommonModule,
    FormsModule,
    HlmBadge,
    HlmButton,
    HlmCardImports,
    HlmCheckbox,
    HlmField,
    HlmFieldLabel,
    HlmInput,
    HlmPaginationImports,
    HlmSelectImports,
    HlmSpinner,
    HlmTableImports,
    AppIconComponent,
  ],
  templateUrl: './memory.component.html',
})
export class MemoryComponent implements OnInit {
  private readonly api = inject(FrostagentApiService);
  private readonly dialog = inject(HlmDialogService);
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
  readonly tagFilter = signal('');
  readonly selectedIds = signal<Set<string>>(new Set());

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
    this.tagFilter.set('');
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
    this.tagFilter.set('');
    await this.loadCurrentPage();
  }

  clearOwnerFilter(): void {
    this.ownerFilter.set('');
    void this.search();
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
    this.pageTokens.reset();
    this.pageIndex.set(0);
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
        toast.success($localize`:@@memoryDeleted:记忆已删除`, { duration: 3000 });
        await this.loadStats();
        await this.loadCurrentPage();
      } else {
        toast.error($localize`:@@memoryDeleteError:删除失败: ${result.error}`, { duration: 5000 });
      }
    } catch (error) {
      toast.error($localize`:@@memoryDeleteError:删除失败: ${error instanceof Error ? error.message : String(error)}`, { duration: 5000 });
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
    const message = $localize`:@@memoryBatchDeleted:已删除 ${success} 条记忆${fail ? `, ${fail} 条失败` : ''}`;
    (fail ? toast.warning : toast.success)(message, { duration: 3000 });
    await this.loadStats();
    await this.loadCurrentPage();
  }

  async openDetail(memory: MemoryEntry): Promise<void> {
    const ref = this.dialog.open<MemoryEntry, { memory: MemoryEntry }>(
      MemoryDetailDialog,
      {
        contentClass: 'sm:max-w-2xl',
        context: { memory },
      },
    );
    const result = await firstValueFrom(ref.closed$);
    if (!result) return;

    try {
      const response = await this.api.updateMemory(
        result.id,
        result.content,
        result.tags,
        result.visibility,
        result.importance,
      );
      if (response.success) {
        toast.success($localize`:@@memoryUpdated:记忆已更新`, {
          duration: 3000,
        });
        await this.loadCurrentPage();
      } else {
        toast.error(
          $localize`:@@memoryUpdateError:更新失败: ${response.error}`,
          { duration: 5000 },
        );
      }
    } catch (error) {
      toast.error(
        $localize`:@@memoryUpdateError:更新失败: ${error instanceof Error ? error.message : String(error)}`,
        { duration: 5000 },
      );
    }
  }

  async openAddDialog(): Promise<void> {
    const ref = this.dialog.open<AddMemoryResult>(MemoryAddDialog, {
      contentClass: 'sm:max-w-lg',
    });
    const result = await firstValueFrom(ref.closed$);
    if (!result) return;

    try {
      const response = await this.api.addMemory(
        result.owner,
        result.content,
        result.tags,
        result.visibility,
      );
      if (response.memory) {
        toast.success($localize`:@@memoryAdded:记忆已添加`, {
          duration: 3000,
        });
        await this.loadStats();
        await this.loadCurrentPage();
      } else {
        toast.error(
          $localize`:@@memoryAddError:添加失败: ${response.error}`,
          { duration: 5000 },
        );
      }
    } catch (error) {
      toast.error(
        $localize`:@@memoryAddError:添加失败: ${error instanceof Error ? error.message : String(error)}`,
        { duration: 5000 },
      );
    }
  }

  async exportMemories(): Promise<void> {
    try {
      const result = await this.api.exportMemories();
      if (result.error) {
        toast.error($localize`:@@memoryExportError:导出失败: ${result.error}`, { duration: 5000 });
        return;
      }
      const blob = new Blob([result.jsonContent], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `memories-export-${new Date().toISOString().slice(0, 10)}.json`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success($localize`:@@memoryExported:导出成功`, { duration: 3000 });
    } catch (error) {
      toast.error($localize`:@@memoryExportError:导出失败: ${error instanceof Error ? error.message : String(error)}`, { duration: 5000 });
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
          toast.error($localize`:@@memoryImportError:导入失败: ${result.error}`, { duration: 5000 });
        } else {
          toast.success($localize`:@@memoryImported:已导入 ${result.imported} 条，跳过 ${result.skipped} 条`, { duration: 3000 });
          await this.loadStats();
          await this.loadCurrentPage();
        }
      } catch (error) {
        toast.error($localize`:@@memoryImportError:导入失败: ${error instanceof Error ? error.message : String(error)}`, { duration: 5000 });
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
      const tags = this.tagFilter()
        .split(/[,，]/)
        .map((tag) => tag.trim())
        .filter(Boolean);
      if (query || tags.length > 0) {
        const response = await this.api.searchMemories(
          query,
          this.pageSize(),
          this.pageTokens.current(),
          tags,
        );
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
