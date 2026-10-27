export type SettingField = {
  label: string;
  group: string;
  type?: 'number' | 'switch' | 'secret' | 'text' | 'select';
  options?: readonly { value: string; label: string }[];
  hint?: string;
  min?: number;
  max?: number;
  step?: number | 'any';
  positive?: boolean;
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
  ADMIN_QQ_IDS: { label: '管理员 QQ 号', group: '安全与权限', hint: '多个号码用英文逗号分隔', hot: true },
  ADMIN_COMMAND_PREFIX: { label: '管理员指令前缀', group: '安全与权限', hot: true },
  GROUP_COMPACT_BUFFER_SIZE: { label: '群聊压缩每批消息数', group: '记忆与压缩', type: 'number', min: 1, step: 1, hint: '每批处理的原消息条数' },
  GROUP_COMPACT_MAX_BUFFER_SIZE: { label: '群聊压缩最大积压消息数', group: '记忆与压缩', type: 'number', min: 1, step: 1, hint: '低于每批条数时自动抬高；积压溢出会淘汰消息' },
  GROUP_COMPACT_MIN_INTERVAL: { label: '群聊压缩最短间隔', group: '记忆与压缩', hint: '同群两次自动压缩的最短间隔，例如 30s 或 2m' },
  GROUP_RAW_CONTEXT_MAX_CHARS: { label: '未压缩群消息字符上限', group: '记忆与压缩', type: 'number', min: 1, step: 1, hot: true, hint: '只限制注入上下文的未压缩群消息部分，不限制整个请求或记忆' },
  MEMORY_EXTRACT_BATCH_MIN: { label: '记忆提取随机轮数下限', group: '记忆与压缩', type: 'number', min: 1, step: 1, hot: true, hint: '与上限共同形成随机触发区间，默认 3～5 个符合条件的对话轮次；不计每条群消息' },
  MEMORY_EXTRACT_BATCH_MAX: { label: '记忆提取随机轮数上限', group: '记忆与压缩', type: 'number', min: 1, step: 1, hot: true, hint: '只控制记忆提取，不控制群聊压缩；低于下限时按下限处理' },
  MEMORY_REFLECTION_TIMEOUT: { label: '记忆反思超时时间', group: '记忆与压缩', hint: '例如 30s 或 2m' },
  ONEBOT_ACTION_TIMEOUT: { label: 'OneBot 动作确认超时', group: '适配器', hint: '例如 10s', hot: true },
  BILLING_ENABLED: { label: '启用计费', group: '计费', type: 'switch' },
  ALCYONE_BASE_URL: { label: '计费服务地址', group: '计费' },
  ALCYONE_SERVICE_TOKEN: { label: '计费服务 Token', group: '计费', type: 'secret' },
  ALCYONE_TIMEOUT: { label: '计费请求超时', group: '计费', hint: '例如 10s' },
  BILLING_MAX_OUTPUT_TOKENS: { label: '预留输出 Token 上限', group: '计费', type: 'number', min: 1, step: 1 },
  BILLING_SAFETY_MULTIPLIER: { label: '输入 Token 安全倍率', group: '计费', type: 'number', positive: true, step: 'any', hint: '必须大于 0，留空使用 1.2' },
  BILLING_PROMPT_PRICE_PER_MILLION: { label: '每百万输入 Token 价格', group: '计费', type: 'number', min: 0, step: 1, hint: '填写内部最小计费单位的整数：100 单位＝1 雪花＝0.01 元；留空使用模型价格表' },
  BILLING_COMPLETION_PRICE_PER_MILLION: { label: '每百万输出 Token 价格', group: '计费', type: 'number', min: 0, step: 1, hint: '填写内部最小计费单位的整数：100 单位＝1 雪花＝0.01 元；留空使用模型价格表' },
  SANDBOX_ENABLED: { label: '启用隔离命令执行', group: '沙箱', type: 'switch', hot: true },
  SANDBOX_BASE_URL: { label: '沙箱网关地址', group: '沙箱', hot: true },
  SANDBOX_AUTH_TOKEN: { label: '沙箱网关 Token', group: '沙箱', type: 'secret', hot: true },
  SANDBOX_SESSION_NAMESPACE: { label: '沙箱会话命名空间', group: '沙箱', hot: true },
  MCP_CONTROL_TOKEN: { label: '管理控制令牌', group: '安全与权限', type: 'secret', hot: true },
  ADMIN_TOKEN: { label: '兼容管理令牌', group: '安全与权限', type: 'secret', hot: true, hint: '管理入口只在管理控制令牌为空时使用它；消息发送接口有独立的令牌回退顺序' },
  MCP_ENFORCE_LOCAL_TOKEN: { label: '本机管理也要求控制令牌', group: '安全与权限', type: 'switch', hot: true },
  SECURITY_GATEWAY_TIMEOUT: { label: '安全审查网关超时', group: '安全与权限', hint: '例如 15s；实例值优先于全局值，未配置时才使用兼容安全审查超时' },
  SECURITY_CLASSIFIER_TIMEOUT: { label: '兼容安全审查超时', group: '安全与权限', hint: '安全审查网关超时的旧名称，只有网关超时未配置时使用，并非独立超时' },
  AGENT_MAX_ITERATIONS: { label: '智能体基础迭代轮数', group: '模型与智能体', type: 'number', min: 1, step: 1, hint: '工具执行可能按运行逻辑追加迭代预算' },
  ACTIONSCAT_ENABLED: { label: '启用 ActionsCat', group: 'ActionsCat', type: 'switch', hot: true, hint: '默认开启；关闭后隐藏全部 ActionsCat 工具并拒绝调用' },
  ACTIONSCAT_ENDPOINT: { label: 'ActionsCat 服务地址', group: 'ActionsCat', hot: true },
  ACTIONSCAT_MANAGEMENT_TOKEN: { label: 'ActionsCat 管理 Token', group: 'ActionsCat', type: 'secret', hot: true },
  ACTIONSCAT_DISPATCH_TOKEN: { label: 'ActionsCat 调度 Token', group: 'ActionsCat', type: 'secret', hot: true, hint: '留空时复用 ActionsCat 管理 Token' },
};

export const dedicatedSettingKeys = new Set([
  'ENABLE_RUNTIME_STATUS_COMMAND',
  'ENABLE_EMBEDDING',
  'ENABLE_POKE', 'POKE_CUSTOM_TEXT', 'POKE_COOLDOWN_SECONDS',
  'SYSTEM_PROMPT',
  'BOT_NAME', 'BOT_ALIASES',
  'ENABLE_AT_IN_GROUP_MSG', 'GROUP_REPLY_ON_MENTION', 'ENABLE_REPLY_IN_GROUP_MSG',
  'ENABLE_PROACTIVE_REPLY', 'PROACTIVE_REPLY_PROBABILITY',
  'PROACTIVE_REPLY_GROUP_WHITELIST', 'ENABLE_PROACTIVE_REPLY_WHITELIST',
  'STICKER_ACTIVITY', 'STICKER_ACTIVITY_MODE',
  'BOT_LOOP_USER_IDS', 'BOT_LOOP_MAX_TURNS', 'BOT_LOOP_MODE',
  'ENABLE_QUOTE_TRACE', 'QUOTE_TRACE_DEPTH',
]);
