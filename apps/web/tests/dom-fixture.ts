import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { setImmediate } from 'node:timers/promises';

const { JSDOM } = createRequire(resolve('package.json'))('jsdom');

// Keep DOM parsing, selectors, events and form validation in jsdom. Only dialog
// presentation and Node/jsdom AbortSignal interoperability need fixture support.
export function createTestDOM(url = 'http://localhost/') {
  const dom = new JSDOM('<!doctype html><html><body></body></html>', { url });
  const listenerErrors: unknown[] = [];
  dom.window.addEventListener('error', (event: ErrorEvent) => {
    listenerErrors.push(event.error ?? new Error(event.message));
    event.preventDefault();
  });
  const keys = [
    'window',
    'document',
    'navigator',
    'localStorage',
    'Event',
    'CustomEvent',
    'EventTarget',
    'MouseEvent',
    'KeyboardEvent',
    'HTMLElement',
    'HTMLInputElement',
    'HTMLSelectElement',
    'HTMLDialogElement',
    'fetch',
  ] as const;
  const previous = new Map(
    keys.map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]),
  );
  for (const key of keys) {
    if (key !== 'fetch')
      Object.defineProperty(globalThis, key, {
        configurable: true,
        writable: true,
        value: key === 'window' ? dom.window : dom.window[key],
      });
  }
  const dialogs: HTMLDialogElement[] = [];
  // Keep Node's AbortController for fetch/Request, but translate its signals at
  // the DOM boundary: jsdom only accepts AbortSignal instances from its realm.
  const signals = new WeakMap<AbortSignal, AbortSignal>();
  const detachSignals: (() => void)[] = [];
  const addEventListener = dom.window.EventTarget.prototype.addEventListener;
  dom.window.EventTarget.prototype.addEventListener = function (
    this: EventTarget,
    type: string,
    listener: EventListenerOrEventListenerObject | null,
    options?: boolean | AddEventListenerOptions,
  ) {
    if (
      typeof options === 'object' &&
      options !== null &&
      options.signal &&
      !(options.signal instanceof dom.window.AbortSignal)
    ) {
      const source = options.signal;
      let signal = signals.get(source);
      if (!signal) {
        const controller = new dom.window.AbortController();
        const abort = () => controller.abort(source.reason);
        if (source.aborted) abort();
        else {
          source.addEventListener('abort', abort, { once: true });
          detachSignals.push(() => source.removeEventListener('abort', abort));
        }
        signal = controller.signal;
        signals.set(source, signal!);
      }
      options = { ...options, signal };
    }
    return addEventListener.call(this, type, listener, options);
  };
  dom.window.HTMLDialogElement.prototype.showModal = function (
    this: HTMLDialogElement,
  ) {
    this.setAttribute('open', '');
    dialogs.push(this);
  };
  dom.window.HTMLDialogElement.prototype.close = function (
    this: HTMLDialogElement,
  ) {
    if (!this.open) return;
    this.removeAttribute('open');
    this.dispatchEvent(new dom.window.Event('close'));
  };
  let disposed = false;
  return {
    window: dom.window as Window & typeof globalThis,
    dialogs,
    root() {
      const root = dom.window.document.createElement('div') as HTMLDivElement;
      dom.window.document.body.appendChild(root);
      return root;
    },
    cleanup() {
      if (disposed) return;
      disposed = true;
      for (const detach of detachSignals) detach();
      dom.window.close();
      for (const [key, descriptor] of previous) {
        if (descriptor) Object.defineProperty(globalThis, key, descriptor);
        else Reflect.deleteProperty(globalThis, key);
      }
      // dispatchEvent reports handler failures through window.error instead of
      // throwing to its caller. They must still fail the enclosing test suite.
      if (listenerErrors.length) throw listenerErrors[0];
    },
  };
}

export function query<T extends Element = HTMLInputElement>(
  root: ParentNode,
  selector: string,
): T {
  const element = root.querySelector<T>(selector);
  assert.ok(element, `missing element: ${selector}`);
  return element;
}

export function emit(
  element: Element,
  name: string,
  init: KeyboardEventInit & MouseEventInit = {},
) {
  const view = element.ownerDocument.defaultView!;
  const EventType = name.startsWith('key')
    ? view.KeyboardEvent
    : name === 'click'
      ? view.MouseEvent
      : view.Event;
  const event = new EventType(name, {
    bubbles: true,
    cancelable: true,
    ...init,
  });
  element.dispatchEvent(event);
  return event;
}

export const settle = () => setImmediate();
