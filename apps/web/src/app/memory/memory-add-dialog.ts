import { Component, inject } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogRef, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';

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
    MatButtonModule,
    MatDialogModule,
    MatFormFieldModule,
    MatInputModule,
    MatSelectModule,
  ],
  template: `
    <h2 mat-dialog-title i18n="@@addMemoryDialog">添加记忆</h2>
    <mat-dialog-content>
      <mat-form-field appearance="outline" class="full-width">
        <mat-label i18n="@@memoryOwner">归属者</mat-label>
        <input matInput [(ngModel)]="owner" placeholder="webui" />
      </mat-form-field>

      <mat-form-field appearance="outline" class="full-width">
        <mat-label i18n="@@memoryContent">内容</mat-label>
        <textarea matInput [(ngModel)]="content" rows="4" required></textarea>
      </mat-form-field>

      <mat-form-field appearance="outline" class="full-width">
        <mat-label i18n="@@memoryTagsCommaSeparated">标签（逗号分隔）</mat-label>
        <input matInput [(ngModel)]="tags" placeholder="tag1, tag2" />
      </mat-form-field>

      <mat-form-field appearance="outline" class="full-width">
        <mat-label i18n="@@memoryVisibility">可见性</mat-label>
        <mat-select [(ngModel)]="visibility">
          <mat-option value="private">🔒 Private</mat-option>
          <mat-option value="public">🌐 Public</mat-option>
        </mat-select>
      </mat-form-field>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button mat-dialog-close i18n="@@cancel">取消</button>
      <button mat-raised-button color="primary" [disabled]="!content.trim()" (click)="save()" i18n="@@save">保存</button>
    </mat-dialog-actions>
  `,
  styles: [`
    .full-width { width: 100%; margin-bottom: 12px; }
  `],
})
export class MemoryAddDialog {
  private readonly dialogRef = inject(MatDialogRef<MemoryAddDialog, AddMemoryResult>);

  owner = 'webui';
  content = '';
  tags = '';
  visibility = 'private';

  save(): void {
    this.dialogRef.close({
      owner: this.owner.trim() || 'webui',
      content: this.content.trim(),
      tags: this.tags.split(',').map(t => t.trim()).filter(Boolean),
      visibility: this.visibility,
    });
  }
}