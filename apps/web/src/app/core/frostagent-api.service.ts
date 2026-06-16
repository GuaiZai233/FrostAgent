import { Injectable } from '@angular/core';
import { createClient } from '@connectrpc/connect';
import type { Client } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import {
  BotStatusService,
  LogLevel,
  LogService,
  SettingsService,
  type EnvVar,
  type GetOverviewResponse,
  type GetSessionsResponse,
  type ListLogsResponse,
  type LogEntry,
} from '@frostagent/proto';

export interface EnvVarUpdate {
  key: string;
  value: string;
  isSecret: boolean;
}

@Injectable({ providedIn: 'root' })
export class FrostagentApiService {
  private readonly transport = createConnectTransport({
    baseUrl: window.location.origin,
  });

  private readonly botClient: Client<typeof BotStatusService> = createClient(
    BotStatusService,
    this.transport,
  );
  private readonly logClient: Client<typeof LogService> = createClient(
    LogService,
    this.transport,
  );
  private readonly settingsClient: Client<typeof SettingsService> =
    createClient(SettingsService, this.transport);

  getOverview(): Promise<GetOverviewResponse> {
    return this.botClient.getOverview({});
  }

  getSessions(pageSize: number, pageToken = ''): Promise<GetSessionsResponse> {
    return this.botClient.getSessions({
      pagination: {
        pageSize,
        pageToken,
      },
    });
  }

  listLogs(
    pageSize: number,
    pageToken: string,
    minLevel: LogLevel,
    sourceFilter: string,
  ): Promise<ListLogsResponse> {
    return this.logClient.listLogs({
      pagination: {
        pageSize,
        pageToken,
      },
      minLevel,
      sourceFilter,
    });
  }

  streamLogs(
    minLevel: LogLevel,
    sourceFilter: string,
    signal: AbortSignal,
  ): AsyncIterable<LogEntry> {
    return this.logClient.streamLogs(
      {
        minLevel,
        sourceFilter,
      },
      { signal },
    );
  }

  clearLogs(): Promise<boolean> {
    return this.logClient.clearLogs({}).then((response) => response.success);
  }

  listEnvVars(): Promise<EnvVar[]> {
    return this.settingsClient
      .listEnvVars({})
      .then((response) =>
        [...response.envVars].sort((a, b) => a.key.localeCompare(b.key)),
      );
  }

  updateEnvVar(
    envVar: EnvVarUpdate,
  ): Promise<{ success: boolean; error: string }> {
    return this.settingsClient.updateEnvVar({
      key: envVar.key,
      value: envVar.value,
      isSecret: envVar.isSecret,
    });
  }

  deleteEnvVar(key: string): Promise<{ success: boolean; error: string }> {
    return this.settingsClient.deleteEnvVar({ key });
  }

  getRawEnvFile(): Promise<string> {
    return this.settingsClient
      .getRawEnvFile({})
      .then((response) => response.content);
  }

  updateRawEnvFile(
    content: string,
  ): Promise<{ success: boolean; error: string }> {
    return this.settingsClient.updateRawEnvFile({ content });
  }
}
