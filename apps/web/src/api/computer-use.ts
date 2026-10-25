import { instanceState } from '../instance-state';
import { getControlToken } from './control-auth';

export interface ComputerConfig {
  enabled: boolean;
  bridge_url: string;
  token_configured: boolean;
  adapter_token_configured: boolean;
  ca_pem: string;
  max_concurrent: number;
  max_seconds: number;
  max_model_rounds: number;
  max_tool_calls: number;
  audit_retention_days: number;
}

export type ComputerConfigUpdate = Omit<
  ComputerConfig,
  'token_configured' | 'adapter_token_configured'
> & {
  token?: string;
  adapter_token?: string;
};

export interface ComputerProject {
  id: string;
  path: string;
  remote: string;
  baseline: string;
  shell: string;
  environment: Record<string, string>;
  archived: boolean;
}

export interface ComputerWorkspace {
  task_id: string;
  project_id: string;
  path: string;
  branch: string;
  shell: string;
  generation: number;
  paused: boolean;
}

export interface ComputerTask {
  id: string;
  project_id: string;
  state: string;
  reason: string;
  generation: number;
  model_rounds: number;
  tool_calls: number;
  gui_stopped: boolean;
  reconcile_required: boolean;
  created_at: string;
  updated_at: string;
}

export interface ComputerSnapshot {
  config: ComputerConfig;
  projects: ComputerProject[];
  workspaces: ComputerWorkspace[];
  tasks: ComputerTask[];
  disclosure: string;
  model: string;
  desktop_paused: boolean;
}

export interface ComputerContent {
  text?: string;
  png?: string;
  expires_at: string;
}

export class ComputerAPIError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ComputerAPIError';
  }
}

// Never retry a mutation here: losing an HTTP response is not evidence that
// the bridge did not execute it. Reconcile persisted task state instead.
export function createComputerUseAPI() {
  const instanceID = instanceState.selected?.id;
  const selectionSignal = instanceState.signal;
  const lifecycle = new AbortController();
  const abort = () => lifecycle.abort();
  selectionSignal.addEventListener('abort', abort, { once: true });
  if (selectionSignal.aborted) abort();

  async function request<T>(
    path = '',
    method = 'GET',
    body?: unknown,
  ): Promise<T> {
    if (!instanceID) throw new Error('请先选择实例');
    lifecycle.signal.throwIfAborted();
    const headers = new Headers({ Accept: 'application/json' });
    const token = getControlToken();
    if (token) headers.set('Authorization', 'Bearer ' + token);
    if (body !== undefined) headers.set('Content-Type', 'application/json');
    const response = await fetch(
      '/instances/' +
        encodeURIComponent(instanceID) +
        '/api/computer-use' +
        path,
      {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: lifecycle.signal,
        cache: 'no-store',
        credentials: 'same-origin',
        redirect: 'error',
      },
    );
    lifecycle.signal.throwIfAborted();
    if (response.status === 410)
      throw new ComputerAPIError(
        410,
        '临时内容已过期或不可用，不会重新执行操作。',
      );
    if (response.status === 401)
      throw new ComputerAPIError(401, '管理认证已失效，请重新验证访问凭据。');
    const result: unknown =
      response.status === 204 ? null : await response.json();
    lifecycle.signal.throwIfAborted();
    if (!response.ok) {
      const error =
        typeof result === 'object' &&
        result !== null &&
        'error' in result &&
        typeof result.error === 'string'
          ? result.error.slice(0, 512)
          : 'HTTP ' + response.status;
      throw new ComputerAPIError(response.status, error);
    }
    return result as T;
  }

  const taskPath = (id: string) => '/tasks/' + encodeURIComponent(id);
  return {
    snapshot: () => request<ComputerSnapshot>(),
    configure: (config: ComputerConfigUpdate) =>
      request<unknown>('/config', 'PUT', config),
    sync: () => request<unknown>('/sync', 'POST', {}),
    takeover: () =>
      request<{ gui_stopped: boolean; desktop_paused: boolean }>(
        '/takeover',
        'POST',
        {},
      ),
    resumeDesktop: () => request<unknown>('/desktop/resume', 'POST', {}),
    registerProject: (project: ComputerProject) =>
      request<unknown>('/projects', 'POST', project),
    archiveProject: (id: string, archived: boolean) =>
      request<unknown>(
        '/projects/' + encodeURIComponent(id) + '/archive',
        'POST',
        {
          archived,
        },
      ),
    start: (projectID: string, instruction: string) =>
      request<ComputerTask>('/tasks', 'POST', {
        project_id: projectID,
        instruction,
      }),
    pause: (id: string) =>
      request<ComputerTask>(taskPath(id) + '/pause', 'POST', {}),
    resume: (id: string, instruction: string, acknowledgeUnknown = false) =>
      request<ComputerTask>(taskPath(id) + '/resume', 'POST', {
        instruction,
        acknowledge_unknown: acknowledgeUnknown,
      }),
    reconcile: (id: string) =>
      request<ComputerTask>(taskPath(id) + '/reconcile', 'POST', {}),
    content: (id: string) =>
      request<ComputerContent>(taskPath(id) + '/content'),
    dispose: () => {
      abort();
      selectionSignal.removeEventListener('abort', abort);
    },
  };
}
