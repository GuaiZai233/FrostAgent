import { BotStatus, LogLevel } from '@frostagent/proto';

export const logLevelOptions = [
  {
    value: LogLevel.UNSPECIFIED,
    label: $localize`:@@logLevelAll:全部`,
    tone: 'neutral',
  },
  {
    value: LogLevel.DEBUG,
    label: $localize`:@@logLevelDebug:调试`,
    tone: 'debug',
  },
  {
    value: LogLevel.INFO,
    label: $localize`:@@logLevelInfo:信息`,
    tone: 'info',
  },
  {
    value: LogLevel.WARN,
    label: $localize`:@@logLevelWarn:警告`,
    tone: 'warn',
  },
  {
    value: LogLevel.ERROR,
    label: $localize`:@@logLevelError:错误`,
    tone: 'error',
  },
] as const;

export function formatCount(value: bigint | number): string {
  return new Intl.NumberFormat($localize.locale).format(value);
}

export function formatUptime(totalSeconds: bigint | number): string {
  const seconds = Number(totalSeconds);
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);

  if (days > 0) {
    return $localize`:@@uptimeDays:上线 ${days}:INTERPOLATION: 天 ${hours}:INTERPOLATION_1: 小时`;
  }
  if (hours > 0) {
    return $localize`:@@uptimeHours:上线 ${hours}:INTERPOLATION: 小时 ${minutes}:INTERPOLATION_1: 分钟`;
  }
  return $localize`:@@uptimeMinutes:上线 ${Math.max(minutes, 0)}:INTERPOLATION: 分钟`;
}

export function formatDateTime(value: string): string {
  if (!value) {
    return $localize`:@@emptyValue:暂无`;
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return new Intl.DateTimeFormat($localize.locale, {
    dateStyle: 'medium',
    timeStyle: 'medium',
  }).format(date);
}

export function formatStatus(status: BotStatus): string {
  switch (status) {
    case BotStatus.RUNNING:
      return $localize`:@@statusRunning:运行中`;
    case BotStatus.INITIALIZING:
      return $localize`:@@statusInitializing:初始化中`;
    case BotStatus.ERROR:
      return $localize`:@@statusError:出现错误`;
    default:
      return $localize`:@@statusUnknown:未知`;
  }
}

export function formatPlatform(platform: string): string {
  switch (platform.toLowerCase()) {
    case 'group':
      return $localize`:@@platformGroup:群聊`;
    case 'private':
      return $localize`:@@platformPrivate:私聊`;
    case 'unknown':
      return $localize`:@@platformUnknown:未知`;
    default:
      return platform;
  }
}

export function formatLogLevel(level: LogLevel): string {
  return (
    logLevelOptions.find((option) => option.value === level)?.label ??
    $localize`:@@logLevelUnknown:未知`
  );
}

export function logLevelTone(level: LogLevel): string {
  return (
    logLevelOptions.find((option) => option.value === level)?.tone ?? 'neutral'
  );
}

export function maskSecret(value: string): string {
  if (!value) {
    return '';
  }
  if (value.includes('*')) {
    return value;
  }
  if (value.length <= 4) {
    return '****';
  }
  return `${'*'.repeat(value.length - 4)}${value.slice(-4)}`;
}

export class PageTokenStack {
  private readonly tokens = [''];
  private index = 0;

  current(): string {
    return this.tokens[this.index] ?? '';
  }

  canGoBack(): boolean {
    return this.index > 0;
  }

  push(nextToken: string): void {
    if (!nextToken) {
      return;
    }
    this.tokens.splice(this.index + 1);
    this.tokens.push(nextToken);
    this.index += 1;
  }

  back(): void {
    if (this.canGoBack()) {
      this.index -= 1;
    }
  }

  reset(): void {
    this.tokens.splice(1);
    this.index = 0;
  }
}
