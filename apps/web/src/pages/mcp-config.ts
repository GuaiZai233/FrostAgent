/**
 * MCP Server configuration types, quote-aware JSON parser, and synchronization utilities.
 */

export interface DraftServerConfig {
  id: string;
  name: string;
  enabled: boolean;
  transportType: 'stdio' | 'streamable_http' | 'sse';
  command: string;
  args: string[];
  workingDir: string;
  env: Record<string, string>;
  url: string;
  headers: Record<string, string>;
}

export interface ParseDraftResult {
  success: boolean;
  draft?: DraftServerConfig;
  error?: string;
  message?: string;
}

/**
 * Quote-aware JSON cleaner that strips // and /* * / comments and trailing commas
 * outside of string literals without corrupting URLs (e.g. "https://...") or string content.
 */
export function cleanJSON(raw: string): string {
  let result = '';
  const len = raw.length;
  let i = 0;

  while (i < len) {
    const ch = raw[i];

    // Double-quoted string literal
    if (ch === '"') {
      result += ch;
      i++;
      while (i < len) {
        const strCh = raw[i];
        result += strCh;
        if (strCh === '\\') {
          // Escaped character inside string
          i++;
          if (i < len) {
            result += raw[i];
          }
        } else if (strCh === '"') {
          break;
        }
        i++;
      }
      i++;
      continue;
    }

    // Single-line comment //
    if (ch === '/' && i + 1 < len && raw[i + 1] === '/') {
      i += 2;
      while (i < len && raw[i] !== '\n' && raw[i] !== '\r') {
        i++;
      }
      continue;
    }

    // Multi-line comment /* ... */
    if (ch === '/' && i + 1 < len && raw[i + 1] === '*') {
      i += 2;
      while (i < len) {
        if (raw[i] === '*' && i + 1 < len && raw[i + 1] === '/') {
          i += 2;
          break;
        }
        if (raw[i] === '\n') {
          result += '\n'; // preserve line breaks
        }
        i++;
      }
      continue;
    }

    // Trailing comma before } or ]
    if (ch === ',') {
      let j = i + 1;
      let isTrailing = false;
      while (j < len) {
        const lookCh = raw[j];
        if (lookCh === ' ' || lookCh === '\t' || lookCh === '\n' || lookCh === '\r') {
          j++;
          continue;
        }
        if (lookCh === '/' && j + 1 < len && raw[j + 1] === '/') {
          j += 2;
          while (j < len && raw[j] !== '\n' && raw[j] !== '\r') {
            j++;
          }
          continue;
        }
        if (lookCh === '/' && j + 1 < len && raw[j + 1] === '*') {
          j += 2;
          while (j < len) {
            if (raw[j] === '*' && j + 1 < len && raw[j + 1] === '/') {
              j += 2;
              break;
            }
            j++;
          }
          continue;
        }
        if (lookCh === '}' || lookCh === ']') {
          isTrailing = true;
        }
        break;
      }

      if (isTrailing) {
        i++;
        continue;
      }
    }

    result += ch;
    i++;
  }

  return result.trim();
}

/**
 * Creates a clean default DraftServerConfig with normalized defaults.
 */
export function createDefaultDraft(
  _isEdit = false,
  existing?: Partial<DraftServerConfig>
): DraftServerConfig {
  let transport: 'stdio' | 'streamable_http' | 'sse' = 'stdio';
  const existingTransport = existing?.transportType as string | undefined;
  if (existingTransport === 'sse') {
    transport = 'sse';
  } else if (existingTransport === 'streamable_http' || existingTransport === 'http') {
    transport = 'streamable_http';
  }

  return {
    id: existing?.id || '',
    name: existing?.name || '',
    enabled: existing?.enabled !== undefined ? existing.enabled : true,
    transportType: transport,
    command: existing?.command || '',
    args: existing?.args ? [...existing.args] : [],
    workingDir: existing?.workingDir || '',
    env: existing?.env ? { ...existing.env } : {},
    url: existing?.url || '',
    headers: existing?.headers ? { ...existing.headers } : {},
  };
}

/**
 * Serializes a DraftServerConfig into pretty-printed JSON.
 */
export function draftToJSON(draft: DraftServerConfig): string {
  const obj: Record<string, unknown> = {};
  if (draft.id) obj.id = draft.id;
  if (draft.name) obj.name = draft.name;
  obj.enabled = draft.enabled;
  obj.transportType = draft.transportType;

  if (draft.transportType === 'stdio') {
    if (draft.command) obj.command = draft.command;
    if (draft.args && draft.args.length > 0) obj.args = draft.args;
    if (draft.workingDir) obj.workingDir = draft.workingDir;
    if (draft.env && Object.keys(draft.env).length > 0) obj.env = draft.env;
  } else {
    if (draft.url) obj.url = draft.url;
    if (draft.headers && Object.keys(draft.headers).length > 0) obj.headers = draft.headers;
  }

  return JSON.stringify(obj, null, 2);
}

/**
 * Parses a JSON text and creates a fully normalized DraftServerConfig.
 * If fields were removed from the JSON, they will be reset to blank/empty defaults,
 * ensuring the JSON acts as the true source of truth.
 */
export function parseJSONToDraft(
  raw: string,
  currentDraft: DraftServerConfig,
  isEdit: boolean
): ParseDraftResult {
  const text = raw.trim();
  if (!text) {
    return { success: false, error: 'JSON 内容为空' };
  }

  const cleaned = cleanJSON(text);
  let parsed: unknown;
  try {
    parsed = JSON.parse(cleaned);
  } catch (e1) {
    if (!cleaned.startsWith('{') && !cleaned.startsWith('[')) {
      try {
        parsed = JSON.parse(`{${cleaned}}`);
      } catch {
        // Fall back to original error
      }
    }
    if (!parsed) {
      const msg = e1 instanceof Error ? e1.message : String(e1);
      return { success: false, error: `JSON 语法错误: ${msg}` };
    }
  }

  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return { success: false, error: 'JSON 内容必须是一个对象' };
  }

  const parsedRecord = parsed as Record<string, unknown>;
  let target: Record<string, unknown> = parsedRecord;
  let inferredId = '';
  let note = '';

  // 1. Claude Desktop / Cursor style: { "mcpServers": { "<id>": { ... } } }
  if (parsedRecord.mcpServers && typeof parsedRecord.mcpServers === 'object') {
    const serversObj = parsedRecord.mcpServers as Record<string, unknown>;
    const keys = Object.keys(serversObj);
    if (keys.length === 0) {
      return { success: false, error: 'mcpServers 对象为空' };
    }
    inferredId = keys[0];
    const firstServer = serversObj[keys[0]];
    if (typeof firstServer === 'object' && firstServer !== null) {
      target = firstServer as Record<string, unknown>;
    }
    if (keys.length > 1) {
      note = `识别到 ${keys.length} 个服务器，已载入首个 "${keys[0]}"`;
    }
  } else if (
    Object.keys(parsedRecord).length === 1 &&
    typeof Object.values(parsedRecord)[0] === 'object' &&
    Object.values(parsedRecord)[0] !== null
  ) {
    // 2. Single-key wrapped format: { "my_server": { "command": ... } }
    const singleKey = Object.keys(parsedRecord)[0];
    const singleVal = Object.values(parsedRecord)[0] as Record<string, unknown>;
    if (
      singleVal.command !== undefined ||
      singleVal.url !== undefined ||
      singleVal.transport !== undefined ||
      singleVal.transportType !== undefined ||
      singleVal.args !== undefined
    ) {
      inferredId = singleKey;
      target = singleVal;
    }
  }

  if (typeof target !== 'object' || target === null) {
    return { success: false, error: '未解析到有效的 MCP 配置对象' };
  }

  // Determine transport type
  let transport: 'stdio' | 'streamable_http' | 'sse' = 'stdio';
  const tType = target.transportType || target.transport || target.type;
  if (typeof tType === 'string') {
    const lower = tType.toLowerCase();
    if (lower.includes('sse')) {
      transport = 'sse';
    } else if (lower.includes('http')) {
      transport = 'streamable_http';
    } else {
      transport = 'stdio';
    }
  } else if (target.url) {
    transport = String(target.url).toLowerCase().includes('sse') ? 'sse' : 'streamable_http';
  } else {
    transport = 'stdio';
  }

  // Re-build clean DraftServerConfig from scratch so deleted JSON fields are cleared!
  const nextDraft: DraftServerConfig = {
    id: isEdit ? currentDraft.id : String(target.id || inferredId || ''),
    name: String(target.name || target.id || inferredId || ''),
    enabled: typeof target.enabled === 'boolean' ? target.enabled : true,
    transportType: transport,
    command: '',
    args: [],
    workingDir: '',
    env: {},
    url: '',
    headers: {},
  };

  // Command
  if (target.command !== undefined && target.command !== null) {
    nextDraft.command = String(target.command);
  }

  // Args: array-native lossless parsing
  if (Array.isArray(target.args)) {
    nextDraft.args = target.args.map((a) => (a === null || a === undefined ? '' : String(a)));
  } else if (typeof target.args === 'string' && target.args.trim().length > 0) {
    nextDraft.args = [target.args];
  }

  // Working Directory
  const workingDirVal = target.workingDir ?? target.working_dir ?? target.cwd;
  if (workingDirVal !== undefined && workingDirVal !== null) {
    nextDraft.workingDir = String(workingDirVal);
  }

  // Environment variables
  if (target.env && typeof target.env === 'object' && !Array.isArray(target.env)) {
    const envObj = target.env as Record<string, unknown>;
    for (const [k, v] of Object.entries(envObj)) {
      if (k) {
        nextDraft.env[k] = v === null || v === undefined ? '' : String(v);
      }
    }
  }

  // URL
  if (target.url !== undefined && target.url !== null) {
    nextDraft.url = String(target.url);
  }

  // Headers
  if (target.headers && typeof target.headers === 'object' && !Array.isArray(target.headers)) {
    const headersObj = target.headers as Record<string, unknown>;
    for (const [k, v] of Object.entries(headersObj)) {
      if (k) {
        nextDraft.headers[k] = v === null || v === undefined ? '' : String(v);
      }
    }
  }

  return {
    success: true,
    draft: nextDraft,
    message: note || '已识别并同步至表单',
  };
}

export function parseEnvText(text: string): Record<string, string> {
  const envMap: Record<string, string> = {};
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const eqIdx = trimmed.indexOf('=');
    if (eqIdx > 0) {
      envMap[trimmed.slice(0, eqIdx).trim()] = trimmed.slice(eqIdx + 1).trim();
    }
  }
  return envMap;
}

export function formatEnvMap(map?: Record<string, string>): string {
  if (!map) return '';
  return Object.entries(map)
    .map(([k, v]) => `${k}=${v}`)
    .join('\n');
}

export function parseHeadersText(text: string): Record<string, string> {
  const headerMap: Record<string, string> = {};
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const colonIdx = trimmed.indexOf(':');
    if (colonIdx > 0) {
      headerMap[trimmed.slice(0, colonIdx).trim()] = trimmed.slice(colonIdx + 1).trim();
    }
  }
  return headerMap;
}

export function formatHeadersMap(map?: Record<string, string>): string {
  if (!map) return '';
  return Object.entries(map)
    .map(([k, v]) => `${k}: ${v}`)
    .join('\n');
}
