import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Router } from '@angular/router';
import { FrostagentApiService } from '../core/frostagent-api.service';

@Component({
  selector: 'app-add-env-var-dialog',
  imports: [
    FormsModule,
    MatButtonModule,
    MatCheckboxModule,
    MatDialogModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
  ],
  template: `
    <h2 mat-dialog-title i18n="@@addEnvVar">新增环境变量</h2>
    <mat-dialog-content>
      <div
        style="display: flex; flex-direction: column; gap: 16px; padding-top: 8px;"
      >
        <mat-form-field appearance="outline">
          <mat-label i18n="@@key">Key</mat-label>
          <input matInput [ngModel]="key()" (ngModelChange)="key.set($event)" />
        </mat-form-field>
        <mat-form-field appearance="outline">
          <mat-label i18n="@@value">Value</mat-label>
          <input
            matInput
            [ngModel]="value()"
            (ngModelChange)="value.set($event)"
          />
        </mat-form-field>
        <mat-checkbox
          [ngModel]="isSecret()"
          (ngModelChange)="isSecret.set($event)"
        >
          <span i18n="@@isSecret">这是敏感信息</span>
        </mat-checkbox>
      </div>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button mat-dialog-close i18n="@@cancel">取消</button>
      <button
        mat-flat-button
        [disabled]="!key().trim() || saving()"
        (click)="save()"
      >
        <mat-icon>save</mat-icon>
        <span i18n="@@save">保存</span>
      </button>
    </mat-dialog-actions>
  `,
})
export class AddEnvVarDialogComponent {
  private readonly api = inject(FrostagentApiService);
  private readonly dialogRef = inject(MatDialogRef<AddEnvVarDialogComponent>);
  private readonly snackBar = inject(MatSnackBar);
  private readonly router = inject(Router);

  readonly key = signal('');
  readonly value = signal('');
  readonly isSecret = signal(false);
  readonly saving = signal(false);

  async save(): Promise<void> {
    const key = this.key().trim();
    if (!key) return;

    this.saving.set(true);
    try {
      const response = await this.api.updateEnvVar({
        key,
        value: this.value(),
        isSecret: this.isSecret(),
      });

      if (response.success) {
        this.snackBar.open($localize`:@@envSaved:环境变量已保存`, undefined, {
          duration: 2500,
        });
        this.dialogRef.close(true);
        void this.router.navigate(['/settings/backend']);
      } else {
        this.snackBar.open(response.error, $localize`:@@close:关闭`, {
          duration: 5000,
        });
      }
    } catch (error) {
      this.snackBar.open(
        error instanceof Error ? error.message : String(error),
        $localize`:@@close:关闭`,
        { duration: 5000 },
      );
    } finally {
      this.saving.set(false);
    }
  }
}
