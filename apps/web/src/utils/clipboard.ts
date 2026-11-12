export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // fallback to execCommand
  }

  const previousFocus = document.activeElement as HTMLElement | null;
  const textArea = document.createElement('textarea');
  try {
    textArea.value = text;
    textArea.readOnly = true;
    textArea.style.position = 'fixed';
    textArea.style.left = '0';
    textArea.style.top = '0';
    textArea.style.opacity = '0';
    textArea.setAttribute('aria-hidden', 'true');

    // Elements outside a modal dialog are inert and cannot receive selection.
    const dialogs = document.querySelectorAll('dialog[open]');
    const host =
      previousFocus?.closest('dialog[open]') ??
      dialogs[dialogs.length - 1] ??
      document.body;
    host.appendChild(textArea);
    textArea.focus({ preventScroll: true });
    textArea.select();
    return document.execCommand('copy');
  } catch {
    return false;
  } finally {
    textArea.remove();
    if (previousFocus?.isConnected)
      previousFocus.focus({ preventScroll: true });
  }
}
