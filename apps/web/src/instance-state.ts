import { controlPlaneFetch } from './api/control-auth';

export interface InstanceInfo {
  id: string;
  name: string;
  created_at: string;
  enabled: boolean;
  deleting?: boolean;
  error?: string;
}

const SELECTED_INSTANCE_KEY = 'frostagent_selected_instance';
let selected: InstanceInfo | null = null;
let controller = new AbortController();
let logSource: 'instance' | 'control-plane' = 'instance';

function rememberSelection(id: string | null): void {
  try {
    if (id) window.sessionStorage.setItem(SELECTED_INSTANCE_KEY, id);
    else window.sessionStorage.removeItem(SELECTED_INSTANCE_KEY);
  } catch {
    // Selection still works in memory if browser storage is unavailable.
  }
}

export const instanceState = {
  get selected() {
    return selected;
  },
  get signal() {
    return controller.signal;
  },
  get logSource() {
    return logSource;
  },
  set logSource(value: 'instance' | 'control-plane') {
    logSource = value;
  },
  select(value: InstanceInfo | null) {
    controller.abort();
    controller = new AbortController();
    selected = value;
    rememberSelection(value?.id ?? null);
    document
      .querySelectorAll<HTMLDialogElement>('dialog')
      .forEach((dialog) =>
        dialog.dispatchEvent(new Event('cancel', { cancelable: true })),
      );
    window.dispatchEvent(new Event('instance-selected'));
  },
  async restoreSelection() {
    if (selected) return;

    let id: string | null;
    try {
      id = window.sessionStorage.getItem(SELECTED_INSTANCE_KEY);
    } catch {
      return;
    }
    if (!id) return;

    const selectionSignal = controller.signal;
    const initialHash = window.location.hash;
    const interaction = new AbortController();
    const cancel = () => interaction.abort();
    const options = { capture: true, signal: interaction.signal };
    for (const type of ['pointerdown', 'click', 'keydown', 'input', 'change']) {
      document.addEventListener(type, cancel, options);
    }
    window.addEventListener(
      'hashchange',
      () => {
        if (window.location.hash !== initialHash) cancel();
      },
      options,
    );

    try {
      const { instances } = await this.refresh();
      // Selection remounts the page and closes dialogs, so give any interaction priority.
      if (
        selectionSignal.aborted ||
        interaction.signal.aborted ||
        window.location.hash !== initialHash
      ) {
        return;
      }

      const instance = instances.find((item) => item.id === id && !item.deleting);
      if (instance) this.select(instance);
      else rememberSelection(null);
    } finally {
      cancel();
    }
  },
  async refresh() {
    const selectedAtStart = selected?.id;
    const result = await instanceRequest<{
      instances: InstanceInfo[];
      next_name: string;
    }>('');
    if (selected && selected.id === selectedAtStart) {
      const next = result.instances.find((item) => item.id === selected?.id);
      if (!next) this.select(null);
      else {
        const labelChanged = selected.name !== next.name || selected.enabled !== next.enabled;
        selected = next;
        if (labelChanged) window.dispatchEvent(new Event('instance-updated'));
      }
    }
    return result;
  },
};
export function instanceURL(path: string): string {
  return instanceState.selected
    ? '/instances/' + instanceState.selected.id + path
    : path;
}
export async function restoreInstanceZIP(file: Blob): Promise<InstanceInfo> {
  const response = await controlPlaneFetch('/api/instances/restore', {
    method: 'POST',
    body: file,
  });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || response.statusText);
  return result as InstanceInfo;
}
export async function instanceRequest<T = { success: boolean }>(
  path: string,
  body?: unknown,
): Promise<T> {
  const response = await controlPlaneFetch(
    '/api/instances' + path,
    body === undefined
      ? undefined
      : {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body),
        },
  );
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || response.statusText);
  return result as T;
}
