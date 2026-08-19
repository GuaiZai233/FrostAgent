import {
  Component,
  OnInit,
  AfterViewInit,
  OnDestroy,
  inject,
  signal,
  viewChild,
  ElementRef,
  effect,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { RouterModule } from '@angular/router';
import { toast } from '@spartan-ng/brain/sonner';
import { HlmButton } from '@spartan-ng/helm/button';
import { HlmCardImports } from '@spartan-ng/helm/card';
import { HlmCheckbox } from '@spartan-ng/helm/checkbox';
import { HlmField, HlmFieldLabel } from '@spartan-ng/helm/field';
import { HlmInput } from '@spartan-ng/helm/input';
import { HlmSpinner } from '@spartan-ng/helm/spinner';
import { HlmTableImports } from '@spartan-ng/helm/table';
import { HlmTabsImports } from '@spartan-ng/helm/tabs';
import { ThemeService } from '../shared/theme.service';

import { EditorView, basicSetup } from 'codemirror';
import { Compartment } from '@codemirror/state';
import { StreamLanguage } from '@codemirror/language';
import { properties } from '@codemirror/legacy-modes/mode/properties';
import { synthwave84 } from '@fsegurai/codemirror-theme-synthwave-84';

import type { EnvVar } from '@frostagent/proto';
import { FrostagentApiService } from '../core/frostagent-api.service';
import { AppIconComponent } from '../shared/app-icon.component';
import {
  ConfirmDialogService,
  type ConfirmDialogData,
} from '../shared/confirm-dialog.component';
import { maskSecret } from '../shared/dashboard-utils';

@Component({
  selector: 'app-backend-settings',
  imports: [
    FormsModule,
    HlmButton,
    HlmCardImports,
    HlmCheckbox,
    HlmField,
    HlmFieldLabel,
    HlmInput,
    HlmSpinner,
    HlmTableImports,
    HlmTabsImports,
    RouterModule,
    AppIconComponent,
  ],
  templateUrl: './backend-settings.component.html',
})
export class BackendSettingsComponent
  implements OnInit, AfterViewInit, OnDestroy
{
  private readonly api = inject(FrostagentApiService);
  private readonly confirmDialog = inject(ConfirmDialogService);
  readonly themeService = inject(ThemeService);

  private readonly editorDiv =
    viewChild.required<ElementRef<HTMLDivElement>>('editor');
  private editorView?: EditorView;

  private readonly themeCompartment = new Compartment();

  readonly envVars = signal<EnvVar[]>([]);
  readonly visibleSecrets = signal(new Set<string>());
  readonly loading = signal(false);
  readonly saving = signal(false);
  readonly error = signal('');
  readonly groupReplyOnMention = signal(false);
  readonly enableAtOther = signal(false);
  readonly enableReplyOther = signal(false);
  readonly rawContent = signal('');
  readonly editingKey = signal('');
  readonly editingValue = signal('');
  readonly editingIsSecret = signal(false);
  constructor() {
    effect(() => {
      const isDark = this.themeService.effectiveMode() === 'dark';

      if (this.editorView) {
        this.editorView.dispatch({
          effects: this.themeCompartment.reconfigure(isDark ? synthwave84 : []),
        });
      }
    });
  }

  ngOnInit(): void {
    void this.refresh();
  }

  ngAfterViewInit(): void {
    const isDark = this.themeService.effectiveMode() === 'dark';

    this.editorView = new EditorView({
      doc: this.rawContent(),
      extensions: [
        basicSetup,
        EditorView.lineWrapping,
        StreamLanguage.define(properties),
        this.themeCompartment.of(isDark ? synthwave84 : []),
        EditorView.theme({
          '&': { height: '100%' },
          '.cm-scroller': { overflow: 'auto' },
        }),
      ],
      parent: this.editorDiv().nativeElement,
    });
  }

  ngOnDestroy(): void {
    this.editorView?.destroy();
  }

  async refresh(): Promise<void> {
    this.loading.set(true);
    this.error.set('');
    try {
      const [envVars, rawContent] = await Promise.all([
        this.api.listEnvVars(),
        this.api.getRawEnvFile(),
      ]);
      this.envVars.set(envVars);
      this.rawContent.set(rawContent);
      const envValue = (key: string): string =>
        envVars.find((v) => v.key === key)?.value ?? '';
      this.groupReplyOnMention.set(envValue('GROUP_REPLY_ON_MENTION') !== 'false');
      this.enableAtOther.set(envValue('ENABLE_AT_IN_GROUP_MSG') === 'true');
      this.enableReplyOther.set(envValue('ENABLE_REPLY_IN_GROUP_MSG') === 'true');
      if (this.editorView) {
        this.editorView.dispatch({
          changes: {
            from: 0,
            to: this.editorView.state.doc.length,
            insert: rawContent,
          },
        });
      }
    } catch (error) {
      this.error.set(error instanceof Error ? error.message : String(error));
    } finally {
      this.loading.set(false);
    }
  }

  startEdit(envVar: EnvVar): void {
    this.editingKey.set(envVar.key);
    this.editingValue.set(envVar.value);
    this.editingIsSecret.set(envVar.isSecret);
  }

  cancelEdit(): void {
    this.editingKey.set('');
    this.editingValue.set('');
    this.editingIsSecret.set(false);
  }

  async saveEdit(): Promise<void> {
    await this.saveEnvVar(
      this.editingKey(),
      this.editingValue(),
      this.editingIsSecret(),
    );
    this.cancelEdit();
  }

  async deleteEnvVar(key: string): Promise<void> {
    const data: ConfirmDialogData = {
      title: '删除环境变量',
      message: `确认删除 ${key} 吗？`,
      cancelLabel: '取消',
      confirmLabel: '删除',
    };
    const confirmed = await this.confirmDialog.confirm(data);

    if (!confirmed) {
      return;
    }

    this.saving.set(true);
    try {
      const response = await this.api.deleteEnvVar(key);
      if (!response.success) {
        this.error.set(response.error);
        return;
      }
      toast.success('环境变量已删除', {
        duration: 2500,
      });
      await this.refresh();
    } finally {
      this.saving.set(false);
    }
  }

  async saveRawEnvFile(): Promise<void> {
    this.saving.set(true);
    this.error.set('');

    try {
      const content =
        this.editorView?.state.doc.toString() ?? this.rawContent();
      const response = await this.api.updateRawEnvFile(content);
      if (!response.success) {
        this.error.set(response.error);
        return;
      }
      toast.success('.env 文件已更新', {
        duration: 2500,
      });
      await this.refresh();
    } finally {
      this.saving.set(false);
    }
  }

  displayValue(envVar: EnvVar): string {
    if (!envVar.isSecret || this.visibleSecrets().has(envVar.key)) {
      return envVar.value || '';
    }
    return maskSecret(envVar.value);
  }

  toggleSecret(key: string): void {
    this.visibleSecrets.update((current) => {
      const next = new Set(current);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  }

  isSecretVisible(key: string): boolean {
    return this.visibleSecrets().has(key);
  }

  private async saveEnvVar(
    key: string,
    value: string,
    isSecret: boolean,
  ): Promise<void> {
    if (!key) {
      this.error.set('Key 不能为空');
      return;
    }

    this.saving.set(true);
    this.error.set('');

    try {
      const response = await this.api.updateEnvVar({
        key,
        value,
        isSecret,
      });
      if (!response.success) {
        this.error.set(response.error);
        return;
      }
      toast.success('环境变量已保存', {
        duration: 2500,
      });
      await this.refresh();
    } finally {
      this.saving.set(false);
    }
  }

  async onToggle(key: string, value: boolean): Promise<void> {
    this.saving.set(true);
    this.error.set('');
    try {
      const response = await this.api.updateEnvVar({
        key,
        value: value ? 'true' : 'false',
        isSecret: false,
      });
      if (!response.success) {
        this.error.set(response.error);
        return;
      }
      toast.success('群聊回复设置已更新', {
        duration: 2500,
      });
      await this.refresh();
    } finally {
      this.saving.set(false);
    }
  }
}
