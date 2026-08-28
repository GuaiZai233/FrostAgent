import { createClient } from '@connectrpc/connect';
import type { Client } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import {
  BotStatusService,
  DialogueService,
  LogLevel,
  LogService,
  MemoryService,
  ModelRouterService,
  SettingsService,
  StickerService,
  type EnvVar,
  type GetOverviewResponse,
  type GetSessionsResponse,
  type GetSessionContextResponse,
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
  type ModelRouterConfiguration,
  type GetStateResponse,
  type SaveDraftResponse,
  type PublishResponse,
  type TestModelResponse,
  type DialogueItem,
  type ListDialoguesResponse,
  type SaveDialoguesResponse,
  type GetRawDialogueFileResponse,
  type UpdateRawDialogueFileResponse,
  type ListStickersResponse,
  type DeleteStickerResponse,
  type UpdateStickerKeywordsResponse,
  type UploadStickerResponse,
  type RetryAllUnsummarizedResponse,
  type GetStickerStatsResponse,
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

const modelRouterClient: Client<typeof ModelRouterService> = createClient(
  ModelRouterService,
  transport,
);

const dialogueClient: Client<typeof DialogueService> = createClient(
  DialogueService,
  transport,
);

const stickerClient: Client<typeof StickerService> = createClient(
  StickerService,
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

  getSessionContext(sessionId: string, recentLimit = 50): Promise<GetSessionContextResponse> {
    return botClient.getSessionContext({
      sessionId,
      recentLimit,
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

  // Model Router
  getModelRouterState(): Promise<GetStateResponse> {
    return modelRouterClient.getState({});
  },

  saveModelRouterDraft(configuration: ModelRouterConfiguration): Promise<SaveDraftResponse> {
    return modelRouterClient.saveDraft({ configuration });
  },

  setDraftEndpointSecret(endpointId: string, apiKey: string): Promise<{ success: boolean; error: string; configured: boolean }> {
    return modelRouterClient.setDraftEndpointSecret({ endpointId, apiKey });
  },

  clearDraftEndpointSecret(endpointId: string): Promise<{ success: boolean; error: string; configured: boolean }> {
    return modelRouterClient.clearDraftEndpointSecret({ endpointId });
  },

  discardModelRouterDraft(): Promise<ModelRouterConfiguration | undefined> {
    return modelRouterClient.discardDraft({}).then((res) => res.draft);
  },

  publishModelRouter(): Promise<PublishResponse> {
    return modelRouterClient.publish({});
  },

  listUpstreamModels(endpointId: string): Promise<{ models: string[]; error: string }> {
    return modelRouterClient.listUpstreamModels({ endpointId });
  },

  testModel(modelId: string): Promise<TestModelResponse> {
    return modelRouterClient.testModel({ modelId });
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

  // Sticker
  listStickers(
    pageSize: number,
    pageToken = '',
    statusFilter = '',
    search = '',
  ): Promise<ListStickersResponse> {
    return stickerClient.listStickers({
      pagination: { pageSize, pageToken },
      statusFilter,
      search,
    });
  },

  deleteSticker(id: string): Promise<DeleteStickerResponse> {
    return stickerClient.deleteSticker({ id });
  },

  updateStickerKeywords(
    id: string,
    description: string,
    keywords: string[],
  ): Promise<UpdateStickerKeywordsResponse> {
    return stickerClient.updateStickerKeywords({ id, description, keywords });
  },

  uploadSticker(fileContent: Uint8Array, filename: string): Promise<UploadStickerResponse> {
    return stickerClient.uploadSticker({ fileContent, filename });
  },

  retryAllUnsummarized(): Promise<RetryAllUnsummarizedResponse> {
    return stickerClient.retryAllUnsummarized({});
  },

  getStickerStats(): Promise<GetStickerStatsResponse> {
    return stickerClient.getStickerStats({});
  },
};
