import {
  Component,
  OnInit,
  AfterViewInit,
  OnDestroy,
  inject,
  signal,
  viewChild,
  ElementRef,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTableModule } from '@angular/material/table';
import { MatToolbarModule } from '@angular/material/toolbar';
import { MatTooltipModule } from '@angular/material/tooltip';
import { MatTabsModule } from '@angular/material/tabs';
import { RouterModule } from '@angular/router';
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api.js';
import 'monaco-editor/esm/vs/basic-languages/ini/ini.contribution.js';
import type { EnvVar } from '@frostagent/proto';
import { FrostagentApiService } from '../core/frostagent-api.service';
import {
  ConfirmDialogComponent,
  type ConfirmDialogData,
} from '../shared/confirm-dialog.component';
import { maskSecret } from '../shared/dashboard-utils';

@Component({
  selector: 'app-backend-settings',
  imports: [
    FormsModule,
    MatButtonModule,
    MatTooltipModule,
    MatCardModule,
    MatCheckboxModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatProgressBarModule,
    MatTableModule,
    MatTabsModule,
    MatToolbarModule,
    RouterModule,
  ],
  templateUrl: './backend-settings.component.html',
})
export class BackendSettingsComponent
  implements OnInit, AfterViewInit, OnDestroy
{
  private readonly api = inject(FrostagentApiService);
  private readonly dialog = inject(MatDialog);
  private readonly snackBar = inject(MatSnackBar);

  private readonly editorDiv =
    viewChild.required<ElementRef<HTMLDivElement>>('editor');
  private monacoEditor?: monaco.editor.IStandaloneCodeEditor;

  readonly envVars = signal<EnvVar[]>([]);
  readonly visibleSecrets = signal(new Set<string>());
  readonly loading = signal(false);
  readonly saving = signal(false);
  readonly error = signal('');
  readonly rawContent = signal('');
  readonly editingKey = signal('');
  readonly editingValue = signal('');
  readonly editingIsSecret = signal(false);
  readonly displayedColumns = ['key', 'value', 'actions'];

  ngOnInit(): void {
    void this.refresh();
  }

  ngAfterViewInit(): void {
    this.monacoEditor = monaco.editor.create(this.editorDiv().nativeElement, {
      theme: 'vs',
      language: 'ini',
      automaticLayout: true,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      value: this.rawContent(),
    });
  }

  ngOnDestroy(): void {
    this.monacoEditor?.dispose();
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
      if (this.monacoEditor) {
        this.monacoEditor.setValue(rawContent);
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
      title: $localize`:@@deleteEnvTitle:删除环境变量`,
      message: $localize`:@@deleteEnvMessage:确认删除 ${key}:INTERPOLATION: 吗？`,
      cancelLabel: $localize`:@@cancel:取消`,
      confirmLabel: $localize`:@@delete:删除`,
    };
    const confirmed = await this.dialog
      .open(ConfirmDialogComponent, { data })
      .afterClosed()
      .toPromise();

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
      this.snackBar.open($localize`:@@envDeleted:环境变量已删除`, undefined, {
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
      const content = this.monacoEditor?.getValue() ?? this.rawContent();
      const response = await this.api.updateRawEnvFile(content);
      if (!response.success) {
        this.error.set(response.error);
        return;
      }
      this.snackBar.open($localize`:@@rawEnvSaved:.env 文件已更新`, undefined, {
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
      this.error.set($localize`:@@keyRequired:Key 不能为空`);
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
      this.snackBar.open($localize`:@@envSaved:环境变量已保存`, undefined, {
        duration: 2500,
      });
      await this.refresh();
    } finally {
      this.saving.set(false);
    }
  }
}
