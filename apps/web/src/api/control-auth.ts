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
    if (trimmed) localStorage.setItem(CONTROL_TOKEN_STORAGE_KEY, trimmed);
    else localStorage.removeItem(CONTROL_TOKEN_STORAGE_KEY);
  } catch {
    // localStorage may be unavailable in a restricted browser.
  }
}

export async function controlPlaneFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  const url = new URL(input instanceof Request ? input.url : String(input), window.location.origin);
  if (url.origin !== window.location.origin) throw new Error('管理请求必须使用同源地址');
  const headers = new Headers(init.headers ?? (input instanceof Request ? input.headers : undefined));
  const token = getControlToken();
  if (token && !headers.has('Authorization')) headers.set('Authorization', `Bearer ${token}`);
  const response = await fetch(input, { ...init, headers });
  if (response.status === 401) throw new Error('控制令牌无效或未设置，请在「前端设置」填写访问凭据');
  return response;
}

export async function downloadControlPlaneFile(url: string, filename: string): Promise<void> {
  const response = await controlPlaneFetch(url);
  if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
  saveDownloadedBlob(await response.blob(), filename);
}

export function saveDownloadedBlob(blob: Blob, filename: string): void {
  if (blob.size === 0) throw new Error('下载文件为空，请重试');
  const blobURL = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = blobURL;
  link.download = filename;
  try {
    document.body.appendChild(link);
    link.click();
  } catch (error) {
    URL.revokeObjectURL(blobURL);
    throw error;
  } finally {
    link.remove();
  }
  // Firefox may still be handing the blob to its download UI after click().
  setTimeout(() => URL.revokeObjectURL(blobURL), 60_000);
}
