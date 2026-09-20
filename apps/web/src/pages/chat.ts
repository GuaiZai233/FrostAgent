import { createInstanceAPI } from '../api/client';
import { instanceState } from '../instance-state';
import { escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { instanceWebSocketURL } from '../utils/websocket-url';

interface ChatMessage {
  id: string;
  role: 'user' | 'bot' | 'system';
  sender: string;
  content: string;
  time: string;
  intermediate?: boolean;
}

interface OneBotSegment {
  type: string;
  data?: {
    text?: string;
    file?: string;
    url?: string;
    qq?: string | number;
    id?: string | number;
    [key: string]: unknown;
  };
}

interface OneBotActionFrame {
  action?: string;
  params?: {
    message?: string | OneBotSegment[];
    [key: string]: unknown;
  };
  echo?: string;
  [key: string]: unknown;
}

interface AstrBotMessageItem {
  type: string;
  text?: string;
  mention_user_id?: string;
  url?: string;
  path?: string;
  [key: string]: unknown;
}

interface AstrBotActionFrame {
  action?: string;
  content?: string;
  messages?: AstrBotMessageItem[];
  is_intermediate?: boolean;
  echo?: string;
  [key: string]: unknown;
}

export function mountChatPage(container: HTMLElement): () => void {
  const api = createInstanceAPI();
  let isUnmounted = false;
  let ws: WebSocket | null = null;
  let msgSeq = 0;

  // Connection & overview state
  let wsListenAddr = '';
  let botName = 'FrostAgent';
  let connectionStatus: 'disconnected' | 'connecting' | 'connected' | 'error' = 'disconnected';

  // Config state
  let adapter: 'onebot' | 'astrbot' = 'onebot';
  let astrbotPlatform = 'aiocqhttp';
  let chatType: 'private' | 'group' = 'private';
  let userId = '10001';
  let nickname = '调试员';
  let groupId = '100001';
  let groupName = '调试群';
  let card = '';
  let isWake = true;

  // Messages list
  const messages: ChatMessage[] = [];

  container.innerHTML = `
    <div class="page-container fade-in flex flex-col gap-4">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div>
          <div class="flex items-center gap-2">
            <h1 class="page-title">直接对话</h1>
            <span class="badge ${getStatusBadgeClass(connectionStatus)}" id="connection-status-badge">
              ${icon('radio', 'w-3 h-3')}
              <span id="connection-status-text">${getStatusText(connectionStatus)}</span>
            </span>
          </div>
          <p class="page-description">
            模拟上游协议帧直连实例模型大脑进行对话调试。会话完全隔离，断电全丢。
          </p>
        </div>
        <div class="flex items-center gap-2 flex-wrap">
          <button class="btn btn-outline btn-sm" id="btn-reconnect">
            ${icon('refresh', 'w-3.5 h-3.5')}
            <span>重新连接</span>
          </button>
          <button class="btn btn-outline btn-sm" id="btn-disconnect">
            ${icon('circle_stop', 'w-3.5 h-3.5')}
            <span>断开连接</span>
          </button>
          <button class="btn btn-outline btn-sm" id="btn-clear-chat">
            ${icon('trash', 'w-3.5 h-3.5')}
            <span>清屏</span>
          </button>
        </div>
      </header>

      <!-- Mock & Ephemeral Warning Banner -->
      <div class="card p-3 border-border flex items-center gap-2.5 text-xs text-muted" style="background-color: var(--secondary);">
        <span class="text-primary flex items-center">${icon('info', 'w-4 h-4')}</span>
        <span>
          <strong>断电全丢模式 (Mock Adapter)</strong>：当前通信通过 <code class="font-mono">?mock=true</code> 连接。对话绝不写入长期记忆库 (<code class="font-mono">brain.json</code>)，不触发群聊总结落盘，不偷取表情包。
        </span>
      </div>

      <!-- Config Card -->
      <div class="card p-4 flex flex-col gap-3.5">
        <div class="flex items-center justify-between gap-2 border-b border-border pb-2.5">
          <div class="flex items-center gap-2 text-xs font-semibold text-foreground">
            ${icon('sliders', 'w-3.5 h-3.5 text-primary')}
            <span>模拟协议与发送者配置</span>
          </div>
          <span class="text-xs text-muted">修改参数后自动更新后续数据包</span>
        </div>

        <!-- Row 1: Adapter, Chat Type, User ID, Nickname -->
        <div class="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-3">
          <div class="form-group">
            <label class="form-label text-xs" for="chat-adapter-select">模拟适配器</label>
            <select id="chat-adapter-select" class="select text-xs">
              <option value="onebot" selected>OneBot (v11 直连)</option>
              <option value="astrbot">AstrBot</option>
            </select>
          </div>

          <div class="form-group">
            <label class="form-label text-xs" for="chat-type-select">会话场景</label>
            <select id="chat-type-select" class="select text-xs">
              <option value="private" selected>私聊 (Private)</option>
              <option value="group">群聊 (Group)</option>
            </select>
          </div>

          <div class="form-group">
            <label class="form-label text-xs" for="chat-uid-input">用户 ID (UID)</label>
            <input type="text" id="chat-uid-input" class="input text-xs font-mono" value="10001" placeholder="例如: 10001" />
          </div>

          <div class="form-group">
            <label class="form-label text-xs" for="chat-nickname-input">发送者昵称</label>
            <input type="text" id="chat-nickname-input" class="input text-xs" value="调试员" placeholder="例如: 调试员" />
          </div>
        </div>

        <!-- Row 2: AstrBot platform config (hidden when OneBot is selected) -->
        <div id="astrbot-config-row" class="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-3 pt-1 border-t border-border" style="display: none;">
          <div class="form-group">
            <label class="form-label text-xs" for="chat-astrbot-platform-select">AstrBot 平台标识 (Platform)</label>
            <select id="chat-astrbot-platform-select" class="select text-xs">
              <option value="aiocqhttp" selected>aiocqhttp (QQ / NapCat, 推荐)</option>
              <option value="qq_official">qq_official (QQ 官方机器人)</option>
              <option value="telegram">telegram</option>
              <option value="discord">discord</option>
              <option value="wechat">wechat</option>
              <option value="astrbot">astrbot (通用默认)</option>
            </select>
          </div>
        </div>

        <!-- Row 3: Group configs (hidden in private mode) -->
        <div id="group-config-row" class="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-3 pt-1 border-t border-border" style="display: none;">
          <div class="form-group">
            <label class="form-label text-xs" for="chat-group-id-input">群号 (Group ID)</label>
            <input type="text" id="chat-group-id-input" class="input text-xs font-mono" value="100001" placeholder="例如: 100001" />
          </div>

          <div class="form-group">
            <label class="form-label text-xs" for="chat-group-name-input">群名称</label>
            <input type="text" id="chat-group-name-input" class="input text-xs" value="调试群" placeholder="例如: 调试群" />
          </div>

          <div class="form-group">
            <label class="form-label text-xs" for="chat-card-input">群名片 (可选)</label>
            <input type="text" id="chat-card-input" class="input text-xs" value="" placeholder="群名片/备注" />
          </div>

          <div class="form-group justify-end pb-1">
            <label class="flex items-center gap-2 text-xs font-medium cursor-pointer select-none text-foreground">
              <input type="checkbox" id="chat-is-wake-checkbox" class="checkbox" checked />
              <span>模拟 @ 唤醒 (is_wake)</span>
            </label>
            <span class="text-xs text-muted">在群聊中模拟 @机器人 触发回复</span>
          </div>
        </div>
      </div>

      <!-- Chat Messages Card -->
      <div class="card flex flex-col overflow-hidden" style="min-height: 24rem; height: calc(100vh - 29rem);">
        <!-- Messages Scroll Container -->
        <div id="chat-messages-container" class="flex-1 p-4 overflow-y-auto flex flex-col gap-3.5">
          <div class="card p-8 text-center text-muted text-xs flex flex-col items-center justify-center gap-2 m-auto" id="chat-empty-state">
            <span class="text-primary flex items-center">${icon('bot', 'w-8 h-8')}</span>
            <span class="font-medium text-foreground">直接对话模拟器已就绪</span>
            <span>选择上方适配器与场景后，在下方输入框发送消息即可与当前实例对话。</span>
          </div>
        </div>

        <!-- Input Area -->
        <div class="p-3 border-t border-border bg-background flex flex-col gap-2">
          <div class="flex items-end gap-2">
            <textarea
              id="chat-input-textarea"
              class="textarea text-xs flex-1 font-sans resize-none"
              rows="3"
              placeholder="输入消息内容... (按 Enter 发送，Shift + Enter 换行)"
            ></textarea>
            <button class="btn btn-primary btn-sm flex-shrink-0" id="btn-send-message" style="height: 3rem; padding: 0 1.25rem;">
              ${icon('play', 'w-4 h-4')}
              <span>发送</span>
            </button>
          </div>
          <div class="flex items-center justify-between text-xs text-muted">
            <span id="chat-ws-endpoint-text" class="font-mono text-xs">端点: 等待获取...</span>
            <span>Enter 发送 / Shift+Enter 换行</span>
          </div>
        </div>
      </div>
    </div>
  `;

  // Elements
  const statusBadge = container.querySelector<HTMLElement>('#connection-status-badge')!;
  const statusText = container.querySelector<HTMLElement>('#connection-status-text')!;
  const btnReconnect = container.querySelector<HTMLButtonElement>('#btn-reconnect')!;
  const btnDisconnect = container.querySelector<HTMLButtonElement>('#btn-disconnect')!;
  const btnClearChat = container.querySelector<HTMLButtonElement>('#btn-clear-chat')!;

  const adapterSelect = container.querySelector<HTMLSelectElement>('#chat-adapter-select')!;
  const astrbotConfigRow = container.querySelector<HTMLElement>('#astrbot-config-row')!;
  const astrbotPlatformSelect = container.querySelector<HTMLSelectElement>('#chat-astrbot-platform-select')!;
  const typeSelect = container.querySelector<HTMLSelectElement>('#chat-type-select')!;
  const uidInput = container.querySelector<HTMLInputElement>('#chat-uid-input')!;
  const nicknameInput = container.querySelector<HTMLInputElement>('#chat-nickname-input')!;
  const groupConfigRow = container.querySelector<HTMLElement>('#group-config-row')!;
  const groupIdInput = container.querySelector<HTMLInputElement>('#chat-group-id-input')!;
  const groupNameInput = container.querySelector<HTMLInputElement>('#chat-group-name-input')!;
  const cardInput = container.querySelector<HTMLInputElement>('#chat-card-input')!;
  const isWakeCheckbox = container.querySelector<HTMLInputElement>('#chat-is-wake-checkbox')!;

  const messagesContainer = container.querySelector<HTMLElement>('#chat-messages-container')!;
  const emptyState = container.querySelector<HTMLElement>('#chat-empty-state')!;
  const inputTextarea = container.querySelector<HTMLTextAreaElement>('#chat-input-textarea')!;
  const btnSend = container.querySelector<HTMLButtonElement>('#btn-send-message')!;
  const wsEndpointText = container.querySelector<HTMLElement>('#chat-ws-endpoint-text')!;

  function getStatusBadgeClass(st: typeof connectionStatus): string {
    switch (st) {
      case 'connected':
        return 'badge-success';
      case 'connecting':
        return 'badge-warning';
      case 'error':
        return 'badge-destructive';
      case 'disconnected':
      default:
        return 'badge-outline';
    }
  }

  function getStatusText(st: typeof connectionStatus): string {
    switch (st) {
      case 'connected':
        return '已连接';
      case 'connecting':
        return '连接中...';
      case 'error':
        return '连接错误';
      case 'disconnected':
      default:
        return '未连接';
    }
  }

  function updateStatus(st: typeof connectionStatus) {
    connectionStatus = st;
    if (statusBadge && statusText) {
      statusBadge.className = `badge ${getStatusBadgeClass(st)}`;
      statusText.textContent = getStatusText(st);
    }
  }

  function appendMessage(msg: ChatMessage) {
    messages.push(msg);
    if (emptyState) {
      emptyState.style.display = 'none';
    }

    const itemEl = document.createElement('div');
    const timeStr = msg.time || new Date().toLocaleTimeString();

    if (msg.role === 'system') {
      itemEl.className = 'flex justify-center my-1';
      itemEl.innerHTML = `
        <span class="badge badge-outline text-xs text-muted" style="padding: 0.25rem 0.75rem;">
          ${escapeHtml(msg.content)} · ${escapeHtml(timeStr)}
        </span>
      `;
    } else if (msg.role === 'user') {
      itemEl.className = 'flex flex-col items-end gap-1';
      itemEl.innerHTML = `
        <div class="flex items-center gap-1.5 text-xs text-muted">
          <span>${escapeHtml(msg.sender)}</span>
          <span class="font-mono">${escapeHtml(timeStr)}</span>
        </div>
        <div class="card p-3 max-w-[80%] rounded-2xl rounded-tr-none text-xs leading-relaxed whitespace-pre-wrap select-text font-sans"
             style="background-color: var(--primary); color: var(--primary-foreground); border-color: var(--primary);">
          ${escapeHtml(msg.content)}
        </div>
      `;
    } else {
      // Bot message
      itemEl.className = 'flex flex-col items-start gap-1';
      itemEl.innerHTML = `
        <div class="flex items-center gap-1.5 text-xs text-muted">
          <span class="text-primary font-medium">${escapeHtml(msg.sender)}</span>
          ${msg.intermediate ? '<span class="badge badge-outline text-[10px] py-0 px-1">中间过程</span>' : ''}
          <span class="font-mono">${escapeHtml(timeStr)}</span>
        </div>
        <div class="card p-3 max-w-[80%] rounded-2xl rounded-tl-none text-xs leading-relaxed whitespace-pre-wrap select-text font-sans bg-secondary border-border text-foreground">
          ${escapeHtml(msg.content)}
        </div>
      `;
    }

    messagesContainer.appendChild(itemEl);
    messagesContainer.scrollTop = messagesContainer.scrollHeight;
  }

  function computeWSURL(): string {
    const inst = instanceState.selected;
    if (!inst) return '';
    return instanceWebSocketURL(
      wsListenAddr,
      inst.id,
      adapter,
      window.location.origin,
      'mock=true',
    );
  }

  function updateEndpointDisplay() {
    const url = computeWSURL();
    if (wsEndpointText) {
      wsEndpointText.textContent = url ? `端点: ${url}` : '端点: 未配置实例';
    }
  }

  function connect() {
    if (isUnmounted) return;
    const inst = instanceState.selected;
    if (!inst) {
      toast.warning('请先在左侧选择一个实例');
      return;
    }

    disconnect();

    const url = computeWSURL();
    if (!url) {
      toast.error('无法生成 WebSocket 连接地址');
      return;
    }

    updateStatus('connecting');
    appendMessage({
      id: `sys_${Date.now()}`,
      role: 'system',
      sender: '系统',
      content: `正在连接至 ${adapter.toUpperCase()} 模拟端点...`,
      time: new Date().toLocaleTimeString(),
    });

    try {
      ws = new WebSocket(url);
    } catch (err) {
      updateStatus('error');
      appendMessage({
        id: `sys_${Date.now()}`,
        role: 'system',
        sender: '系统',
        content: `连接创建失败: ${err instanceof Error ? err.message : String(err)}`,
        time: new Date().toLocaleTimeString(),
      });
      return;
    }

    ws.onopen = () => {
      if (isUnmounted) return;
      updateStatus('connected');
      appendMessage({
        id: `sys_${Date.now()}`,
        role: 'system',
        sender: '系统',
        content: `WebSocket 连接已建立 [${adapter.toUpperCase()}]，模拟调试已就绪`,
        time: new Date().toLocaleTimeString(),
      });
    };

    ws.onclose = (ev) => {
      if (isUnmounted) return;
      updateStatus('disconnected');
      appendMessage({
        id: `sys_${Date.now()}`,
        role: 'system',
        sender: '系统',
        content: `连接已关闭 (code: ${ev.code}${ev.reason ? `, reason: ${ev.reason}` : ''})`,
        time: new Date().toLocaleTimeString(),
      });
    };

    ws.onerror = () => {
      if (isUnmounted) return;
      updateStatus('error');
      appendMessage({
        id: `sys_${Date.now()}`,
        role: 'system',
        sender: '系统',
        content: 'WebSocket 发生通信错误',
        time: new Date().toLocaleTimeString(),
      });
    };

    ws.onmessage = (event) => {
      if (isUnmounted) return;
      handleIncomingFrame(event.data);
    };
  }

  function disconnect() {
    if (ws) {
      ws.onopen = null;
      ws.onclose = null;
      ws.onerror = null;
      ws.onmessage = null;
      try {
        ws.close();
      } catch {
        // ignore
      }
      ws = null;
    }
    updateStatus('disconnected');
  }

  function handleIncomingFrame(rawData: string) {
    let payload: unknown;
    try {
      payload = JSON.parse(rawData);
    } catch {
      console.warn('DirectChat: Received non-JSON frame:', rawData);
      return;
    }

    if (!payload || typeof payload !== 'object') {
      return;
    }

    if (adapter === 'onebot') {
      handleOneBotFrame(payload as OneBotActionFrame);
    } else if (adapter === 'astrbot') {
      handleAstrBotFrame(payload as AstrBotActionFrame);
    }
  }

  function handleOneBotFrame(action: OneBotActionFrame) {
    // 1. Critical ACK handling: if action has echo, immediately respond with ACK
    // to satisfy backend SendActionAndWait
    if (action.echo && ws && ws.readyState === WebSocket.OPEN) {
      const ack = {
        status: 'ok',
        retcode: 0,
        data: {
          message_id: Math.floor(Math.random() * 1000000) + 1,
          ...(action.action === 'get_group_info'
            ? { group_id: Number(groupId) || 100001, group_name: groupName || '调试群' }
            : {}),
        },
        echo: action.echo,
      };
      try {
        ws.send(JSON.stringify(ack));
      } catch (err) {
        console.error('DirectChat: Failed to send OneBot ACK:', err);
      }
    }

    // 2. Extract outbound message text
    if (action.action === 'send_private_msg' || action.action === 'send_group_msg') {
      let replyText = '';
      const rawMsg = action.params?.message;
      if (typeof rawMsg === 'string') {
        replyText = rawMsg;
      } else if (Array.isArray(rawMsg)) {
        replyText = rawMsg
          .map((seg: OneBotSegment) => {
            if (seg.type === 'text') return String(seg.data?.text || '');
            if (seg.type === 'image') return `[图片: ${String(seg.data?.file || seg.data?.url || 'image')}]`;
            if (seg.type === 'at') return `@${String(seg.data?.qq || '')} `;
            if (seg.type === 'reply') return `[回复:${String(seg.data?.id || '')}] `;
            return `[${seg.type}]`;
          })
          .join('');
      }

      if (replyText.trim()) {
        appendMessage({
          id: `bot_${Date.now()}_${++msgSeq}`,
          role: 'bot',
          sender: botName,
          content: replyText,
          time: new Date().toLocaleTimeString(),
        });
      }
    }
  }

  function handleAstrBotFrame(action: AstrBotActionFrame) {
    if (action.action === 'send_message') {
      let replyText = action.content || '';
      if (!replyText && Array.isArray(action.messages)) {
        replyText = action.messages
          .map((m: AstrBotMessageItem) => {
            if (m.type === 'plain') return m.text || '';
            if (m.type === 'mention_user') return `@${m.mention_user_id || ''} `;
            if (m.type === 'image') return `[图片: ${m.url || m.path || 'image'}]`;
            return m.text || `[${m.type}]`;
          })
          .join('');
      }

      if (replyText.trim()) {
        appendMessage({
          id: `bot_${Date.now()}_${++msgSeq}`,
          role: 'bot',
          sender: botName,
          content: replyText,
          time: new Date().toLocaleTimeString(),
          intermediate: action.is_intermediate,
        });
      }
    }
  }

  function sendMessage() {
    const text = inputTextarea.value.trim();
    if (!text) return;

    if (!ws || ws.readyState !== WebSocket.OPEN) {
      toast.warning('WebSocket 尚未连接，请先点击重新连接');
      return;
    }

    // Append to local message list
    appendMessage({
      id: `user_${Date.now()}_${++msgSeq}`,
      role: 'user',
      sender: nickname || '调试员',
      content: text,
      time: new Date().toLocaleTimeString(),
    });

    inputTextarea.value = '';

    // Dispatch protocol frame
    if (adapter === 'onebot') {
      sendOneBotMessage(text);
    } else if (adapter === 'astrbot') {
      sendAstrBotMessage(text);
    }
  }

  function sendOneBotMessage(text: string) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    const currentUid = Number(userId) || 10001;
    const currentGid = Number(groupId) || 100001;
    const senderName = nickname || '调试员';

    if (chatType === 'group') {
      const eventPayload = {
        time: Math.floor(Date.now() / 1000),
        self_id: 1000000,
        post_type: 'message',
        message_type: 'group',
        sub_type: 'normal',
        message_id: ++msgSeq,
        group_id: currentGid,
        user_id: currentUid,
        anonymous: null,
        message: isWake
          ? [
              { type: 'at', data: { qq: '1000000' } },
              { type: 'text', data: { text: ' ' + text } },
            ]
          : [{ type: 'text', data: { text } }],
        raw_message: isWake ? `[CQ:at,qq=1000000] ${text}` : text,
        font: 0,
        sender: {
          user_id: currentUid,
          nickname: senderName,
          card: card || senderName,
          role: 'member',
          title: '',
          level: '1',
        },
      };
      ws.send(JSON.stringify(eventPayload));
    } else {
      const eventPayload = {
        time: Math.floor(Date.now() / 1000),
        self_id: 1000000,
        post_type: 'message',
        message_type: 'private',
        sub_type: 'friend',
        message_id: ++msgSeq,
        user_id: currentUid,
        message: [{ type: 'text', data: { text } }],
        raw_message: text,
        font: 0,
        sender: {
          user_id: currentUid,
          nickname: senderName,
          sex: 'unknown',
          age: 0,
        },
      };
      ws.send(JSON.stringify(eventPayload));
    }
  }

  function sendAstrBotMessage(text: string) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;

    const currentUid = String(userId || '10001');
    const currentGid = String(groupId || '100001');
    const senderName = nickname || '调试员';
    const currentPlatform = astrbotPlatform || 'aiocqhttp';

    const eventPayload = {
      type: 'event',
      event_type: 'message',
      message_id: `msg_${Date.now()}_${++msgSeq}`,
      session_id:
        chatType === 'group'
          ? `${currentPlatform}:group:${currentGid}`
          : `${currentPlatform}:private:${currentUid}`,
      platform: currentPlatform,
      message_type: chatType,
      user_id: currentUid,
      sender_name: senderName,
      sender_card: card || senderName,
      group_id: chatType === 'group' ? currentGid : '',
      group_name: chatType === 'group' ? groupName || '调试群' : '',
      content: text,
      is_wake: isWake,
      is_at: isWake,
      timestamp: Math.floor(Date.now() / 1000),
    };
    ws.send(JSON.stringify(eventPayload));
  }

  // Event Listeners
  btnReconnect.addEventListener('click', () => {
    connect();
  });

  btnDisconnect.addEventListener('click', () => {
    disconnect();
    appendMessage({
      id: `sys_${Date.now()}`,
      role: 'system',
      sender: '系统',
      content: '已手动断开连接',
      time: new Date().toLocaleTimeString(),
    });
  });

  btnClearChat.addEventListener('click', () => {
    messages.length = 0;
    messagesContainer.innerHTML = '';
    if (emptyState) {
      emptyState.style.display = 'flex';
      messagesContainer.appendChild(emptyState);
    }
  });

  adapterSelect.addEventListener('change', () => {
    adapter = adapterSelect.value as 'onebot' | 'astrbot';
    if (astrbotConfigRow) {
      astrbotConfigRow.style.display = adapter === 'astrbot' ? 'grid' : 'none';
    }
    updateEndpointDisplay();
    connect();
  });

  astrbotPlatformSelect?.addEventListener('change', () => {
    astrbotPlatform = astrbotPlatformSelect.value;
  });

  typeSelect.addEventListener('change', () => {
    chatType = typeSelect.value as 'private' | 'group';
    if (groupConfigRow) {
      groupConfigRow.style.display = chatType === 'group' ? 'grid' : 'none';
    }
  });

  uidInput.addEventListener('input', () => {
    userId = uidInput.value.trim();
  });

  nicknameInput.addEventListener('input', () => {
    nickname = nicknameInput.value.trim();
  });

  groupIdInput.addEventListener('input', () => {
    groupId = groupIdInput.value.trim();
  });

  groupNameInput.addEventListener('input', () => {
    groupName = groupNameInput.value.trim();
  });

  cardInput.addEventListener('input', () => {
    card = cardInput.value.trim();
  });

  isWakeCheckbox.addEventListener('change', () => {
    isWake = isWakeCheckbox.checked;
  });

  btnSend.addEventListener('click', () => {
    sendMessage();
  });

  inputTextarea.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  });

  // Load initial bot overview to obtain wsListenAddr and botName
  async function initOverview() {
    try {
      const overview = await api.getOverview();
      if (isUnmounted) return;
      wsListenAddr = overview.wsListenAddr || '';
      botName = overview.botName || 'FrostAgent';
      updateEndpointDisplay();
      connect();
    } catch (err) {
      console.error('DirectChat: Failed to load overview:', err);
      if (isUnmounted) return;
      updateEndpointDisplay();
      connect();
    }
  }

  void initOverview();

  // Cleanup on page unmount
  return () => {
    isUnmounted = true;
    disconnect();
  };
}
