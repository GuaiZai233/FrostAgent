import { Injectable } from '@angular/core';
import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { BotStatusService, type GetOverviewResponse } from '@frostagent/proto';

export type DashboardState =
  | { status: 'loading' }
  | { status: 'ready'; overview: GetOverviewResponse }
  | { status: 'error'; message: string };

@Injectable({ providedIn: 'root' })
export class DashboardService {
  private readonly transport = createConnectTransport({
    baseUrl: globalThis.location?.origin ?? 'http://localhost:8080',
  });

  private readonly botStatus = createClient(BotStatusService, this.transport);

  async getOverview(): Promise<GetOverviewResponse> {
    return this.botStatus.getOverview({});
  }
}
