import { Component, OnInit, computed, inject, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import type { DialogueItem } from '@frostagent/proto';
import { toast } from '@spartan-ng/brain/sonner';
import { HlmBadge } from '@spartan-ng/helm/badge';
import { HlmButton } from '@spartan-ng/helm/button';
import { HlmCardImports } from '@spartan-ng/helm/card';
import { HlmDialogService } from '@spartan-ng/helm/dialog';
import { HlmField, HlmFieldLabel } from '@spartan-ng/helm/field';
import { HlmInput } from '@spartan-ng/helm/input';
import { HlmSpinner } from '@spartan-ng/helm/spinner';
import { HlmTextarea } from '@spartan-ng/helm/textarea';
import { firstValueFrom } from 'rxjs';
import { FrostagentApiService } from '../core/frostagent-api.service';
import { AppIconComponent } from '../shared/app-icon.component';
import { DialogueEditDialog, type DialogueEditDialogData } from './dialogue-edit-dialog';

@Component({
  selector: 'app-dialogue',
  imports: [
    CommonModule,
    FormsModule,
    HlmBadge,
    HlmButton,
    HlmCardImports,
    HlmField,
    HlmFieldLabel,
    HlmInput,
    HlmSpinner,
    HlmTextarea,
    AppIconComponent,
  ],
  templateUrl: './dialogue.component.html',
})
export class DialogueComponent implements OnInit {
  private readonly api = inject(FrostagentApiService);
  private readonly dialog = inject(HlmDialogService);

  readonly dialogues = signal<DialogueItem[]>([]);
  readonly loading = signal(false);
  readonly saving = signal(false);
  readonly error = signal('');
  readonly filePath = signal('eval/dialogue/dialogue.yml');
  readonly promptPreview = signal('');
  readonly searchQuery = signal('');
  readonly selectedRelation = signal('');
  readonly viewMode = signal<'visual' | 'raw'>('visual');
  readonly rawYaml = signal('');
  readonly rawLoading = signal(false);
  readonly rawSaving = signal(false);
  readonly showPromptPreview = signal(true);

  readonly availableRelations = computed(() => {
    const set = new Set<string>();
    for (const d of this.dialogues()) {
      if (d.relation) {
        set.add(d.relation.trim());
      }
    }
    return Array.from(set);
  });

  readonly filteredDialogues = computed(() => {
    const list = this.dialogues();
    const query = this.searchQuery().trim().toLowerCase();
    const rel = this.selectedRelation().trim();

    return list.filter((item) => {
      if (rel && item.relation !== rel) {
        return false;
      }
      if (!query) {
        return true;
      }
      return (
        item.user.toLowerCase().includes(query) ||
        item.preferred.toLowerCase().includes(query) ||
        item.scene.toLowerCase().includes(query) ||
        item.relation.toLowerCase().includes(query) ||
        item.id.toLowerCase().includes(query)
      );
    });
  });

  ngOnInit(): void {
    void this.loadDialogues();
  }

  async loadDialogues(): Promise<void> {
    this.loading.set(true);
    this.error.set('');
    try {
      const resp = await this.api.listDialogues();
      this.dialogues.set(resp.dialogues || []);
      this.promptPreview.set(resp.promptPreview || '');
      if (resp.filePath) {
        this.filePath.set(resp.filePath);
      }
    } catch (err) {
      this.error.set(err instanceof Error ? err.message : String(err));
    } finally {
      this.loading.set(false);
    }
  }

  async openAddDialog(): Promise<void> {
    // Generate next default numeric ID
    const currentList = this.dialogues();
    let maxId = 0;
    for (const item of currentList) {
      const num = parseInt(item.id, 10);
      if (!isNaN(num) && num > maxId) {
        maxId = num;
      }
    }
    const nextId = String(maxId + 1);

    const ref = this.dialog.open<DialogueEditDialogData | null, { data: DialogueEditDialogData }>(
      DialogueEditDialog,
      {
        contentClass: 'sm:max-w-xl',
        context: {
          data: {
            id: nextId,
            scene: '',
            relation: '熟人',
            user: '',
            preferred: '',
            isEdit: false,
          },
        },
      },
    );

    const result = await firstValueFrom(ref.closed$);
    if (!result) return;

    const newItem: DialogueItem = {
      id: result.id,
      scene: result.scene,
      relation: result.relation,
      user: result.user,
      preferred: result.preferred,
    } as DialogueItem;

    const updatedList = [...this.dialogues(), newItem];
    await this.saveList(updatedList, '示例对话添加成功');
  }

  async openEditDialog(item: DialogueItem, originalIndex: number): Promise<void> {
    const ref = this.dialog.open<DialogueEditDialogData | null, { data: DialogueEditDialogData }>(
      DialogueEditDialog,
      {
        contentClass: 'sm:max-w-xl',
        context: {
          data: {
            id: item.id,
            scene: item.scene,
            relation: item.relation,
            user: item.user,
            preferred: item.preferred,
            isEdit: true,
          },
        },
      },
    );

    const result = await firstValueFrom(ref.closed$);
    if (!result) return;

    const updatedList = [...this.dialogues()];
    // Find index in actual dialogues array
    const targetIdx = originalIndex >= 0 ? originalIndex : updatedList.findIndex((d) => d.id === item.id);
    if (targetIdx >= 0) {
      updatedList[targetIdx] = {
        id: result.id,
        scene: result.scene,
        relation: result.relation,
        user: result.user,
        preferred: result.preferred,
      } as DialogueItem;
      await this.saveList(updatedList, '示例对话已更新');
    }
  }

  async deleteDialogue(item: DialogueItem, index: number): Promise<void> {
    const updatedList = [...this.dialogues()];
    const targetIdx = index >= 0 ? index : updatedList.findIndex((d) => d.id === item.id);
    if (targetIdx >= 0) {
      updatedList.splice(targetIdx, 1);
      await this.saveList(updatedList, '示例对话已删除');
    }
  }

  async moveUp(index: number): Promise<void> {
    if (index <= 0) return;
    const list = [...this.dialogues()];
    const temp = list[index - 1];
    list[index - 1] = list[index];
    list[index] = temp;
    await this.saveList(list, '顺序已调整');
  }

  async moveDown(index: number): Promise<void> {
    const list = [...this.dialogues()];
    if (index >= list.length - 1) return;
    const temp = list[index + 1];
    list[index + 1] = list[index];
    list[index] = temp;
    await this.saveList(list, '顺序已调整');
  }

  private async saveList(newList: DialogueItem[], successMsg: string): Promise<void> {
    this.saving.set(true);
    try {
      const resp = await this.api.saveDialogues(newList);
      if (resp.success) {
        this.dialogues.set(newList);
        if (resp.promptPreview) {
          this.promptPreview.set(resp.promptPreview);
        }
        toast.success(successMsg, { duration: 3000 });
      } else {
        toast.error(`保存失败: ${resp.error}`, { duration: 5000 });
      }
    } catch (err) {
      toast.error(`保存失败: ${err instanceof Error ? err.message : String(err)}`, {
        duration: 5000,
      });
    } finally {
      this.saving.set(false);
    }
  }

  async setViewMode(mode: 'visual' | 'raw'): Promise<void> {
    this.viewMode.set(mode);
    if (mode === 'raw') {
      await this.loadRawYaml();
    }
  }

  async loadRawYaml(): Promise<void> {
    this.rawLoading.set(true);
    try {
      const resp = await this.api.getRawDialogueFile();
      this.rawYaml.set(resp.content);
    } catch (err) {
      toast.error(`加载原始文件失败: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      this.rawLoading.set(false);
    }
  }

  async saveRawYaml(): Promise<void> {
    this.rawSaving.set(true);
    try {
      const resp = await this.api.updateRawDialogueFile(this.rawYaml());
      if (resp.success) {
        if (resp.promptPreview) {
          this.promptPreview.set(resp.promptPreview);
        }
        toast.success('原始 YAML 保存成功并已实时生效！', { duration: 3000 });
        // Reload visual list
        await this.loadDialogues();
      } else {
        toast.error(`保存失败: ${resp.error}`, { duration: 6000 });
      }
    } catch (err) {
      toast.error(`保存失败: ${err instanceof Error ? err.message : String(err)}`, {
        duration: 6000,
      });
    } finally {
      this.rawSaving.set(false);
    }
  }

  copyPromptPreview(): void {
    const text = this.promptPreview();
    if (!text) return;
    navigator.clipboard.writeText(text).then(
      () => toast.success('提示词片段已复制到剪贴板', { duration: 3000 }),
      () => toast.error('复制失败，请手动选择复制'),
    );
  }

  exportYaml(): void {
    const text = this.rawYaml() || this.promptPreview();
    this.api.getRawDialogueFile().then((resp) => {
      const content = resp.content || text;
      const blob = new Blob([content], { type: 'text/yaml;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `dialogue-examples-${new Date().toISOString().slice(0, 10)}.yml`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success('已导出示例对话文件', { duration: 3000 });
    });
  }

  importYaml(event: Event): void {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;

    const reader = new FileReader();
    reader.onload = async () => {
      try {
        const content = reader.result as string;
        const resp = await this.api.updateRawDialogueFile(content);
        if (resp.success) {
          toast.success('导入 YAML 成功并已实时生效', { duration: 3000 });
          await this.loadDialogues();
        } else {
          toast.error(`导入失败: ${resp.error}`, { duration: 6000 });
        }
      } catch (err) {
        toast.error(`导入解析失败: ${err instanceof Error ? err.message : String(err)}`);
      }
    };
    reader.readAsText(file);
    input.value = '';
  }

  getRelationBadgeVariant(relation: string): 'default' | 'secondary' | 'outline' {
    switch (relation) {
      case '主人':
        return 'default';
      case '熟人':
      case '朋友':
        return 'secondary';
      default:
        return 'outline';
    }
  }
}
