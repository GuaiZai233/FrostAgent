import { createClient } from '@connectrpc/connect';
import type { Client } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import {
  BotStatusService,
  DialogueService,
  LogLevel,
  LogService,
  MemoryService,
  SettingsService,
  type EnvVar,
  type GetOverviewResponse,
  type GetSessionsResponse,
  type DeleteGroupSummaryResponse,
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
  type DialogueItem,
  type ListDialoguesResponse,
  type SaveDialoguesResponse,
  type GetRawDialogueFileResponse,
  type UpdateRawDialogueFileResponse,
} from '@frostagent/proto';

export interface EnvVarUpdate {
  key: string;
  value: string;
  isSecret: boolean;
}

const transport = createConnectTransport({
  baseUrl: window.location.origin,
});

const botClient: Client<typeof BotStatusService> = createClient(
  BotStatusService,
  transport,
);

const logClient: Client<typeof LogService> = createClient(
  LogService,
  transport,
);

const settingsClient: Client<typeof SettingsService> = createClient(
  SettingsService,
  transport,
);

const memoryClient: Client<typeof MemoryService> = createClient(
  MemoryService,
  transport,
);

const dialogueClient: Client<typeof DialogueService> = createClient(
  DialogueService,
  transport,
);

export const api = {
  // Bot Overview & Sessions
  getOverview(): Promise<GetOverviewResponse> {
    return botClient.getOverview({});
  },

  getSessions(pageSize: number, pageToken = ''): Promise<GetSessionsResponse> {
    return botClient.getSessions({
      pagination: {
        pageSize,
        pageToken,
      },
    });
  },

  deleteGroupSummary(sessionId: string): Promise<DeleteGroupSummaryResponse> {
    return botClient.deleteGroupSummary({ sessionId });
  },

  // Logs
  listLogs(
    pageSize: number,
    pageToken: string,
    minLevel: LogLevel,
    sourceFilter: string,
  ): Promise<ListLogsResponse> {
    return logClient.listLogs({
      pagination: {
        pageSize,
        pageToken,
      },
      minLevel,
      sourceFilter,
    });
  },

  streamLogs(
    minLevel: LogLevel,
    sourceFilter: string,
    signal: AbortSignal,
  ): AsyncIterable<LogEntry> {
    return logClient.streamLogs(
      {
        minLevel,
        sourceFilter,
      },
      { signal },
    );
  },

  clearLogs(): Promise<boolean> {
    return logClient.clearLogs({}).then((res) => res.success);
  },

  // Settings & Env Vars
  listEnvVars(): Promise<EnvVar[]> {
    return settingsClient
      .listEnvVars({})
      .then((res) => [...res.envVars].sort((a, b) => a.key.localeCompare(b.key)));
  },

  updateEnvVar(envVar: EnvVarUpdate): Promise<{ success: boolean; error: string }> {
    return settingsClient.updateEnvVar({
      key: envVar.key,
      value: envVar.value,
      isSecret: envVar.isSecret,
    });
  },

  deleteEnvVar(key: string): Promise<{ success: boolean; error: string }> {
    return settingsClient.deleteEnvVar({ key });
  },

  getRawEnvFile(): Promise<string> {
    return settingsClient.getRawEnvFile({}).then((res) => res.content);
  },

  updateRawEnvFile(content: string): Promise<{ success: boolean; error: string }> {
    return settingsClient.updateRawEnvFile({ content });
  },

  // Memory
  listMemories(
    pageSize: number,
    pageToken = '',
    owner = '',
  ): Promise<ListMemoriesResponse> {
    return memoryClient.listMemories({
      pagination: { pageSize, pageToken },
      owner,
    });
  },

  deleteMemory(id: string): Promise<DeleteMemoryResponse> {
    return memoryClient.deleteMemory({ id });
  },

  getMemoryStats(): Promise<GetMemoryStatsResponse> {
    return memoryClient.getMemoryStats({});
  },

  searchMemories(
    query: string,
    pageSize: number,
    pageToken = '',
  ): Promise<SearchMemoriesResponse> {
    return memoryClient.searchMemories({
      query,
      pagination: { pageSize, pageToken },
    });
  },

  addMemory(
    owner: string,
    content: string,
    tags: string[],
    visibility: string,
  ): Promise<AddMemoryResponse> {
    return memoryClient.addMemory({ owner, content, tags, visibility });
  },

  updateMemory(
    id: string,
    content: string,
    tags: string[],
    visibility: string,
  ): Promise<UpdateMemoryResponse> {
    return memoryClient.updateMemory({
      id,
      content,
      tags,
      visibility,
    });
  },

  exportMemories(): Promise<ExportMemoriesResponse> {
    return memoryClient.exportMemories({});
  },

  importMemories(
    jsonContent: string,
    overwrite: boolean,
  ): Promise<ImportMemoriesResponse> {
    return memoryClient.importMemories({ jsonContent, overwrite });
  },

  triggerMemoryReflection(owner = ''): Promise<TriggerReflectionResponse> {
    return memoryClient.triggerReflection({ owner });
  },

  // Dialogue Examples
  listDialogues(): Promise<ListDialoguesResponse> {
    return dialogueClient.listDialogues({});
  },

  saveDialogues(dialogues: DialogueItem[]): Promise<SaveDialoguesResponse> {
    return dialogueClient.saveDialogues({ dialogues });
  },

  getRawDialogueFile(): Promise<GetRawDialogueFileResponse> {
    return dialogueClient.getRawDialogueFile({});
  },

  updateRawDialogueFile(content: string): Promise<UpdateRawDialogueFileResponse> {
    return dialogueClient.updateRawDialogueFile({ content });
  },
};
