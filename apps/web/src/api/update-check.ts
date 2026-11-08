import { controlPlaneFetch } from './control-auth';

export interface UpdateChannel {
  value?: string;
  url?: string;
  status: 'up-to-date' | 'different' | 'unknown' | 'unavailable' | 'no-release';
  error?: string;
}

export interface BuildVersion {
  display: string;
  version: string;
  commit: string;
}

export interface UpdateCheckResult {
  current: BuildVersion;
  stable: UpdateChannel;
  latest: UpdateChannel;
  checked_at: string;
}

export async function fetchBuildVersion(signal?: AbortSignal): Promise<BuildVersion> {
  const response = await controlPlaneFetch('/api/version', { signal });
  if (!response.ok) throw new Error(`读取版本失败：HTTP ${response.status}`);
  return response.json();
}

export async function fetchUpdateCheck(signal?: AbortSignal): Promise<UpdateCheckResult> {
  const response = await controlPlaneFetch('/api/update-check', { signal });
  if (!response.ok) throw new Error(`检查更新失败：HTTP ${response.status}`);
  return response.json();
}
