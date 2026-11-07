import { instanceState } from '../instance-state';
import { getControlToken } from './client';

export interface SkillInfo {
  name: string;
  description: string;
  enabled: boolean;
  manual_only: boolean;
  user_invocable: boolean;
  compatibility?: string;
  warnings: string[];
  files: string[];
  revision: string;
}

export function createSkillsAPI() {
  const instanceID = instanceState.selected?.id;
  const selectionSignal = instanceState.signal;
  async function request<T>(
    path: string,
    method = 'GET',
    body?: BodyInit,
    contentType?: string,
  ): Promise<T> {
    if (!instanceID) throw new Error('请先选择实例');
    selectionSignal.throwIfAborted();
    const headers = new Headers({ Accept: 'application/json' });
    const token = getControlToken();
    if (token) headers.set('Authorization', `Bearer ${token}`);
    if (contentType) headers.set('Content-Type', contentType);
    const response = await fetch(
      `/instances/${encodeURIComponent(instanceID)}/api/skills${path}`,
      {
        method,
        headers,
        body,
        signal: selectionSignal,
      },
    );
    if (!response.ok)
      throw new Error(
        (await response.text()).trim() || `HTTP ${response.status}`,
      );
    return (await response.json()) as T;
  }
  return {
    list: () => request<SkillInfo[]>(''),
    get: (name: string) =>
      request<{ info: SkillInfo; body: string; source: string }>(
        `/items/${encodeURIComponent(name)}`,
      ),
    importZIP: (file: File) => {
      if (file.size > 16 * 1024 * 1024) throw new Error('ZIP 最大为 16 MiB');
      return request<SkillInfo[]>('/import', 'POST', file, 'application/zip');
    },
    setEnabled: (name: string, enabled: boolean) =>
      request<{ success: boolean }>(
        `/items/${encodeURIComponent(name)}`,
        'POST',
        JSON.stringify({ enabled }),
        'application/json',
      ),
    delete: (name: string) =>
      request<{ success: boolean }>(
        `/items/${encodeURIComponent(name)}`,
        'DELETE',
      ),
  };
}
