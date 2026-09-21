import { instanceState } from '../instance-state';
import { createClient } from '@connectrpc/connect';
import type { Client, Interceptor } from '@connectrpc/connect';
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
  MCPService,
  type MCPServerInfo,
  type MCPToolInfo,
  type ListMCPServersResponse,
  type GetMCPServerResponse,
  type AddMCPServerResponse,
  type UpdateMCPServerResponse,
  type DeleteMCPServerResponse,
  type ToggleMCPServerResponse,
  type ToggleMCPToolResponse,
  type SyncMCPServerResponse,
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
  type MarkStickerInappropriateFlagResponse,
  type ClearStickerInappropriateFlagResponse,
  type UploadStickerResponse,
  type RetryAllUnsummarizedResponse,
  type GetStickerStatsResponse,
} from '@frostagent/proto';

export interface EnvVarUpdate {
  key: string;
  value: string;
  isSecret: boolean;
}

export type { MCPServerInfo, MCPToolInfo };

const CONTROL_TOKEN_STORAGE_KEY = 'frostagent_control_token';

export function getControlToken(): string {
  try {
    return localStorage.getItem(CONTROL_TOKEN_STORAGE_KEY) || '';
  } catch {
    return '';
  }
}

export function setControlToken(token: string): void {
  try {
    const trimmed = token.trim();
    if (trimmed) {
      localStorage.setItem(CONTROL_TOKEN_STORAGE_KEY, trimmed);
    } else {
      localStorage.removeItem(CONTROL_TOKEN_STORAGE_KEY);
    }
  } catch {
    // localStorage errors ignored
  }
}

export interface LockedPrincipal {
  principal: { platform: string; user_id: string };
  reason?: string;
  locked_at?: string;
}

// Security controls belong to the control plane, never to a selected instance.
async function securityRequest<T>(path: string, body?: unknown): Promise<T> {
  const headers = new Headers({ Accept: 'application/json' });
  const token = getControlToken();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  if (body !== undefined) headers.set('Content-Type', 'application/json');
  const response = await fetch(`/api/security/${path}`, {
    method: body === undefined ? 'GET' : 'POST',
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!response.ok) {
    const message = (await response.text()).trim();
    throw new Error(message || response.statusText || `HTTP ${response.status}`);
  }
  return (await response.json()) as T;
}

export const securityAPI = {
  listLockedPrincipals(): Promise<{ locked: LockedPrincipal[] | null }> {
    return securityRequest('locked');
  },
  unlockPrincipal(
    platform: string,
    userID: string,
  ): Promise<{ unlocked: LockedPrincipal['principal'] }> {
    return securityRequest('unlock', { platform, user_id: userID });
  },
};

export interface ActionsCatStatus {
  configured: boolean;
  healthy: boolean;
  endpoint: string;
  error?: string;
}

export interface ActionsCatAction {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  schedule?: string;
  timeout_sec?: number;
  capabilities?: string[];
  created_at?: string;
  updated_at?: string;
}

export interface ActionsCatRun {
  id: string;
  action_id: string;
  status: string;
  trigger_type: string;
  exit_code?: number;
  duration_ms: number;
  stdout?: string;
  stderr?: string;
  created_at: string;
  completed_at?: string;
}

export interface ActionsCatRunLogs {
  stdout: string;
  stderr: string;
}

export interface TriggerActionRunRequest {
  extra_env?: Record<string, string>;
  trigger_metadata?: Record<string, unknown>;
}

async function actionsCatRequest<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const instanceID = instanceState.selected?.id;
  const selectionSignal = instanceState.signal;
  selectionSignal.throwIfAborted();
  if (!instanceID) {
    throw new Error('请先选择实例');
  }

  const token = getControlToken();
  const headers = new Headers(options.headers);
  if (token && !headers.has('Authorization')) {
    headers.set('Authorization', `Bearer ${token}`);
  }
  if (!headers.has('Accept')) {
    headers.set('Accept', 'application/json');
  }

  const cleanPath = path.startsWith('/') ? path : `/${path}`;
  const url = `/instances/${instanceID}/api/actionscat${cleanPath}`;
  const signal = options.signal
    ? AbortSignal.any([options.signal, selectionSignal])
    : selectionSignal;

  const response = await fetch(url, { ...options, headers, signal });
  if (!response.ok) {
    let errMsg = '';
    try {
      const errJson = await response.json();
      errMsg = errJson.error || errJson.message || '';
    } catch {
      errMsg = (await response.text()).trim();
    }
    throw new Error(errMsg || response.statusText || `HTTP ${response.status}`);
  }
  return (await response.json()) as T;
}

export const actionsCatAPI = {
  getStatus(): Promise<ActionsCatStatus> {
    return actionsCatRequest<ActionsCatStatus>('/status');
  },
  listActions(): Promise<ActionsCatAction[]> {
    return actionsCatRequest<ActionsCatAction[]>('/actions');
  },
  getAction(actionID: string): Promise<ActionsCatAction> {
    return actionsCatRequest<ActionsCatAction>(
      `/actions/${encodeURIComponent(actionID)}`,
    );
  },
  triggerRun(
    actionID: string,
    req?: TriggerActionRunRequest,
  ): Promise<ActionsCatRun> {
    return actionsCatRequest<ActionsCatRun>(
      `/actions/${encodeURIComponent(actionID)}/runs`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req || {}),
      },
    );
  },
  listRuns(
    actionID: string,
    limit = 50,
    offset = 0,
  ): Promise<ActionsCatRun[]> {
    return actionsCatRequest<ActionsCatRun[]>(
      `/actions/${encodeURIComponent(actionID)}/runs?limit=${limit}&offset=${offset}`,
    );
  },
  getRun(actionID: string, runID: string): Promise<ActionsCatRun> {
    return actionsCatRequest<ActionsCatRun>(
      `/actions/${encodeURIComponent(actionID)}/runs/${encodeURIComponent(runID)}`,
    );
  },
  getRunLogs(actionID: string, runID: string): Promise<ActionsCatRunLogs> {
    return actionsCatRequest<ActionsCatRunLogs>(
      `/actions/${encodeURIComponent(actionID)}/runs/${encodeURIComponent(runID)}/logs`,
    );
  },
  dispatch(payload: Record<string, unknown>): Promise<{ ok: boolean }> {
    return actionsCatRequest<{ ok: boolean }>('/dispatch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
  },
};

const authInterceptor: Interceptor = (next) => async (req) => {
  const token = getControlToken();
  if (token && !req.header.has('Authorization')) {
    req.header.set('Authorization', `Bearer ${token}`);
  }
  return await next(req);
};

export function createInstanceAPI() {
  const instanceID = instanceState.selected?.id;
  const selectionSignal = instanceState.signal;
  const transport = createConnectTransport({
    baseUrl: window.location.origin,
    fetch: async (input, init) => {
      const original = new Request(input, init);
      const url = new URL(original.url);
      selectionSignal.throwIfAborted();
      const id = instanceID;
      const isLogService = url.pathname.startsWith(
        '/frostagent.v1.LogService/',
      );
      const isControlPlaneLog =
        isLogService && instanceState.logSource === 'control-plane';
      if (id && !isControlPlaneLog)
        url.pathname = `/instances/${id}${url.pathname}`;
      else if (!id && !isControlPlaneLog) throw new Error('请先选择实例');
      const signal = AbortSignal.any([original.signal, selectionSignal]);
      const headers = new Headers(original.headers);
      headers.set('X-FrostAgent-Log-Source', instanceState.logSource);
      const body =
        original.method === 'GET' || original.method === 'HEAD'
          ? undefined
          : await original.arrayBuffer();
      return fetch(url, { method: original.method, headers, signal, body });
    },
    interceptors: [authInterceptor],
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

  const mcpClient: Client<typeof MCPService> = createClient(
    MCPService,
    transport,
  );

  return {
    // Bot Overview & Sessions
    getOverview(): Promise<GetOverviewResponse> {
      return botClient.getOverview({});
    },

    getSessions(
      pageSize: number,
      pageToken = '',
    ): Promise<GetSessionsResponse> {
      return botClient.getSessions({
        pagination: {
          pageSize,
          pageToken,
        },
      });
    },

    getSessionContext(
      sessionId: string,
      recentLimit = 50,
    ): Promise<GetSessionContextResponse> {
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
        .then((res) =>
          [...res.envVars].sort((a, b) => a.key.localeCompare(b.key)),
        );
    },

    updateEnvVar(
      envVar: EnvVarUpdate,
    ): Promise<{ success: boolean; error: string }> {
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

    updateRawEnvFile(
      content: string,
    ): Promise<{ success: boolean; error: string }> {
      return settingsClient.updateRawEnvFile({ content });
    },

    // Model Router
    getModelRouterState(): Promise<GetStateResponse> {
      return modelRouterClient.getState({});
    },

    saveModelRouterDraft(
      configuration: ModelRouterConfiguration,
    ): Promise<SaveDraftResponse> {
      return modelRouterClient.saveDraft({ configuration });
    },

    setDraftEndpointSecret(
      endpointId: string,
      apiKey: string,
    ): Promise<{ success: boolean; error: string; configured: boolean }> {
      return modelRouterClient.setDraftEndpointSecret({ endpointId, apiKey });
    },

    clearDraftEndpointSecret(
      endpointId: string,
    ): Promise<{ success: boolean; error: string; configured: boolean }> {
      return modelRouterClient.clearDraftEndpointSecret({ endpointId });
    },

    discardModelRouterDraft(): Promise<ModelRouterConfiguration | undefined> {
      return modelRouterClient.discardDraft({}).then((res) => res.draft);
    },

    publishModelRouter(): Promise<PublishResponse> {
      return modelRouterClient.publish({});
    },

    listUpstreamModels(
      endpointId: string,
    ): Promise<{ models: string[]; error: string }> {
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

    updateRawDialogueFile(
      content: string,
    ): Promise<UpdateRawDialogueFileResponse> {
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

    markStickerInappropriateFlag(
      id: string,
    ): Promise<MarkStickerInappropriateFlagResponse> {
      return stickerClient.markStickerInappropriateFlag({ id });
    },

    clearStickerInappropriateFlag(
      id: string,
    ): Promise<ClearStickerInappropriateFlagResponse> {
      return stickerClient.clearStickerInappropriateFlag({ id });
    },

    uploadSticker(
      fileContent: Uint8Array,
      filename: string,
    ): Promise<UploadStickerResponse> {
      return stickerClient.uploadSticker({ fileContent, filename });
    },

    retryAllUnsummarized(): Promise<RetryAllUnsummarizedResponse> {
      return stickerClient.retryAllUnsummarized({});
    },

    getStickerStats(): Promise<GetStickerStatsResponse> {
      return stickerClient.getStickerStats({});
    },

    // MCP
    listMCPServers(): Promise<ListMCPServersResponse> {
      return mcpClient.listMCPServers({});
    },
    getMCPServer(id: string): Promise<GetMCPServerResponse> {
      return mcpClient.getMCPServer({ id });
    },
    addMCPServer(params: MCPServerParams): Promise<AddMCPServerResponse> {
      return mcpClient.addMCPServer(params);
    },
    updateMCPServer(params: MCPServerParams): Promise<UpdateMCPServerResponse> {
      return mcpClient.updateMCPServer(params);
    },
    deleteMCPServer(id: string): Promise<DeleteMCPServerResponse> {
      return mcpClient.deleteMCPServer({ id });
    },
    toggleMCPServer(
      id: string,
      enabled: boolean,
    ): Promise<ToggleMCPServerResponse> {
      return mcpClient.toggleMCPServer({ id, enabled });
    },
    toggleMCPTool(
      serverId: string,
      toolName: string,
      enabled: boolean,
    ): Promise<ToggleMCPToolResponse> {
      return mcpClient.toggleMCPTool({ serverId, toolName, enabled });
    },
    syncMCPServer(id: string): Promise<SyncMCPServerResponse> {
      return mcpClient.syncMCPServer({ id });
    },

    // ActionsCat
    getActionsCatStatus(): Promise<ActionsCatStatus> {
      return actionsCatAPI.getStatus();
    },
    listActionsCatActions(): Promise<ActionsCatAction[]> {
      return actionsCatAPI.listActions();
    },
    getActionCatAction(actionID: string): Promise<ActionsCatAction> {
      return actionsCatAPI.getAction(actionID);
    },
    triggerActionsCatRun(
      actionID: string,
      req?: TriggerActionRunRequest,
    ): Promise<ActionsCatRun> {
      return actionsCatAPI.triggerRun(actionID, req);
    },
    listActionsCatRuns(
      actionID: string,
      limit = 50,
      offset = 0,
    ): Promise<ActionsCatRun[]> {
      return actionsCatAPI.listRuns(actionID, limit, offset);
    },
    getActionsCatRun(actionID: string, runID: string): Promise<ActionsCatRun> {
      return actionsCatAPI.getRun(actionID, runID);
    },
    getActionsCatRunLogs(
      actionID: string,
      runID: string,
    ): Promise<ActionsCatRunLogs> {
      return actionsCatAPI.getRunLogs(actionID, runID);
    },
    dispatchActionsCat(
      payload: Record<string, unknown>,
    ): Promise<{ ok: boolean }> {
      return actionsCatAPI.dispatch(payload);
    },
  };
}

interface MCPServerParams {
  id: string;
  name: string;
  enabled: boolean;
  transportType: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  workingDir?: string;
  url?: string;
  headers?: Record<string, string>;
}
