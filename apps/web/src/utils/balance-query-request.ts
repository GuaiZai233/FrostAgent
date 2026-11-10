export type BalanceQueryTarget = { origin: string; path: string };

// Mirrors the quoting rules in internal/balancequery/curl.go. This only identifies
// the UI's request target; the backend remains responsible for request validation.
function splitCurl(raw: string): string[] | null {
  if (new TextEncoder().encode(raw).length > 65536 || raw.includes('\0')) return null;
  const words: string[] = [];
  const chars = Array.from(raw);
  let word = '';
  let quote = '';
  let started = false;
  for (let i = 0; i < chars.length; i++) {
    const ch = chars[i];
    if (ch === '\\' && quote !== "'") {
      const next = chars[i + 1];
      if (next === undefined) return null;
      if (next === '\r' && chars[i + 2] === '\n') { i += 2; continue; }
      if (next === '\n') { i++; continue; }
      if (quote === '"' && !['"', '\\', '$', '`'].includes(next)) {
        word += ch;
      } else {
        word += next;
        i++;
      }
      started = true;
      continue;
    }
    if (quote) {
      if (ch === quote) quote = ''; else word += ch;
      started = true;
    } else if (ch === "'" || ch === '"') {
      quote = ch;
      started = true;
    } else if (/\p{White_Space}/u.test(ch)) {
      if (started) { words.push(word); word = ''; started = false; }
    } else {
      if (';|`<>$'.includes(ch)) return null;
      word += ch;
      started = true;
    }
  }
  if (quote) return null;
  if (started) words.push(word);
  return words.length <= 256 ? words : null;
}

function parseTargetURL(raw: string): BalanceQueryTarget | null {
  // Parse the selected URL, never the whole curl text. Keep the raw authority and
  // path: URL.origin drops explicit default ports and URL.pathname cleans dot
  // segments, whereas Go's origin/path comparison preserves both.
  const parts = /^(https?):\/\/([^\s/?#\\@]+)(\/[^?#]*)?(?:\?[^#]*)?$/i.exec(raw);
  if (!parts || Array.from(raw).some((ch) => ch.charCodeAt(0) < 32 || ch.charCodeAt(0) === 127)) return null;
  try {
    const url = new URL(raw);
    const path = decodeURIComponent(parts[3] ?? '');
    if (!url.hostname || url.username || url.password || url.hash) return null;
    if (parts[2].includes('{{API_KEY}}') || path.includes('{{API_KEY}}')) return null;
    return { origin: `${parts[1]}://${parts[2]}`.toLowerCase(), path };
  } catch {
    return null;
  }
}

export function balanceQueryOrigin(rawUrl: string): string {
  return parseTargetURL(rawUrl.trim())?.origin ?? '';
}

const flagOptions = new Set(['-s', '--silent', '-S', '--show-error', '-G', '--get']);
const valueOptions = new Set(['-X', '--request', '-H', '--header', '-d', '--data', '--data-raw', '--data-binary', '--url']);

export function parseBalanceQueryTarget(raw: string): BalanceQueryTarget | null {
  const words = splitCurl(raw);
  if (!words || words.length < 2 || !['curl', 'curl.exe'].includes(words[0])) return null;
  let target: string | null = null;
  for (let i = 1; i < words.length; i++) {
    const arg = words[i];
    let option = arg;
    let value: string | undefined;
    const equals = arg.indexOf('=');
    if (arg.startsWith('--') && equals !== -1) {
      option = arg.slice(0, equals);
      value = arg.slice(equals + 1);
    } else if (arg.length > 2 && ['-H', '-X', '-d'].includes(arg.slice(0, 2))) {
      option = arg.slice(0, 2);
      value = arg.slice(2);
    }
    if (flagOptions.has(option)) continue;
    if (valueOptions.has(option)) {
      if (value === undefined) value = words[++i];
      if (value === undefined) return null;
      if (option !== '--url') continue;
    } else {
      if (arg.startsWith('-')) return null;
      value = arg;
    }
    if (target !== null || !value) return null;
    target = value;
  }
  return target === null ? null : parseTargetURL(target);
}
