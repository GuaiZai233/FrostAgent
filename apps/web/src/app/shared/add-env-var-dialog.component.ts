import { Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { Router } from '@angular/router';
import { BrnDialogRef } from '@spartan-ng/brain/dialog';
import { toast } from '@spartan-ng/brain/sonner';
import { HlmButton } from '@spartan-ng/helm/button';
import { HlmCheckbox } from '@spartan-ng/helm/checkbox';
import {
  HlmDialogDescription,
  HlmDialogFooter,
  HlmDialogHeader,
  HlmDialogTitle,
} from '@spartan-ng/helm/dialog';
import { HlmField, HlmFieldLabel } from '@spartan-ng/helm/field';
import { HlmInput } from '@spartan-ng/helm/input';
import { FrostagentApiService } from '../core/frostagent-api.service';
import { AppIconComponent } from './app-icon.component';

@Component({
  selector: 'app-add-env-var-dialog',
  imports: [
    FormsModule,
    HlmButton,
    HlmCheckbox,
    HlmDialogDescription,
    HlmDialogFooter,
    HlmDialogHeader,
    HlmDialogTitle,
    HlmField,
    HlmFieldLabel,
    HlmInput,
    AppIconComponent,
  ],
  template: `
    <div hlmDialogHeader>
      <h2 hlmDialogTitle>新增环境变量</h2>
      <p hlmDialogDescription>
        添加或覆盖后端使用的环境变量。
      </p>
    </div>

    <div class="grid gap-4 py-2">
      <div hlmField>
        <label hlmFieldLabel for="env-key">Key</label>
        <input
          id="env-key"
          hlmInput
          autocomplete="off"
          [ngModel]="key()"
          (ngModelChange)="key.set($event)"
        />
      </div>

      <div hlmField>
        <label hlmFieldLabel for="env-value">Value</label>
        <input
          id="env-value"
          hlmInput
          [type]="isSecret() ? 'password' : 'text'"
          autocomplete="off"
          [ngModel]="value()"
          (ngModelChange)="value.set($event)"
        />
      </div>

      <label hlmFieldLabel class="cursor-pointer">
        <hlm-checkbox
          [checked]="isSecret()"
          (checkedChange)="isSecret.set($event)"
        />
        <span>这是敏感信息</span>
      </label>
    </div>

    <div hlmDialogFooter>
      <button hlmBtn variant="outline" (click)="close()">
        <span>取消</span>
      </button>
      <button
        hlmBtn
        [disabled]="!key().trim() || saving()"
        (click)="save()"
      >
        <app-icon>save</app-icon>
        <span>保存</span>
      </button>
    </div>
  `,
})
export class AddEnvVarDialogComponent {
  private readonly api = inject(FrostagentApiService);
  private readonly dialogRef = inject(BrnDialogRef<boolean>);
  private readonly router = inject(Router);

  readonly key = signal('');
  readonly value = signal('');
  readonly isSecret = signal(false);
  readonly saving = signal(false);

  close(): void {
    this.dialogRef.close(false);
  }

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
        toast.success('环境变量已保存', {
          duration: 2500,
        });
        this.dialogRef.close(true);
        void this.router.navigate(['/settings/backend']);
      } else {
        toast.error(response.error, { duration: 5000 });
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : String(error), {
        duration: 5000,
      });
    } finally {
      this.saving.set(false);
    }
  }
}
