import { Component } from '@angular/core';
import { injectBrnDialogContext } from '@spartan-ng/brain/dialog';
import {
  HlmDialogDescription,
  HlmDialogHeader,
  HlmDialogTitle,
} from '@spartan-ng/helm/dialog';
import type { LogEntry } from '@frostagent/proto';

interface LogSummaryDialogData {
  entry: LogEntry;
}

@Component({
  selector: 'app-log-summary-dialog',
  imports: [HlmDialogDescription, HlmDialogHeader, HlmDialogTitle],
  template: `
    <div hlmDialogHeader>
      <h2 hlmDialogTitle i18n="@@summary">摘要</h2>
      <p hlmDialogDescription>
        {{ data.entry.source || '-' }}
      </p>
    </div>
    <div
      class="bg-muted/50 max-h-[65vh] overflow-auto rounded-lg border p-4"
    >
      <p class="break-words text-sm leading-relaxed whitespace-pre-wrap">
        {{ data.entry.summary || '-' }}
      </p>
    </div>
  `,
})
export class LogSummaryDialogComponent {
  readonly data = injectBrnDialogContext<LogSummaryDialogData>();
}
