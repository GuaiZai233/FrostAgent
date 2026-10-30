import { createInstanceAPI } from '../api/client';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { instanceState } from '../instance-state';
import { copyToClipboard } from '../utils/clipboard';
import { escapeHtml } from '../utils/formatters';
import { instanceWebSocketURL } from '../utils/websocket-url';
import { SettingsDraft } from './settings-draft';

const adapters = [
  { id: 'onebot', name: 'OneBot', prefix: 'ONEBOT', upstream: '在 NapCat 等上游启用正向 WebSocket 服务端，并填写它的连接地址。' },
  { id: 'astrbot', name: 'AstrBot', prefix: 'ASTRBOT', upstream: '需要更新配套 FrostAgent 插件，并在 AstrBot 插件中启用 WebSocket 服务端。' },
] as const;

const fields = ['MODE', 'URL', 'TOKEN'] as const;

export function mountConnectionsPage(container: HTMLElement, api = createInstanceAPI()): () => void {
  const instanceID = instanceState.selected?.id;
  const instanceName = instanceState.selected?.name || '';
  const selectionSignal = instanceState.signal;
  const controller = new AbortController();
  const signal = AbortSignal.any([controller.signal, selectionSignal]);
  let unmounted = false;
  let loading = false;
  let saving = false;
  let draft: SettingsDraft | null = null;
  const addresses = new Map<string, string>();

  if (!instanceID) {
    container.innerHTML = '<div class="page-container"><h1 class="page-title">请选择实例</h1></div>';
    return () => controller.abort();
  }

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-3 flex-wrap pb-1">
        <div>
          <h1 class="page-title">连接</h1>
          <p class="text-xs text-muted mt-1">实例：${escapeHtml(instanceName)} <span class="font-mono">(${escapeHtml(instanceID)})</span></p>
        </div>
        <div class="flex items-center gap-2">
          <button type="button" id="connections-retry" class="btn btn-outline btn-sm" hidden>重新加载</button>
          <button type="button" id="connections-restore" class="btn btn-outline btn-sm" disabled>撤销更改</button>
          <button type="button" id="connections-save" class="btn btn-primary btn-sm" disabled>${icon('save', 'w-3.5 h-3.5')}保存连接</button>
        </div>
      </header>
      <p id="connections-loading" class="text-xs text-muted" role="status">正在加载连接配置…</p>
      <p id="connections-error" class="text-xs text-destructive" role="alert" hidden></p>
      <div id="connections-draft" class="settings-draft-banner" role="status" hidden>当前更改尚未保存，离开将丢弃。保存后自动应用，并关闭当前实例的旧连接。</div>
      <div class="flex flex-col gap-4">
        ${adapters.map((adapter) => `
          <article class="card p-4 flex flex-col gap-3">
            <h2 class="text-sm font-semibold">${adapter.name}</h2>
            <label class="text-xs font-medium" for="${adapter.id}-mode">连接方式</label>
            <select id="${adapter.id}-mode" class="input w-full" disabled>
              <option value="reverse">反向 WebSocket（FA 作为服务端）</option>
              <option value="forward">正向 WebSocket（FA 作为客户端）</option>
            </select>
            <div id="${adapter.id}-reverse" class="flex flex-col gap-2">
              <p class="text-xs text-muted">由 ${adapter.name === 'OneBot' ? 'NapCat 等上游' : 'AstrBot 插件'}主动连接 FA。将下面的实例地址填入上游的反向 WebSocket 配置。</p>
              <div class="flex items-center gap-2 flex-wrap">
                <code id="${adapter.id}-address" class="text-xs font-mono break-all flex-1 min-w-0"></code>
                <button type="button" id="${adapter.id}-copy" class="btn btn-outline btn-sm" aria-label="复制 ${adapter.name} 地址" disabled>${icon('copy', 'w-3.5 h-3.5')}复制</button>
              </div>
              <p class="text-xs text-muted">地址根据实际 WS 监听地址生成；跨主机或 Docker 端口映射时，请按上游可达的地址调整。</p>
            </div>
            <div id="${adapter.id}-forward" class="flex flex-col gap-2" hidden>
              <p class="text-xs text-muted">${adapter.upstream} FA 将主动连接，断线后自动重连；实例停用时停止连接。</p>
              <label class="text-xs font-medium" for="${adapter.id}-url">上游 WebSocket 地址</label>
              <input id="${adapter.id}-url" class="input font-mono w-full" type="url" placeholder="ws://上游主机:端口/" autocomplete="off" spellcheck="false" disabled>
              <label class="text-xs font-medium" for="${adapter.id}-token">上游访问令牌（可选）</label>
              <input id="${adapter.id}-token" class="input font-mono w-full" type="password" autocomplete="off" spellcheck="false" disabled>
              <p class="text-xs text-muted">以 Authorization: Bearer 发送。令牌单独填写，不要放进地址；连接非受信网络请使用 wss://。</p>
            </div>
          </article>
        `).join('')}
        <aside class="card p-4 text-xs text-muted leading-relaxed">
          两种模式都能双向收发消息；同一适配器只启用一种真实上游连接方式，避免重复接收消息。切回反向模式会保留正向地址和令牌。<br>
          Docker 内的 127.0.0.1 指容器自己，请填写 FA 容器实际能访问的上游地址。这里仅配置适配器 WebSocket，不改变 HTTP 管理面板的端口或访问限制。
        </aside>
      </div>
    </div>
  `;

  const el = <T extends HTMLElement>(id: string) => container.querySelector<T>(`#${id}`)!;
  const saveButton = el<HTMLButtonElement>('connections-save');
  const restoreButton = el<HTMLButtonElement>('connections-restore');
  const retryButton = el<HTMLButtonElement>('connections-retry');
  const errorElement = el<HTMLElement>('connections-error');

  function active(): boolean {
    return !unmounted && !signal.aborted && instanceState.selected?.id === instanceID;
  }

  function updateState() {
    const locked = !active() || loading || saving || !draft;
    saveButton.disabled = locked || !draft?.dirty;
    restoreButton.disabled = locked || !draft?.dirty;
    retryButton.disabled = !active() || loading || saving;
    el<HTMLElement>('connections-draft').hidden = !draft?.dirty;
    for (const adapter of adapters) {
      const forward = draft?.get(`${adapter.prefix}_WS_MODE`) === 'forward';
      el<HTMLSelectElement>(`${adapter.id}-mode`).disabled = locked;
      el<HTMLElement>(`${adapter.id}-reverse`).hidden = forward;
      el<HTMLElement>(`${adapter.id}-forward`).hidden = !forward;
      el<HTMLInputElement>(`${adapter.id}-url`).disabled = locked || !forward;
      el<HTMLInputElement>(`${adapter.id}-token`).disabled = locked || !forward;
      el<HTMLButtonElement>(`${adapter.id}-copy`).disabled = locked || forward || !addresses.get(adapter.id);
    }
  }

  function populate() {
    for (const adapter of adapters) {
      el<HTMLSelectElement>(`${adapter.id}-mode`).value = draft?.get(`${adapter.prefix}_WS_MODE`) || 'reverse';
      el<HTMLInputElement>(`${adapter.id}-url`).value = draft?.get(`${adapter.prefix}_WS_URL`) || '';
      el<HTMLInputElement>(`${adapter.id}-token`).value = draft?.get(`${adapter.prefix}_WS_TOKEN`) || '';
      el<HTMLElement>(`${adapter.id}-address`).textContent = addresses.get(adapter.id) || '';
    }
    updateState();
  }

  async function load() {
    if (!active() || loading) return;
    loading = true;
    errorElement.hidden = true;
    retryButton.hidden = true;
    el<HTMLElement>('connections-loading').hidden = false;
    updateState();
    try {
      const [values, overview] = await Promise.all([api.listEnvVars(), api.getOverview()]);
      if (!active()) return;
      const settings = adapters.flatMap((adapter) => fields.map((field) => {
        const key = `${adapter.prefix}_WS_${field}`;
        const value = values.find((entry) => entry.key === key)?.value || '';
        return { key, value: field === 'MODE' ? value || 'reverse' : value, isSecret: field === 'TOKEN' };
      }));
      for (const adapter of adapters) {
        addresses.set(adapter.id, instanceWebSocketURL(overview.wsListenAddr, instanceID!, adapter.id, window.location.origin));
      }
      draft = new SettingsDraft(settings, []);
      populate();
    } catch {
      if (!active()) return;
      errorElement.textContent = '加载连接配置失败，请重试。';
      errorElement.hidden = false;
      retryButton.hidden = false;
    } finally {
      if (active()) {
        loading = false;
        el<HTMLElement>('connections-loading').hidden = true;
        updateState();
      }
    }
  }

  function validate(): boolean {
    for (const adapter of adapters) {
      const mode = draft!.get(`${adapter.prefix}_WS_MODE`);
      const address = draft!.get(`${adapter.prefix}_WS_URL`);
      const token = draft!.get(`${adapter.prefix}_WS_TOKEN`);
      if (mode !== 'reverse' && mode !== 'forward') {
        toast.error(`${adapter.name} 请选择正向或反向 WebSocket`);
        return false;
      }
      if (mode === 'forward' || address) {
        try {
          const url = new URL(address);
          const hasControlOrSpace = [...address].some((character) => character.charCodeAt(0) <= 32 || character.charCodeAt(0) === 127);
          if (!['ws:', 'wss:'].includes(url.protocol) || !url.hostname || url.username || url.password || url.hash || hasControlOrSpace) throw new Error();
        } catch {
          toast.error(`${adapter.name} 请填写有效的 ws:// 或 wss:// 上游地址，不含用户名、密码、片段或空白字符`);
          return false;
        }
      }
      if (/[\r\n]/.test(token)) {
        toast.error(`${adapter.name} 访问令牌不能包含换行`);
        return false;
      }
    }
    return true;
  }

  async function save() {
    if (!active() || loading || saving || !draft?.dirty || !validate()) return;
    const changes = draft.pending();
    saving = true;
    updateState();
    try {
      const result = await api.updateEnvVars(changes, signal);
      if (!active()) return;
      if (!result.success) {
        toast.error('连接配置保存失败，请检查配置后重试；更改已保留。');
        return;
      }
      draft.acknowledgeSettings(changes);
      populate();
      toast.success('连接配置已保存并应用；停用的实例将在启用后连接');
    } catch {
      if (active()) toast.error('连接配置保存失败，请重试；更改已保留。');
    } finally {
      if (active()) {
        saving = false;
        updateState();
      }
    }
  }

  for (const adapter of adapters) {
    for (const field of fields) {
      const input = el<HTMLInputElement | HTMLSelectElement>(`${adapter.id}-${field.toLowerCase()}`);
      input.addEventListener(field === 'MODE' ? 'change' : 'input', () => {
        if (!active() || loading || saving || !draft) return;
        draft.set(`${adapter.prefix}_WS_${field}`, input.value);
        updateState();
      });
    }
    el<HTMLButtonElement>(`${adapter.id}-copy`).addEventListener('click', async () => {
      if (!active() || loading || saving || !draft || draft.get(`${adapter.prefix}_WS_MODE`) === 'forward') return;
      const address = addresses.get(adapter.id);
      if (!address) return;
      const copied = await copyToClipboard(address);
      if (active()) {
        if (copied) toast.success(`${adapter.name} 地址已复制`);
        else toast.error('复制失败，请手动复制地址');
      }
    });
  }
  restoreButton.addEventListener('click', () => {
    if (!active() || loading || saving || !draft) return;
    draft.restore();
    populate();
  });
  saveButton.addEventListener('click', () => void save());
  retryButton.addEventListener('click', () => void load());
  const abort = () => updateState();
  signal.addEventListener('abort', abort, { once: true });
  void load();

  return () => {
    unmounted = true;
    controller.abort();
    signal.removeEventListener('abort', abort);
    for (const adapter of adapters) el<HTMLInputElement>(`${adapter.id}-token`).value = '';
    draft = null;
  };
}
