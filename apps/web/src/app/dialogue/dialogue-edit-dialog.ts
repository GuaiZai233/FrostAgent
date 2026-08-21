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
import { HlmTextarea } from '@spartan-ng/helm/textarea';
import { HlmBadge } from '@spartan-ng/helm/badge';
import { AppIconComponent } from '../shared/app-icon.component';

export interface DialogueEditDialogData {
  id: string;
  scene: string;
  relation: string;
  user: string;
  preferred: string;
  isEdit: boolean;
}

@Component({
  selector: 'app-dialogue-edit-dialog',
  imports: [
    FormsModule,
    HlmButton,
    HlmDialogFooter,
    HlmDialogHeader,
    HlmDialogTitle,
    HlmField,
    HlmFieldLabel,
    HlmInput,
    HlmTextarea,
    HlmBadge,
    AppIconComponent,
  ],
  template: `
    <div hlmDialogHeader>
      <h2 hlmDialogTitle class="flex items-center gap-2">
        <app-icon class="text-primary">{{ isEdit ? 'edit_note' : 'add_comment' }}</app-icon>
        <span>{{ isEdit ? '编辑示例对话' : '添加示例对话' }}</span>
      </h2>
    </div>

    <div class="grid gap-4 py-3">
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div hlmField>
          <label hlmFieldLabel for="dialogue-id">
            <span>编号 (ID)</span>
          </label>
          <input
            id="dialogue-id"
            hlmInput
            [(ngModel)]="id"
            placeholder="例如 1, 2, ex-01"
          />
        </div>

        <div hlmField class="sm:col-span-2">
          <label hlmFieldLabel for="dialogue-relation">
            <span>关系 (Relation)</span>
          </label>
          <input
            id="dialogue-relation"
            hlmInput
            [(ngModel)]="relation"
            placeholder="如：熟人、朋友、群友、主人"
          />
          <div class="mt-1.5 flex flex-wrap gap-1.5">
            @for (preset of presetRelations; track preset) {
              <button
                type="button"
                hlmBadge
                variant="outline"
                class="hover:bg-muted/80 cursor-pointer text-xs"
                (click)="relation = preset"
              >
                {{ preset }}
              </button>
            }
          </div>
        </div>
      </div>

      <div hlmField>
        <label hlmFieldLabel for="dialogue-scene">
          <span>场景描述 (Scene，选填)</span>
        </label>
        <input
          id="dialogue-scene"
          hlmInput
          [(ngModel)]="scene"
          placeholder="如：日常问候、撒娇、被夸奖、情绪安抚等"
        />
      </div>

      <div hlmField>
        <label hlmFieldLabel for="dialogue-user" class="font-medium text-foreground">
          <span class="text-destructive">*</span>
          <span>用户输入 (User)</span>
        </label>
        <textarea
          id="dialogue-user"
          hlmTextarea
          [(ngModel)]="user"
          rows="3"
          placeholder="输入用户的提问或触发语句..."
          required
        ></textarea>
      </div>

      <div hlmField>
        <label hlmFieldLabel for="dialogue-preferred" class="font-medium text-foreground">
          <span class="text-destructive">*</span>
          <span>期望回复 (Preferred / Assistant)</span>
        </label>
        <textarea
          id="dialogue-preferred"
          hlmTextarea
          [(ngModel)]="preferred"
          rows="4"
          placeholder="输入智能体人设期望的回复示范（体现语气、句式与性格）..."
          required
        ></textarea>
      </div>
    </div>

    <div hlmDialogFooter>
      <button hlmBtn variant="outline" (click)="close()">
        <span>取消</span>
      </button>
      <button
        hlmBtn
        [disabled]="!isValid()"
        (click)="save()"
      >
        <app-icon class="mr-1">check</app-icon>
        <span>保存</span>
      </button>
    </div>
  `,
})
export class DialogueEditDialog {
  private readonly dialogRef = inject(BrnDialogRef<DialogueEditDialogData | null>);
  private readonly context = injectBrnDialogContext<{ data?: DialogueEditDialogData }>();

  id = this.context?.data?.id ?? '1';
  scene = this.context?.data?.scene ?? '';
  relation = this.context?.data?.relation ?? '熟人';
  user = this.context?.data?.user ?? '';
  preferred = this.context?.data?.preferred ?? '';
  isEdit = Boolean(this.context?.data?.isEdit);

  readonly presetRelations = ['熟人', '朋友', '群友', '主人', '陌生人'];

  isValid(): boolean {
    return Boolean(this.user.trim() && this.preferred.trim());
  }

  close(): void {
    this.dialogRef.close(null);
  }

  save(): void {
    if (!this.isValid()) return;
    this.dialogRef.close({
      id: this.id.trim() || '1',
      scene: this.scene.trim(),
      relation: this.relation.trim() || '熟人',
      user: this.user.trim(),
      preferred: this.preferred.trim(),
      isEdit: this.isEdit,
    });
  }
}
