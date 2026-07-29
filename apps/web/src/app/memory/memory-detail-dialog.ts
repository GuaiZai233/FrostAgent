import { Component, inject } from '@angular/core';
import { FormsModule } from '@angular/forms';
import {
  BrnDialogRef,
  injectBrnDialogContext,
} from '@spartan-ng/brain/dialog';
import { HlmButton } from '@spartan-ng/helm/button';
import {
  HlmDialogFooter,
  HlmDialogHeader,
  HlmDialogTitle,
} from '@spartan-ng/helm/dialog';
import { HlmField, HlmFieldLabel } from '@spartan-ng/helm/field';
import { HlmInput } from '@spartan-ng/helm/input';
import { HlmSelectImports } from '@spartan-ng/helm/select';
import { HlmSlider } from '@spartan-ng/helm/slider';
import { HlmTextarea } from '@spartan-ng/helm/textarea';
import type { MemoryEntry } from '@frostagent/proto';

export interface MemoryDetailDialogData {
  memory: MemoryEntry;
}

@Component({
  selector: 'app-memory-detail-dialog',
  imports: [
    FormsModule,
    HlmButton,
    HlmDialogFooter,
    HlmDialogHeader,
    HlmDialogTitle,
    HlmField,
    HlmFieldLabel,
    HlmInput,
    HlmSelectImports,
    HlmSlider,
    HlmTextarea,
  ],
  template: `
    <div hlmDialogHeader>
      <h2 hlmDialogTitle i18n="@@memoryDetail">记忆详情</h2>
    </div>

    <dl class="grid grid-cols-2 gap-x-4 gap-y-2 rounded-lg border p-3 text-sm">
      <div class="col-span-2">
        <dt class="text-muted-foreground" i18n="@@memoryId">ID</dt>
        <dd class="break-all">{{ data.memory.id }}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground" i18n="@@memoryOwner">归属者</dt>
        <dd>{{ data.memory.owner }}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground" i18n="@@memorySource">来源</dt>
        <dd>{{ data.memory.source }}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground" i18n="@@memoryCreatedAt">创建时间</dt>
        <dd>{{ data.memory.createdAt }}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground" i18n="@@memoryUpdatedAt">更新时间</dt>
        <dd>{{ data.memory.updatedAt }}</dd>
      </div>
    </dl>

    <div class="grid max-h-[55vh] gap-4 overflow-y-auto py-2 pe-1">
      <div hlmField>
        <label hlmFieldLabel for="detail-content">
          <span i18n="@@memoryContent">内容</span>
        </label>
        <textarea
          id="detail-content"
          hlmTextarea
          [(ngModel)]="editedContent"
          rows="4"
        ></textarea>
      </div>

      <div hlmField>
        <label hlmFieldLabel for="detail-tags" i18n="@@memoryTagsCommaSeparated">
          标签（逗号分隔）
        </label>
        <input
          id="detail-tags"
          hlmInput
          [(ngModel)]="editedTags"
          placeholder="tag1, tag2, tag3"
        />
      </div>

      <div hlmField>
        <label hlmFieldLabel i18n="@@memoryVisibility">可见性</label>
        <hlm-select
          [value]="editedVisibility"
          (valueChange)="setVisibility($event)"
        >
          <hlm-select-trigger class="w-full">
            <hlm-select-value />
          </hlm-select-trigger>
          <hlm-select-content *hlmSelectPortal>
            <hlm-select-item value="private">🔒 Private</hlm-select-item>
            <hlm-select-item value="public">🌐 Public</hlm-select-item>
          </hlm-select-content>
        </hlm-select>
      </div>

      <div hlmField>
        <label hlmFieldLabel id="importance-label" i18n="@@memoryImportanceSlider">
          重要度: {{ editedImportance.toFixed(2) }}
        </label>
        <hlm-slider
          aria-labelledby="importance-label"
          [min]="0"
          [max]="1"
          [step]="0.01"
          [value]="[editedImportance]"
          (valueChange)="setImportance($event)"
        />
      </div>
    </div>

    <div hlmDialogFooter>
      <button hlmBtn variant="outline" (click)="close()">
        <span i18n="@@cancel">取消</span>
      </button>
      <button hlmBtn (click)="save()">
        <span i18n="@@save">保存</span>
      </button>
    </div>
  `,
})
export class MemoryDetailDialog {
  readonly data = injectBrnDialogContext<MemoryDetailDialogData>();
  private readonly dialogRef = inject(BrnDialogRef<MemoryEntry>);

  editedContent = this.data.memory.content;
  editedTags = (this.data.memory.tags ?? []).join(', ');
  editedVisibility = this.data.memory.visibility;
  editedImportance = this.data.memory.importance;

  close(): void {
    this.dialogRef.close();
  }

  setImportance(value: number[]): void {
    this.editedImportance = value[0] ?? 0;
  }

  setVisibility(value: string | null | undefined): void {
    if (value) this.editedVisibility = value;
  }

  save(): void {
    this.dialogRef.close({
      ...this.data.memory,
      content: this.editedContent,
      tags: this.editedTags
        .split(',')
        .map((tag) => tag.trim())
        .filter(Boolean),
      visibility: this.editedVisibility,
      importance: this.editedImportance,
    } as MemoryEntry);
  }
}
