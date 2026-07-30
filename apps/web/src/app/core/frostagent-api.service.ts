import { Injectable } from '@angular/core';
import { createClient } from '@connectrpc/connect';
import type { Client } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import {
  BotStatusService,
  LogLevel,
  LogService,
  MemoryService,
  SettingsService,
  type EnvVar,
  type GetOverviewResponse,
  type GetSessionsResponse,
  type ListLogsResponse,
  type LogEntry,
  type ListMemoriesResponse,
  type DeleteMemoryResponse,
  type GetMemoryStatsResponse,
  type SearchMemoriesResponse,
  type AddMemoryResponse,
  type UpdateMemoryResponse,
  type ExportMemoriesResponse,
  type ImportMemoriesResponse,
  type TriggerReflectionResponse,
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
  private readonly memoryClient: Client<typeof MemoryService> = createClient(
    MemoryService,
    this.transport,
  );

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

  listMemories(
    pageSize: number,
    pageToken = '',
    owner = '',
  ): Promise<ListMemoriesResponse> {
    return this.memoryClient.listMemories({
      pagination: { pageSize, pageToken },
      owner,
    });
  }

  deleteMemory(id: string): Promise<DeleteMemoryResponse> {
    return this.memoryClient.deleteMemory({ id });
  }

  getMemoryStats(): Promise<GetMemoryStatsResponse> {
    return this.memoryClient.getMemoryStats({});
  }

  searchMemories(
    query: string,
    pageSize: number,
    pageToken = '',
  ): Promise<SearchMemoriesResponse> {
    return this.memoryClient.searchMemories({
      query,
      pagination: { pageSize, pageToken },
    });
  }

  addMemory(
    owner: string,
    content: string,
    tags: string[],
    visibility: string,
  ): Promise<AddMemoryResponse> {
    return this.memoryClient.addMemory({ owner, content, tags, visibility });
  }

  updateMemory(
    id: string,
    content: string,
    tags: string[],
    visibility: string,
    importance: number,
  ): Promise<UpdateMemoryResponse> {
    return this.memoryClient.updateMemory({
      id,
      content,
      tags,
      visibility,
      importance,
    });
  }

  exportMemories(): Promise<ExportMemoriesResponse> {
    return this.memoryClient.exportMemories({});
  }

  importMemories(
    jsonContent: string,
    overwrite: boolean,
  ): Promise<ImportMemoriesResponse> {
    return this.memoryClient.importMemories({ jsonContent, overwrite });
  }

  triggerMemoryReflection(owner = ''): Promise<TriggerReflectionResponse> {
    return this.memoryClient.triggerReflection({ owner });
  }
}
