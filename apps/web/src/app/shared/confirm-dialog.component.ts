import { Component, Injectable, inject } from '@angular/core';
import {
  BrnDialogRef,
  injectBrnDialogContext,
} from '@spartan-ng/brain/dialog';
import {
  HlmAlertDialogAction,
  HlmAlertDialogDescription,
  HlmAlertDialogFooter,
  HlmAlertDialogHeader,
  HlmAlertDialogTitle,
} from '@spartan-ng/helm/alert-dialog';
import { HlmButton } from '@spartan-ng/helm/button';
import { HlmDialogService } from '@spartan-ng/helm/dialog';
import { firstValueFrom } from 'rxjs';

export interface ConfirmDialogData {
  title: string;
  message: string;
  confirmLabel: string;
  cancelLabel: string;
}

@Component({
  selector: 'app-confirm-dialog',
  imports: [
    HlmAlertDialogAction,
    HlmAlertDialogDescription,
    HlmAlertDialogFooter,
    HlmAlertDialogHeader,
    HlmAlertDialogTitle,
    HlmButton,
  ],
  template: `
    <div hlmAlertDialogHeader>
      <h2 hlmAlertDialogTitle>{{ data.title }}</h2>
      <p hlmAlertDialogDescription>{{ data.message }}</p>
    </div>
    <div hlmAlertDialogFooter>
      <button hlmBtn variant="outline" (click)="close(false)">
        {{ data.cancelLabel }}
      </button>
      <button
        hlmAlertDialogAction
        variant="destructive"
        (click)="close(true)"
      >
        {{ data.confirmLabel }}
      </button>
    </div>
  `,
})
export class ConfirmDialogComponent {
  readonly data = injectBrnDialogContext<ConfirmDialogData>();
  private readonly dialogRef = inject(BrnDialogRef<boolean>);

  close(result: boolean): void {
    this.dialogRef.close(result);
  }
}

@Injectable({ providedIn: 'root' })
export class ConfirmDialogService {
  private readonly dialog = inject(HlmDialogService);

  async confirm(data: ConfirmDialogData): Promise<boolean> {
    const ref = this.dialog.open<boolean, ConfirmDialogData>(
      ConfirmDialogComponent,
      {
        context: data,
        role: 'alertdialog',
        showCloseButton: false,
        closeOnOutsidePointerEvents: false,
      },
    );
    return (await firstValueFrom(ref.closed$)) ?? false;
  }
}
