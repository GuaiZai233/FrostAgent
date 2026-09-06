export interface InstanceInfo {
  id: string;
  name: string;
  created_at: string;
  enabled: boolean;
  error?: string;
  restart_required?: boolean;
}
let selected: InstanceInfo | null = null;
let controller = new AbortController();
let general = false;
export const instanceState = {
  get selected() {
    return selected;
  },
  get signal() {
    return controller.signal;
  },
  get showGeneral() {
    return general;
  },
  set showGeneral(value: boolean) {
    general = value;
  },
  select(value: InstanceInfo | null) {
    controller.abort();
    controller = new AbortController();
    selected = value;
    document
      .querySelectorAll<HTMLDialogElement>('dialog')
      .forEach((dialog) =>
        dialog.dispatchEvent(new Event('cancel', { cancelable: true })),
      );
    window.dispatchEvent(new Event('instance-selected'));
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
      else selected = next;
    }
    return result;
  },
};
export function instanceURL(path: string): string {
  return instanceState.selected
    ? '/instances/' + instanceState.selected.id + path
    : path;
}
export async function instanceRequest<T = { success: boolean }>(
  path: string,
  body?: unknown,
): Promise<T> {
  const response = await fetch(
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
