import { Component, inject } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { BrnDialogRef } from '@spartan-ng/brain/dialog';
import { HlmButton } from '@spartan-ng/helm/button';
import {
  HlmDialogFooter,
  HlmDialogHeader,
  HlmDialogTitle,
} from '@spartan-ng/helm/dialog';
import { HlmField, HlmFieldLabel } from '@spartan-ng/helm/field';
import { HlmInput } from '@spartan-ng/helm/input';
import { HlmSelectImports } from '@spartan-ng/helm/select';
import { HlmTextarea } from '@spartan-ng/helm/textarea';

export interface AddMemoryResult {
  content: string;
  tags: string[];
  visibility: string;
  owner: string;
}

@Component({
  selector: 'app-memory-add-dialog',
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
    HlmTextarea,
  ],
  template: `
    <div hlmDialogHeader>
      <h2 hlmDialogTitle>添加记忆</h2>
    </div>

    <div class="grid gap-4 py-2">
      <div hlmField>
        <label hlmFieldLabel for="memory-owner">
          <span>归属者</span>
        </label>
        <input id="memory-owner" hlmInput [(ngModel)]="owner" placeholder="webui" />
      </div>

      <div hlmField>
        <label hlmFieldLabel for="memory-content">
          <span>内容</span>
        </label>
        <textarea
          id="memory-content"
          hlmTextarea
          [(ngModel)]="content"
          rows="4"
          required
        ></textarea>
      </div>

      <div hlmField>
        <label hlmFieldLabel for="memory-tags">
          标签（逗号分隔）
        </label>
        <input
          id="memory-tags"
          hlmInput
          [(ngModel)]="tags"
          placeholder="tag1, tag2"
        />
      </div>

      <div hlmField>
        <label hlmFieldLabel>可见性</label>
        <hlm-select
          [value]="visibility"
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
    </div>

    <div hlmDialogFooter>
      <button hlmBtn variant="outline" (click)="close()">
        <span>取消</span>
      </button>
      <button
        hlmBtn
        [disabled]="!content.trim()"
        (click)="save()"
      >
        <span>保存</span>
      </button>
    </div>
  `,
})
export class MemoryAddDialog {
  private readonly dialogRef = inject(BrnDialogRef<AddMemoryResult>);

  owner = 'webui';
  content = '';
  tags = '';
  visibility = 'private';

  close(): void {
    this.dialogRef.close();
  }

  setVisibility(value: string | null | undefined): void {
    if (value) this.visibility = value;
  }

  save(): void {
    this.dialogRef.close({
      owner: this.owner.trim() || 'webui',
      content: this.content.trim(),
      tags: this.tags
        .split(',')
        .map((tag) => tag.trim())
        .filter(Boolean),
      visibility: this.visibility,
    });
  }
}
