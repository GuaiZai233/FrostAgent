import { BotStatus, LogEntry, LogLevel } from '@frostagent/proto';

export const logLevelOptions = [
  { value: LogLevel.UNSPECIFIED, label: '全部', tone: 'neutral' },
  { value: LogLevel.DEBUG, label: '调试', tone: 'debug' },
  { value: LogLevel.INFO, label: '信息', tone: 'info' },
  { value: LogLevel.WARN, label: '警告', tone: 'warn' },
  { value: LogLevel.ERROR, label: '错误', tone: 'error' },
] as const;

export function formatCount(value: bigint | number | undefined | null): string {
  if (value === undefined || value === null) return '0';
  return new Intl.NumberFormat('zh-CN').format(value);
}

export function formatUptime(totalSeconds: bigint | number | undefined | null): string {
  if (totalSeconds === undefined || totalSeconds === null) return '未知';
  const seconds = Number(totalSeconds);
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);

  if (days > 0) {
    return `上线 ${days} 天 ${hours} 小时`;
  }
  if (hours > 0) {
    return `上线 ${hours} 小时 ${minutes} 分钟`;
  }
  return `上线 ${Math.max(minutes, 0)} 分钟`;
}

export function formatDateTime(value: string | undefined | null): string {
  if (!value) {
    return '暂无';
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'medium',
  }).format(date);
}

export function formatStatus(status: BotStatus): string {
  switch (status) {
    case BotStatus.RUNNING:
      return '运行中';
    case BotStatus.INITIALIZING:
      return '初始化中';
    case BotStatus.ERROR:
      return '出现错误';
    default:
      return '未知';
  }
}

export function formatPlatform(platform: string | undefined | null): string {
  if (!platform) return '未知';
  switch (platform.toLowerCase()) {
    case 'group':
      return '群聊';
    case 'private':
      return '私聊';
    case 'unknown':
      return '未知';
    case 'onebot':
      return 'OneBot';
    case 'astrbot':
      return 'AstrBot';
    case 'aiocqhttp':
      return 'aiocqhttp';
    case 'telegram':
      return 'Telegram';
    case 'discord':
      return 'Discord';
    default:
      return platform;
  }
}

export function isGroupSession(
  session: { id?: string; sessionId?: string; platform?: string } | string | null | undefined,
): boolean {
  if (!session) {
    return false;
  }
  if (typeof session === 'string') {
    const s = session.toLowerCase();
    return s.startsWith('group:') || s.includes(':group:');
  }
  if (session.platform?.toLowerCase() === 'group') {
    return true;
  }
  const id = (session.id ?? session.sessionId ?? '').toLowerCase();
  return id.startsWith('group:') || id.includes(':group:');
}

export function formatLogLevel(level: LogLevel): string {
  return logLevelOptions.find((option) => option.value === level)?.label ?? '未知';
}

export function logLevelTone(level: LogLevel): string {
  return logLevelOptions.find((option) => option.value === level)?.tone ?? 'neutral';
}

export function logLevelBadgeClass(level: LogLevel): string {
  switch (level) {
    case LogLevel.ERROR:
      return 'badge-destructive';
    case LogLevel.WARN:
      return 'badge-warning';
    case LogLevel.DEBUG:
      return 'badge-purple';
    case LogLevel.INFO:
      return 'badge-info';
    default:
      return 'badge-outline';
  }
}

export function maskSecret(value: string): string {
  if (!value) {
    return '';
  }
  if (value.length <= 4) {
    return '****';
  }
  return `${'*'.repeat(value.length - 4)}${value.slice(-4)}`;
}

export function escapeHtml(str: string | null | undefined): string {
  if (str === null || str === undefined) return '';
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

export class PageTokenStack {
  private readonly tokens: string[] = [''];
  private index = 0;
  private nextTok = '';

  get pageIndex(): number {
    return this.index;
  }

  get currentToken(): string {
    return this.tokens[this.index] ?? '';
  }

  get canGoBack(): boolean {
    return this.index > 0;
  }

  get canGoNext(): boolean {
    return Boolean(this.nextTok);
  }

  setNextToken(token: string | undefined | null): void {
    this.nextTok = token || '';
  }

  next(): void {
    if (!this.nextTok) return;
    this.tokens.splice(this.index + 1);
    this.tokens.push(this.nextTok);
    this.index += 1;
    this.nextTok = '';
  }

  prev(): void {
    if (this.canGoBack) {
      this.index -= 1;
      this.nextTok = '';
    }
  }

  reset(): void {
    this.tokens.splice(1);
    this.tokens[0] = '';
    this.index = 0;
    this.nextTok = '';
  }
}

export function formatConsoleLog(entry: LogEntry): string {
  let timeStr = '00:00:00';
  if (entry.timestamp) {
    const d = new Date(entry.timestamp);
    if (!Number.isNaN(d.getTime())) {
      const pad = (n: number) => String(n).padStart(2, '0');
      timeStr = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
    } else {
      timeStr = entry.timestamp;
    }
  }

  let levelStr = 'INFO';
  switch (entry.level) {
    case LogLevel.DEBUG:
      levelStr = 'DEBUG';
      break;
    case LogLevel.INFO:
      levelStr = 'INFO';
      break;
    case LogLevel.WARN:
      levelStr = 'WARN';
      break;
    case LogLevel.ERROR:
      levelStr = 'ERROR';
      break;
    default:
      levelStr = 'INFO';
      break;
  }

  const sourceStr = entry.source || 'SYSTEM';
  const contentStr = entry.summary || entry.responseBody || entry.requestBody || '';

  return `[${timeStr}][${levelStr}][${sourceStr}] ${contentStr}`;
}

export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // fallback to execCommand
  }

  try {
    const textArea = document.createElement('textarea');
    textArea.value = text;
    textArea.style.position = 'fixed';
    textArea.style.left = '-999999px';
    textArea.style.top = '-999999px';
    textArea.style.opacity = '0';
    document.body.appendChild(textArea);
    textArea.focus();
    textArea.select();
    const success = document.execCommand('copy');
    textArea.remove();
    return success;
  } catch {
    return false;
  }
}

