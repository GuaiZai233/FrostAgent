import { Component, inject } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatChipsModule } from '@angular/material/chips';
import { MatDialogRef, MAT_DIALOG_DATA, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatSliderModule } from '@angular/material/slider';
import type { MemoryEntry } from '@frostagent/proto';

export interface MemoryDetailDialogData {
  memory: MemoryEntry;
}

@Component({
  selector: 'app-memory-detail-dialog',
  imports: [
    FormsModule,
    MatButtonModule,
    MatChipsModule,
    MatDialogModule,
    MatFormFieldModule,
    MatInputModule,
    MatSelectModule,
    MatSliderModule,
  ],
  template: `
    <h2 mat-dialog-title i18n="@@memoryDetail">记忆详情</h2>
    <mat-dialog-content>
      <p><strong i18n="@@memoryId">ID</strong><br/>{{ data.memory.id }}</p>
      <p><strong i18n="@@memoryOwner">归属者</strong><br/>{{ data.memory.owner }}</p>
      <p><strong i18n="@@memorySource">来源</strong><br/>{{ data.memory.source }}</p>
      <p><strong i18n="@@memoryCreatedAt">创建时间</strong><br/>{{ data.memory.createdAt }}</p>
      <p><strong i18n="@@memoryUpdatedAt">更新时间</strong><br/>{{ data.memory.updatedAt }}</p>

      <mat-form-field appearance="outline" class="full-width">
        <mat-label i18n="@@memoryContent">内容</mat-label>
        <textarea matInput [(ngModel)]="editedContent" rows="4"></textarea>
      </mat-form-field>

      <mat-form-field appearance="outline" class="full-width">
        <mat-label i18n="@@memoryTagsCommaSeparated">标签（逗号分隔）</mat-label>
        <input matInput [(ngModel)]="editedTags" placeholder="tag1, tag2, tag3" />
      </mat-form-field>

      <mat-form-field appearance="outline" class="full-width">
        <mat-label i18n="@@memoryVisibility">可见性</mat-label>
        <mat-select [(ngModel)]="editedVisibility">
          <mat-option value="private">🔒 Private</mat-option>
          <mat-option value="public">🌐 Public</mat-option>
        </mat-select>
      </mat-form-field>

      <div class="slider-row">
        <label i18n="@@memoryImportanceSlider">重要度: {{ editedImportance.toFixed(2) }}</label>
        <mat-slider min="0" max="1" step="0.01" class="full-width">
          <input matSliderThumb [(ngModel)]="editedImportance" />
        </mat-slider>
      </div>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button mat-dialog-close i18n="@@cancel">取消</button>
      <button mat-raised-button color="primary" (click)="save()" i18n="@@save">保存</button>
    </mat-dialog-actions>
  `,
  styles: [`
    .full-width { width: 100%; margin-bottom: 12px; }
    .slider-row { margin: 16px 0; }
    .slider-row label { display: block; margin-bottom: 8px; }
  `],
})
export class MemoryDetailDialog {
  readonly data: MemoryDetailDialogData = inject(MAT_DIALOG_DATA);
  private readonly dialogRef = inject(MatDialogRef<MemoryDetailDialog, MemoryEntry>);

  editedContent = this.data.memory.content;
  editedTags = (this.data.memory.tags ?? []).join(', ');
  editedVisibility = this.data.memory.visibility;
  editedImportance = this.data.memory.importance;

  save(): void {
    this.dialogRef.close({
      ...this.data.memory,
      content: this.editedContent,
      tags: this.editedTags.split(',').map(t => t.trim()).filter(Boolean),
      visibility: this.editedVisibility,
      importance: this.editedImportance,
    } as MemoryEntry);
  }
}
