import { createInstanceAPI } from '../api/client';
import { DialogueItem } from '@frostagent/proto';
import { escapeHtml } from '../utils/formatters';
import { icon } from '../components/icons';
import { toast } from '../components/toast';
import { openDialog } from '../components/dialog';
import { confirmDialog } from '../components/confirm';

export function mountDialoguePage(container: HTMLElement, api = createInstanceAPI()): () => void {
  let isUnmounted = false;
  let identityLoaded = false;
  let identitySaving = false;
  let savedName = '';
  let savedAliases = '';
  const identityController = new AbortController();
  let systemPrompt = '';
  let systemPromptLoaded = false;
  let systemPromptEditing = false;
  let systemPromptSaving = false;
  let systemPromptLoadSeq = 0;
  let loading = false;
  let dialogues: DialogueItem[] = [];
  let filePath = '当前实例 · 数据库存储';
  let promptPreview = '';
  let searchQuery = '';
  let viewMode: 'visual' | 'raw' = 'visual';
  let rawYaml = '';
  let rawLoading = false;
  let rawSaving = false;

  container.innerHTML = `
    <div class="page-container fade-in">
      <header class="flex items-center justify-between gap-4 flex-wrap pb-1">
        <div>
          <div class="flex items-center gap-2">
            <h1 class="page-title">人格设定</h1>
            <span class="badge badge-outline text-xs font-mono" id="dialogue-filepath">${escapeHtml(filePath)}</span>
          </div>
        </div>
        <div class="flex items-center gap-2 flex-wrap">
          <div class="tabs">
            <button class="tab-item active" id="mode-visual-btn">
              ${icon('chat', 'w-3.5 h-3.5')}
              <span>可视化管理</span>
            </button>
            <button class="tab-item" id="mode-raw-btn">
              ${icon('code', 'w-3.5 h-3.5')}
              <span>YAML 编辑</span>
            </button>
          </div>
          <button class="btn btn-outline btn-sm" id="dialogue-export-btn">
            ${icon('download', 'w-3.5 h-3.5')}
            <span>导出</span>
          </button>
          <label class="btn btn-outline btn-sm" style="cursor: pointer;">
            ${icon('upload', 'w-3.5 h-3.5')}
            <span>导入</span>
            <input type="file" id="dialogue-import-input" accept=".yml,.yaml" style="display: none;" />
          </label>
          <button class="btn btn-outline btn-icon-sm" id="dialogue-refresh-btn" title="刷新">
            ${icon('refresh', 'w-3.5 h-3.5')}
          </button>
        </div>
      </header>

      <article class="card p-4 flex flex-col gap-3">
        <h2 class="card-title text-sm font-semibold">名称与唤醒别名</h2>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <label class="flex flex-col gap-1.5 text-xs font-semibold">机器人名称
            <input id="persona-bot-name" class="input text-sm" type="text" disabled />
          </label>
          <label class="flex flex-col gap-1.5 text-xs font-semibold">唤醒别名
            <input id="persona-bot-aliases" class="input text-sm" type="text" placeholder="多个名称用英文逗号分隔" disabled />
          </label>
        </div>
        <div class="flex justify-end"><button id="persona-identity-save" type="button" class="btn btn-primary btn-sm" disabled>保存名称与别名</button></div>
      </article>

      <article class="card p-4 flex flex-col gap-3">
        <div class="flex items-center justify-between gap-3">
          <h2 class="card-title text-sm font-semibold">系统提示词 (System Prompt)</h2>
          <button type="button" class="btn btn-ghost btn-icon-sm" id="system-prompt-edit-btn" title="编辑系统提示词" aria-label="编辑系统提示词" disabled>
            ${icon('pencil', 'w-3.5 h-3.5')}
          </button>
        </div>
        <div id="system-prompt-display" class="rounded-md bg-muted p-3 text-sm text-muted whitespace-pre-wrap break-words select-text" style="min-height: 5rem;">加载中...</div>
        <div id="system-prompt-editor" class="flex flex-col gap-3" style="display: none;">
          <textarea id="system-prompt-input" class="textarea text-sm leading-relaxed" rows="8" aria-label="系统提示词"></textarea>
          <div class="flex items-center justify-end gap-2">
            <button type="button" class="btn btn-outline btn-sm" id="system-prompt-cancel-btn">取消</button>
            <button type="button" class="btn btn-primary btn-sm" id="system-prompt-save-btn">保存并生效</button>
          </div>
        </div>
      </article>

      <!-- Prompt Preview Collapsible -->
      <details class="card prompt-preview-card overflow-hidden" id="prompt-preview-details" open>
        <summary class="flex items-center justify-between p-3 cursor-pointer select-none border-b border-border hover-bg text-xs font-medium">
          <div class="flex items-center gap-2">
            <span class="text-primary flex items-center">${icon('sparkles', 'w-3.5 h-3.5')}</span>
            <span class="font-medium text-foreground">注入 System Prompt 的对话示例预览</span>
          </div>
          <div class="flex items-center gap-2">
            <button type="button" class="btn btn-ghost btn-sm" id="copy-prompt-btn" style="height: 1.5rem; padding: 0 0.5rem; font-size: 0.75rem;">
              ${icon('copy', 'w-3 h-3')}
              <span>复制片段</span>
            </button>
            <span class="text-muted text-xs">点击折叠</span>
          </div>
        </summary>
        <div class="p-3 bg-muted">
          <pre id="prompt-preview-content" class="text-xs font-mono whitespace-pre-wrap select-text leading-relaxed text-foreground" style="max-height: 12rem; overflow-y: auto;">加载中...</pre>
        </div>
      </details>

      <!-- Visual Mode Container -->
      <div id="visual-mode-container" class="flex flex-col gap-4">
        <div class="flex items-center justify-between gap-3 flex-wrap">
          <div class="flex items-center gap-2 flex-wrap">
            <div style="width: 14rem;">
              <input
                type="search"
                id="dialogue-search-input"
                class="input text-xs"
                placeholder="搜索示例对话..."
              />
            </div>
          </div>
          <button class="btn btn-primary btn-sm" id="dialogue-add-btn">
            ${icon('plus', 'w-3.5 h-3.5')}
            <span>添加对话示例</span>
          </button>
        </div>

        <div id="dialogue-cards-container" class="flex flex-col gap-3">
          <div class="card p-6 text-center text-muted">
            <span class="spinner"></span>
            <span style="margin-left: 0.5rem;">加载中...</span>
          </div>
        </div>
      </div>

      <!-- Raw Mode Container -->
      <div id="raw-mode-container" class="flex flex-col gap-3" style="display: none;">
        <div class="card p-4 flex flex-col gap-3">
          <div class="flex items-center justify-between">
            <label class="form-label" for="raw-yaml-textarea">YAML 内容编辑</label>
            <div class="flex items-center gap-2">
              <button class="btn btn-outline btn-sm" id="raw-reload-btn">
                ${icon('refresh', 'w-3.5 h-3.5')}
                <span>重新加载</span>
              </button>
              <button class="btn btn-primary btn-sm" id="raw-save-btn">
                ${icon('save', 'w-3.5 h-3.5')}
                <span>保存并应用</span>
              </button>
            </div>
          </div>
          <p class="text-xs text-muted">保存或导入将替换当前实例的全部对话示例，并即时生效。数据保存在数据库中，YAML 用于编辑和导入导出。</p>
          <textarea
            id="raw-yaml-textarea"
            class="textarea font-mono text-xs leading-relaxed"
            rows="22"
            placeholder="YAML 内容..."
            style="white-space: pre;"
          ></textarea>
        </div>
      </div>
    </div>
  `;

  // Element references
  const systemPromptDisplay = container.querySelector<HTMLElement>('#system-prompt-display')!;
  const systemPromptEditor = container.querySelector<HTMLElement>('#system-prompt-editor')!;
  const systemPromptInput = container.querySelector<HTMLTextAreaElement>('#system-prompt-input')!;
  const systemPromptEditBtn = container.querySelector<HTMLButtonElement>('#system-prompt-edit-btn')!;
  const systemPromptCancelBtn = container.querySelector<HTMLButtonElement>('#system-prompt-cancel-btn')!;
  const systemPromptSaveBtn = container.querySelector<HTMLButtonElement>('#system-prompt-save-btn')!;
  const filepathEl = container.querySelector<HTMLElement>('#dialogue-filepath')!;
  const modeVisualBtn = container.querySelector<HTMLButtonElement>('#mode-visual-btn')!;
  const modeRawBtn = container.querySelector<HTMLButtonElement>('#mode-raw-btn')!;
  const visualContainer = container.querySelector<HTMLElement>('#visual-mode-container')!;
  const rawContainer = container.querySelector<HTMLElement>('#raw-mode-container')!;
  const promptPreviewContent = container.querySelector<HTMLElement>('#prompt-preview-content')!;
  const copyPromptBtn = container.querySelector<HTMLButtonElement>('#copy-prompt-btn')!;
  const searchInput = container.querySelector<HTMLInputElement>('#dialogue-search-input')!;
  const addBtn = container.querySelector<HTMLButtonElement>('#dialogue-add-btn')!;
  const cardsContainer = container.querySelector<HTMLElement>('#dialogue-cards-container')!;
  const rawTextarea = container.querySelector<HTMLTextAreaElement>('#raw-yaml-textarea')!;
  const rawReloadBtn = container.querySelector<HTMLButtonElement>('#raw-reload-btn')!;
  const rawSaveBtn = container.querySelector<HTMLButtonElement>('#raw-save-btn')!;
  const exportBtn = container.querySelector<HTMLButtonElement>('#dialogue-export-btn')!;
  const importInput = container.querySelector<HTMLInputElement>('#dialogue-import-input')!;
  const refreshBtn = container.querySelector<HTMLButtonElement>('#dialogue-refresh-btn')!;
  const nameInput = container.querySelector<HTMLInputElement>('#persona-bot-name')!;
  const aliasesInput = container.querySelector<HTMLInputElement>('#persona-bot-aliases')!;
  const identitySave = container.querySelector<HTMLButtonElement>('#persona-identity-save')!;

  function renderIdentity(): void {
    nameInput.disabled = aliasesInput.disabled = !identityLoaded || identitySaving;
    identitySave.disabled = !identityLoaded || identitySaving || (nameInput.value === savedName && aliasesInput.value === savedAliases);
    identitySave.textContent = identitySaving ? '保存中…' : '保存名称与别名';
  }

  async function saveIdentity(): Promise<void> {
    if (!identityLoaded || identitySaving) return;
    const name = nameInput.value;
    const aliases = aliasesInput.value;
    const changes = [
      { key: 'BOT_NAME', value: name, isSecret: false },
      { key: 'BOT_ALIASES', value: aliases, isSecret: false },
    ].filter(({ key, value }) => value !== (key === 'BOT_NAME' ? savedName : savedAliases));
    if (!changes.length) return;
    identitySaving = true;
    refreshBtn.disabled = true;
    renderIdentity();
    renderSystemPrompt();
    try {
      const response = await api.updateEnvVars(changes, identityController.signal);
      if (isUnmounted) return;
      if (!response.success) throw new Error(response.error);
      savedName = name;
      savedAliases = aliases;
      toast.success('名称与别名已保存并生效');
    } catch (error) {
      if (!isUnmounted) toast.error('保存名称与别名失败: ' + String(error));
    } finally {
      identitySaving = false;
      if (!isUnmounted) {
        refreshBtn.disabled = systemPromptSaving;
        renderIdentity();
        renderSystemPrompt();
      }
    }
  }
  nameInput.addEventListener('input', renderIdentity);
  aliasesInput.addEventListener('input', renderIdentity);
  identitySave.addEventListener('click', () => void saveIdentity());

  function renderSystemPrompt() {
    systemPromptDisplay.style.display = systemPromptEditing ? 'none' : 'block';
    systemPromptEditor.style.display = systemPromptEditing ? 'flex' : 'none';
    systemPromptEditBtn.disabled = !systemPromptLoaded || systemPromptEditing || systemPromptSaving || identitySaving;
    systemPromptInput.disabled = systemPromptSaving || identitySaving;
    systemPromptCancelBtn.disabled = systemPromptSaving || identitySaving;
    systemPromptSaveBtn.disabled = systemPromptSaving || identitySaving;
    if (!systemPromptEditing) {
      systemPromptDisplay.textContent = systemPromptLoaded ? systemPrompt || '（未设置）' : '加载中...';
    }
  }

  async function loadSystemPrompt() {
    const loadSeq = ++systemPromptLoadSeq;
    systemPromptLoaded = false;
    systemPromptEditing = false;
    renderSystemPrompt();
    try {
      const vars = await api.listEnvVars();
      if (isUnmounted || loadSeq !== systemPromptLoadSeq) return;
      systemPrompt = vars.find((item) => item.key === 'SYSTEM_PROMPT')?.value ?? '';
      if (!identitySaving && (!identityLoaded || (nameInput.value === savedName && aliasesInput.value === savedAliases))) {
        savedName = vars.find((item) => item.key === 'BOT_NAME')?.value ?? '';
        savedAliases = vars.find((item) => item.key === 'BOT_ALIASES')?.value ?? '';
        nameInput.value = savedName;
        aliasesInput.value = savedAliases;
      }
      identityLoaded = true;
      renderIdentity();
      systemPromptLoaded = true;
      renderSystemPrompt();
    } catch (err) {
      if (isUnmounted || loadSeq !== systemPromptLoadSeq) return;
      systemPromptDisplay.textContent = '加载失败';
      toast.error('加载系统提示词失败: ' + (err instanceof Error ? err.message : String(err)));
    }
  }

  async function saveSystemPrompt() {
    if (systemPromptSaving) return;
    const value = systemPromptInput.value;
    systemPromptSaving = true;
    refreshBtn.disabled = true;
    renderSystemPrompt();
    try {
      const result = await api.updateEnvVar({ key: 'SYSTEM_PROMPT', value, isSecret: false });
      if (isUnmounted) return;
      if (!result.success) {
        toast.error('保存系统提示词失败: ' + result.error);
        return;
      }
      systemPrompt = value;
      systemPromptEditing = false;
      toast.success('系统提示词已保存并生效');
    } catch (err) {
      if (!isUnmounted) {
        toast.error('保存系统提示词失败: ' + (err instanceof Error ? err.message : String(err)));
      }
    } finally {
      systemPromptSaving = false;
      refreshBtn.disabled = identitySaving;
      if (!isUnmounted) renderSystemPrompt();
    }
  }

  async function loadData() {
    if (isUnmounted) return;
    loading = true;
    try {
      const resp = await api.listDialogues();
      if (isUnmounted) return;
      dialogues = resp.dialogues || [];
      promptPreview = resp.promptPreview || '（无提示词预览）';
      filePath = resp.filePath || '当前实例 · 数据库存储';
      filepathEl.textContent = filePath;
      promptPreviewContent.textContent = promptPreview;
    } catch (err) {
      if (isUnmounted) return;
      toast.error('加载示例对话失败: ' + (err instanceof Error ? err.message : String(err)));
      dialogues = [];
      promptPreviewContent.textContent = '加载失败';
    } finally {
      if (!isUnmounted) {
        loading = false;
        renderVisual();
      }
    }
  }

  function renderVisual() {
    const query = searchQuery.trim().toLowerCase();
    const filtered = dialogues.filter((item) => {
      if (!query) return true;
      return (
        item.user.toLowerCase().includes(query) ||
        item.preferred.toLowerCase().includes(query) ||
        item.id.toLowerCase().includes(query)
      );
    });

    if (loading && dialogues.length === 0) {
      cardsContainer.innerHTML = `
        <div class="card p-6 text-center text-muted">
          <span class="spinner"></span>
          <span style="margin-left: 0.5rem;">加载中...</span>
        </div>
      `;
      return;
    }

    if (filtered.length === 0) {
      cardsContainer.innerHTML = `
        <div class="card p-8 text-center text-muted">
          暂无匹配的示例对话。
        </div>
      `;
      return;
    }

    cardsContainer.innerHTML = filtered
      .map((item) => {
        const originalIndex = dialogues.findIndex((d) => d.id === item.id);
        const isFirst = originalIndex === 0;
        const isLast = originalIndex === dialogues.length - 1;

        return `
          <article class="card dialogue-card p-4 flex flex-col gap-3">
            <header class="flex items-center justify-between gap-2 border-b border-border pb-2.5 flex-wrap">
              <div class="flex items-center gap-2">
                <span class="badge badge-outline font-mono text-xs">#${escapeHtml(item.id)}</span>
              </div>
              <div class="flex items-center gap-1">
                <button class="btn btn-ghost btn-icon-sm" data-action="move-up" data-index="${originalIndex}" ${isFirst ? 'disabled' : ''} title="上移">
                  ${icon('arrow_up', 'w-3.5 h-3.5')}
                </button>
                <button class="btn btn-ghost btn-icon-sm" data-action="move-down" data-index="${originalIndex}" ${isLast ? 'disabled' : ''} title="下移">
                  ${icon('arrow_down', 'w-3.5 h-3.5')}
                </button>
                <button class="btn btn-ghost btn-icon-sm" data-action="edit-dialogue" data-id="${escapeHtml(item.id)}" title="编辑">
                  ${icon('pencil', 'w-3.5 h-3.5')}
                </button>
                <button class="btn btn-ghost btn-icon-sm text-destructive" data-action="delete-dialogue" data-id="${escapeHtml(item.id)}" title="删除">
                  ${icon('trash', 'w-3.5 h-3.5')}
                </button>
              </div>
            </header>

            <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div class="card p-3.5 bg-muted text-xs leading-relaxed flex flex-col gap-1.5">
                <div class="flex items-center gap-1.5 font-medium text-muted">
                  ${icon('user', 'w-3.5 h-3.5')}
                  <span>用户输入 (User)</span>
                </div>
                <div class="font-mono whitespace-pre-wrap select-text mt-0.5 text-foreground">${escapeHtml(item.user)}</div>
              </div>

              <div class="card p-3.5 bg-muted text-xs leading-relaxed flex flex-col gap-1.5 border-primary/20">
                <div class="flex items-center gap-1.5 font-medium text-foreground">
                  ${icon('bot', 'w-3.5 h-3.5 text-primary')}
                  <span>期望回复 (Assistant / Preferred)</span>
                </div>
                <div class="font-mono whitespace-pre-wrap select-text mt-0.5 text-foreground">${escapeHtml(item.preferred)}</div>
              </div>
            </div>
          </article>
        `;
      })
      .join('');

    // Attach card event listeners
    cardsContainer.querySelectorAll<HTMLButtonElement>('[data-action="move-up"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const idx = parseInt(btn.dataset.index || '-1', 10);
        if (idx > 0) void moveDialogue(idx, idx - 1);
      });
    });

    cardsContainer.querySelectorAll<HTMLButtonElement>('[data-action="move-down"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const idx = parseInt(btn.dataset.index || '-1', 10);
        if (idx >= 0 && idx < dialogues.length - 1) void moveDialogue(idx, idx + 1);
      });
    });

    cardsContainer.querySelectorAll<HTMLButtonElement>('[data-action="edit-dialogue"]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const id = btn.dataset.id;
        const item = dialogues.find((d) => d.id === id);
        if (item) openEditModal(item);
      });
    });

    cardsContainer.querySelectorAll<HTMLButtonElement>('[data-action="delete-dialogue"]').forEach((btn) => {
      btn.addEventListener('click', async () => {
        const id = btn.dataset.id;
        if (!id) return;
        const confirmed = await confirmDialog({
          title: '删除示例对话',
          message: `确定要删除编号为 #${id} 的示例对话吗？`,
          confirmLabel: '删除',
          destructive: true,
        });
        if (confirmed) {
          const newList = dialogues.filter((d) => d.id !== id);
          await saveList(newList, '示例对话已删除');
        }
      });
    });
  }

  async function moveDialogue(fromIdx: number, toIdx: number) {
    const list = [...dialogues];
    const item = list[fromIdx];
    list[fromIdx] = list[toIdx];
    list[toIdx] = item;
    await saveList(list, '顺序已调整');
  }

  async function saveList(newList: DialogueItem[], successMsg: string) {
    try {
      const resp = await api.saveDialogues(newList);
      if (resp.success) {
        dialogues = newList;
        if (resp.promptPreview) {
          promptPreview = resp.promptPreview;
          promptPreviewContent.textContent = promptPreview;
        }
        toast.success(successMsg);
        renderVisual();
      } else {
        toast.error('保存失败: ' + resp.error);
      }
    } catch (err) {
      toast.error('保存失败: ' + (err instanceof Error ? err.message : String(err)));
    }
  }

  function openEditModal(item?: DialogueItem) {
    const isEdit = Boolean(item);
    let nextId = '1';
    if (!isEdit) {
      let maxId = 0;
      for (const d of dialogues) {
        const num = parseInt(d.id, 10);
        if (!isNaN(num) && num > maxId) maxId = num;
      }
      nextId = String(maxId + 1);
    }

    const currentId = item ? item.id : nextId;
    const currentUser = item ? item.user : '';
    const currentPreferred = item ? item.preferred : '';

    openDialog({
      title: isEdit ? '编辑示例对话' : '添加示例对话',
      maxWidth: '36rem',
      bodyHtml: `
        <div class="flex flex-col gap-3.5">
          <div class="form-group">
            <label class="form-label" for="dlg-id">编号 (ID)</label>
            <input id="dlg-id" class="input text-xs" value="${escapeHtml(currentId)}" placeholder="如 1, 2" />
          </div>

          <div class="form-group">
            <label class="form-label" for="dlg-user">用户输入 (User) <span class="text-destructive">*</span></label>
            <textarea id="dlg-user" class="textarea text-xs" rows="3" placeholder="输入用户的提问或触发语句...">${escapeHtml(currentUser)}</textarea>
          </div>

          <div class="form-group">
            <label class="form-label" for="dlg-preferred">期望回复 (Preferred / Assistant) <span class="text-destructive">*</span></label>
            <textarea id="dlg-preferred" class="textarea text-xs" rows="4" placeholder="输入智能体人设期望的回复示范...">${escapeHtml(currentPreferred)}</textarea>
          </div>
        </div>
      `,
      footerHtml: `
        <button class="btn btn-outline btn-sm" id="dlg-cancel-btn">取消</button>
        <button class="btn btn-primary btn-sm" id="dlg-save-btn">保存</button>
      `,
      onMount: (dialogEl, close) => {
        const idInput = dialogEl.querySelector<HTMLInputElement>('#dlg-id')!;
        const userInput = dialogEl.querySelector<HTMLTextAreaElement>('#dlg-user')!;
        const preferredInput = dialogEl.querySelector<HTMLTextAreaElement>('#dlg-preferred')!;
        const saveBtn = dialogEl.querySelector<HTMLButtonElement>('#dlg-save-btn')!;
        const cancelBtn = dialogEl.querySelector<HTMLButtonElement>('#dlg-cancel-btn')!;

        cancelBtn.addEventListener('click', () => close());
        saveBtn.addEventListener('click', async () => {
          const userVal = userInput.value.trim();
          const prefVal = preferredInput.value.trim();
          if (!userVal || !prefVal) {
            toast.error('用户输入和期望回复均不能为空');
            return;
          }

          const newItem: DialogueItem = {
            id: idInput.value.trim() || '1',
            user: userVal,
            preferred: prefVal,
          } as DialogueItem;

          let updatedList: DialogueItem[];
          if (isEdit && item) {
            const idx = dialogues.findIndex((d) => d.id === item.id);
            updatedList = [...dialogues];
            if (idx >= 0) updatedList[idx] = newItem;
            else updatedList.push(newItem);
          } else {
            updatedList = [...dialogues, newItem];
          }

          close();
          await saveList(updatedList, isEdit ? '示例对话已更新' : '示例对话添加成功');
        });
      },
    });
  }

  // Raw YAML handlers
  async function loadRawYaml() {
    if (rawLoading) return;
    rawLoading = true;
    rawReloadBtn.disabled = true;
    try {
      const resp = await api.getRawDialogueFile();
      rawYaml = resp.content || '';
      rawTextarea.value = rawYaml;
    } catch (err) {
      toast.error('加载 YAML 失败: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      rawLoading = false;
      rawReloadBtn.disabled = false;
    }
  }

  async function saveRawYaml() {
    if (rawSaving) return;
    rawSaving = true;
    rawSaveBtn.disabled = true;
    try {
      const content = rawTextarea.value;
      const resp = await api.updateRawDialogueFile(content);
      if (resp.success) {
        promptPreview = resp.promptPreview || '（无提示词预览）';
        promptPreviewContent.textContent = promptPreview;
        toast.success('YAML 保存成功并已实时生效！');
        void loadData();
      } else {
        toast.error('保存失败: ' + resp.error);
      }
    } catch (err) {
      toast.error('保存失败: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      rawSaving = false;
      rawSaveBtn.disabled = false;
    }
  }

  function setMode(mode: 'visual' | 'raw') {
    viewMode = mode;
    if (mode === 'visual') {
      modeVisualBtn.classList.add('active');
      modeRawBtn.classList.remove('active');
      visualContainer.style.display = 'flex';
      rawContainer.style.display = 'none';
      renderVisual();
    } else {
      modeVisualBtn.classList.remove('active');
      modeRawBtn.classList.add('active');
      visualContainer.style.display = 'none';
      rawContainer.style.display = 'flex';
      void loadRawYaml();
    }
  }

  // Event handlers
  systemPromptEditBtn.addEventListener('click', () => {
    if (!systemPromptLoaded) return;
    systemPromptInput.value = systemPrompt;
    systemPromptEditing = true;
    renderSystemPrompt();
    systemPromptInput.focus();
  });
  systemPromptCancelBtn.addEventListener('click', () => {
    systemPromptEditing = false;
    renderSystemPrompt();
  });
  systemPromptSaveBtn.addEventListener('click', () => void saveSystemPrompt());
  systemPromptInput.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      systemPromptCancelBtn.click();
    } else if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
      event.preventDefault();
      systemPromptSaveBtn.click();
    }
  });
  modeVisualBtn.addEventListener('click', () => setMode('visual'));
  modeRawBtn.addEventListener('click', () => setMode('raw'));

  copyPromptBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    if (!promptPreview) return;
    navigator.clipboard.writeText(promptPreview).then(
      () => toast.success('提示词片段已复制到剪贴板'),
      () => toast.error('复制失败，请手动选择文本复制'),
    );
  });

  searchInput.addEventListener('input', () => {
    searchQuery = searchInput.value;
    renderVisual();
  });

  addBtn.addEventListener('click', () => openEditModal());
  rawReloadBtn.addEventListener('click', () => void loadRawYaml());
  rawSaveBtn.addEventListener('click', () => void saveRawYaml());

  exportBtn.addEventListener('click', async () => {
    try {
      const resp = await api.getRawDialogueFile();
      const content = resp.content;
      const blob = new Blob([content], { type: 'text/yaml;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `dialogue-examples-${new Date().toISOString().slice(0, 10)}.yml`;
      a.click();
      URL.revokeObjectURL(url);
      toast.success('已导出示例对话文件');
    } catch (err) {
      toast.error('导出失败: ' + (err instanceof Error ? err.message : String(err)));
    }
  });

  importInput.addEventListener('change', (e) => {
    const input = e.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;

    const reader = new FileReader();
    reader.onload = async () => {
      try {
        const content = reader.result as string;
        const resp = await api.updateRawDialogueFile(content);
        if (resp.success) {
          toast.success('导入 YAML 成功并已实时生效');
          void loadData();
          if (viewMode === 'raw') void loadRawYaml();
        } else {
          toast.error('导入失败: ' + resp.error);
        }
      } catch (err) {
        toast.error('导入解析失败: ' + (err instanceof Error ? err.message : String(err)));
      }
    };
    reader.readAsText(file);
    input.value = '';
  });

  refreshBtn.addEventListener('click', () => {
    void loadData();
    if (!systemPromptEditing) void loadSystemPrompt();
    if (viewMode === 'raw') void loadRawYaml();
  });

  void loadData();
  void loadSystemPrompt();

  return () => {
    isUnmounted = true;
    identityController.abort();
    systemPromptLoadSeq++;
  };
}
