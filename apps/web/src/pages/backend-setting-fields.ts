export type SettingField = {
  label: string;
  group: string;
  type?: 'number' | 'switch' | 'secret' | 'text' | 'select';
  options?: readonly { value: string; label: string }[];
  hint?: string;
  min?: number;
  max?: number;
  step?: number;
  hot?: boolean;
};

// Keep the interface labels here so internal configuration keys never become
// the primary language of the settings page.
export const settingFields: Record<string, SettingField> = {
  LISTEN_ADDR: { label: '管理页面监听地址', group: '网络与连接', hot: true },
  WS_LISTEN_ADDR: { label: '适配器监听地址', group: '网络与连接' },
  HTTP_ALLOWED_ORIGINS: { label: '管理页面允许的跨域来源', group: '网络与连接', hot: true },
  WS_ALLOWED_ORIGINS: { label: 'WebSocket 允许的来源', group: '网络与连接', hot: true },
  SECURITY_CONTROL_MODE: { label: '安全控制模式', group: '安全与权限', type: 'select', hot: true, options: [
    { value: 'off', label: '关闭' }, { value: 'simple', label: '基础' }, { value: 'aggressive', label: '严格' },
  ] },
  MAX_CONTEXT_MESSAGES: { label: '保留消息条数', group: '对话与上下文', type: 'number', min: 4, step: 1, hot: true },
  MAX_CONTEXT_CHARS: { label: '上下文字符上限', group: '对话与上下文', type: 'number', min: 1, step: 1 },
  BOT_NAME: { label: '机器人名称', group: '对话与上下文', hot: true },
  BOT_ALIASES: { label: '机器人唤醒别名', group: '对话与上下文', hint: '多个名称用英文逗号分隔', hot: true },
  ADMIN_QQ_IDS: { label: '管理员 QQ 号', group: '安全与权限', hint: '多个号码用英文逗号分隔', hot: true },
  ADMIN_COMMAND_PREFIX: { label: '管理员指令前缀', group: '安全与权限', hot: true },
  GROUP_COMPACT_BUFFER_SIZE: { label: '群聊压缩批量消息数', group: '记忆与压缩', type: 'number', min: 1, step: 1 },
  GROUP_COMPACT_MAX_BUFFER_SIZE: { label: '群聊压缩最大缓冲消息数', group: '记忆与压缩', type: 'number', min: 1, step: 1 },
  GROUP_COMPACT_MIN_INTERVAL: { label: '群聊压缩最短间隔', group: '记忆与压缩', hint: '例如 30s 或 2m' },
  GROUP_RAW_CONTEXT_MAX_CHARS: { label: '群聊原消息上下文字符上限', group: '记忆与压缩', type: 'number', min: 1, step: 1, hot: true },
  MEMORY_EXTRACT_BATCH_MIN: { label: '记忆提取最小累计轮数', group: '记忆与压缩', type: 'number', min: 1, step: 1, hot: true },
  MEMORY_EXTRACT_BATCH_MAX: { label: '记忆提取最大累计轮数', group: '记忆与压缩', type: 'number', min: 1, step: 1, hot: true },
  MEMORY_REFLECTION_TIMEOUT: { label: '记忆反思超时时间', group: '记忆与压缩', hint: '例如 30s 或 2m' },
  ENABLE_ONEBOT_ADAPTER: { label: '启用 OneBot 适配器', group: '适配器', type: 'switch' },
  ONEBOT_ACTION_TIMEOUT: { label: 'OneBot 动作确认超时', group: '适配器', hint: '例如 10s', hot: true },
  ENABLE_ASTRBOT_ADAPTER: { label: '启用 AstrBot 适配器', group: '适配器', type: 'switch' },
  BILLING_ENABLED: { label: '启用计费', group: '计费', type: 'switch' },
  ALCYONE_BASE_URL: { label: '计费服务地址', group: '计费' },
  ALCYONE_SERVICE_TOKEN: { label: '计费服务 Token', group: '计费', type: 'secret' },
  ALCYONE_TIMEOUT: { label: '计费请求超时', group: '计费', hint: '例如 10s' },
  BILLING_MAX_OUTPUT_TOKENS: { label: '预留输出 Token 上限', group: '计费', type: 'number', min: 1, step: 1 },
  BILLING_SAFETY_MULTIPLIER: { label: '输入 Token 安全倍率', group: '计费', type: 'number', min: 0, step: 0.01 },
  BILLING_PROMPT_PRICE_PER_MILLION: { label: '每百万输入 Token 价格', group: '计费', type: 'number', min: 0, step: 0.0001 },
  BILLING_COMPLETION_PRICE_PER_MILLION: { label: '每百万输出 Token 价格', group: '计费', type: 'number', min: 0, step: 0.0001 },
  SANDBOX_ENABLED: { label: '启用隔离命令执行', group: '沙箱', type: 'switch', hot: true },
  SANDBOX_BASE_URL: { label: '沙箱网关地址', group: '沙箱', hot: true },
  SANDBOX_AUTH_TOKEN: { label: '沙箱网关 Token', group: '沙箱', type: 'secret', hot: true },
  SANDBOX_SESSION_NAMESPACE: { label: '沙箱会话命名空间', group: '沙箱', hot: true },
  MCP_CONTROL_TOKEN: { label: 'MCP 管理 Token', group: '安全与权限', type: 'secret', hot: true },
  ADMIN_TOKEN: { label: '兼容管理 Token', group: '安全与权限', type: 'secret', hot: true },
  ALLOW_REMOTE_MCP_MANAGEMENT: { label: '允许远程管理 MCP', group: '安全与权限', type: 'switch', hot: true },
  MCP_ENFORCE_LOCAL_TOKEN: { label: '本机 MCP 管理也要求 Token', group: '安全与权限', type: 'switch', hot: true },
  UPSTREAM_API_KEY: { label: '上游 API 密钥', group: '模型与智能体', type: 'secret' },
  CODER_API_KEY: { label: 'Coder API 密钥', group: '模型与智能体', type: 'secret' },
  SECURITY_GATEWAY_TIMEOUT: { label: '安全审查网关超时', group: '安全与权限', hint: '例如 15s' },
  SECURITY_CLASSIFIER_TIMEOUT: { label: '安全分类器超时', group: '安全与权限', hint: '例如 15s' },
  AGENT_MAX_ITERATIONS: { label: '智能体最大迭代轮数', group: '模型与智能体', type: 'number', min: 1, step: 1 },
  ACTIONSCAT_ENDPOINT: { label: 'ActionsCat 服务地址', group: 'ActionsCat', hot: true },
  ACTIONSCAT_MANAGEMENT_TOKEN: { label: 'ActionsCat 管理 Token', group: 'ActionsCat', type: 'secret', hot: true },
  ACTIONSCAT_DISPATCH_TOKEN: { label: 'ActionsCat 调度 Token', group: 'ActionsCat', type: 'secret', hot: true },
};

export const dedicatedSettingKeys = new Set([
  'SYSTEM_PROMPT',
  'ENABLE_AT_IN_GROUP_MSG', 'GROUP_REPLY_ON_MENTION', 'ENABLE_REPLY_IN_GROUP_MSG',
  'ENABLE_PROACTIVE_REPLY', 'PROACTIVE_REPLY_PROBABILITY',
  'PROACTIVE_REPLY_GROUP_WHITELIST', 'ENABLE_PROACTIVE_REPLY_WHITELIST',
  'STICKER_ACTIVITY', 'STICKER_ACTIVITY_MODE',
]);
