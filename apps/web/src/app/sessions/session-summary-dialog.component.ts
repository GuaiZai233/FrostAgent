import { Component } from '@angular/core';
import type { SessionInfo } from '@frostagent/proto';
import { injectBrnDialogContext } from '@spartan-ng/brain/dialog';
import {
  HlmDialogDescription,
  HlmDialogHeader,
  HlmDialogTitle,
} from '@spartan-ng/helm/dialog';

interface SessionSummaryDialogData {
  session: SessionInfo;
}

@Component({
  selector: 'app-session-summary-dialog',
  imports: [HlmDialogDescription, HlmDialogHeader, HlmDialogTitle],
  template: `
    <div hlmDialogHeader>
      <h2 hlmDialogTitle i18n="@@groupSummary">群聊总结</h2>
      <p hlmDialogDescription>{{ data.session.sessionId }}</p>
    </div>
    <div class="bg-muted/50 max-h-[65vh] overflow-auto rounded-lg border p-4">
      <p class="break-words text-sm leading-relaxed whitespace-pre-wrap">
        @if (data.session.groupSummary) {
          {{ data.session.groupSummary }}
        } @else {
          <span class="text-muted-foreground" i18n="@@noGroupSummary">
            暂无群聊总结。
          </span>
        }
      </p>
    </div>
  `,
})
export class SessionSummaryDialogComponent {
  readonly data = injectBrnDialogContext<SessionSummaryDialogData>();
}
