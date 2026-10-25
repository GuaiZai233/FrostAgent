import {
  createComputerUseAPI,
  type ComputerConfigUpdate,
  type ComputerProject,
  type ComputerSnapshot,
} from '../api/computer-use';
import { escapeHtml } from '../utils/formatters';
import {
  parseProjectEnvironment,
  taskStateLabel,
  validatedPreview,
  withTokenUpdate,
} from './computer-use-model';

const restoredTaskNotice =
  '仅供核对的恢复记录，配置桥/项目后新建任务；不会重放旧操作。';

export function mountComputerUsePage(
  container: HTMLElement,
  api = createComputerUseAPI(),
): () => void {
  const lifecycle = new AbortController();
  let snapshot: ComputerSnapshot | undefined;
  let busy = false;
  let takeoverBusy = false;
  let desktopActionGeneration = 0;
  let confirmedTakeoverGeneration: number | undefined;
  let editingProject: ComputerProject | undefined;
  let resumingTask = '';
  let snapshotSequence = 0;
  let pollTimer: ReturnType<typeof setTimeout> | undefined;
  let contentTimer: ReturnType<typeof setTimeout> | undefined;

  container.innerHTML = `
    <div class="page-container fade-in computer-use-page">
      <header class="flex items-center justify-between gap-4 flex-wrap">
        <h1 class="page-title">Computer Use</h1>
        <div class="flex gap-2 flex-wrap">
          <button class="btn btn-outline btn-sm" id="cu-refresh">刷新状态</button>
          <button class="btn btn-outline btn-sm" id="cu-sync">连接 VM 同步项目</button>
          <button class="btn btn-outline btn-sm" id="cu-takeover">暂停整机桌面 / 人工接管</button>
          <button class="btn btn-outline btn-sm" id="cu-desktop-resume">恢复 Bot 桌面权限</button>
        </div>
      </header>
      <section class="card p-4 cu-notice" aria-label="权限与数据流">
        <h2 class="font-semibold">仅在受信任的专用 Windows VM 中启用</h2>
        <p>这是整机权限委托，不是沙箱。Bot 可使用 VM 上的工具和 Git 凭据；worktree 只隔离工作文件。</p>
        <p>仅主人 QQ 私聊和本管理工作台可发起任务。群聊、主动回复及「直接对话」模拟聊天不可操作。</p>
        <p>代码、终端输出和截图可能发送给配置的云模型供应商；私网桥不代表内容始终留在 VM。</p>
        <p id="cu-disclosure" class="break-words"></p>
        <p id="cu-model" class="break-words"></p>
        <p id="cu-desktop-state" class="break-words"></p>
      </section>
      <p id="cu-feedback" class="text-sm break-words" role="status" aria-live="polite">正在读取本地状态…</p>
      <details class="card p-4" open>
        <summary class="font-semibold">桥连接与任务预算</summary>
        <form id="cu-config-form" class="mt-4">
          <fieldset id="cu-config-fields" class="cu-fields">
            <label class="cu-wide flex items-center gap-2"><input id="cu-enabled" type="checkbox">启用 Computer Use</label>
            <label class="cu-field cu-wide">私网桥地址
              <input class="input" id="cu-bridge-url" type="url" placeholder="https://192.168.50.20:9443" autocomplete="off">
            </label>
            <label class="cu-field">桥令牌
              <input class="input" id="cu-token" type="password" autocomplete="new-password" placeholder="留空保留已配置令牌">
              <span id="cu-token-status" class="text-xs text-muted"></span>
            </label>
            <label class="flex items-center gap-2"><input id="cu-clear-token" type="checkbox">清除已配置桥令牌</label>
            <label class="cu-field">可信适配器令牌
              <input class="input" id="cu-adapter-token" type="password" autocomplete="new-password" placeholder="留空保留已配置令牌">
              <span id="cu-adapter-token-status" class="text-xs text-muted"></span>
            </label>
            <label class="flex items-center gap-2"><input id="cu-clear-adapter-token" type="checkbox">清除可信适配器令牌</label>
            <p class="cu-wide text-xs text-muted">与桥令牌不同。OneBot / AstrBot 反向连接须使用 WSS（本机回环可用 WS），在握手中使用 Authorization: Bearer 可信适配器令牌；未配置时仅本工作台可发起 Computer Use，普通聊天不受影响。</p>
            <label class="cu-field cu-wide">私有 CA（PEM，可留空使用系统信任）
              <textarea class="textarea font-mono" id="cu-ca" rows="3" spellcheck="false"></textarea>
            </label>
            <label class="cu-field">并发任务上限<input class="input" id="cu-concurrent" type="number" min="1" max="64" required></label>
            <label class="cu-field">每任务时间预算（秒）<input class="input" id="cu-seconds" type="number" min="1" max="86400" required></label>
            <label class="cu-field">模型轮数上限<input class="input" id="cu-rounds" type="number" min="1" required></label>
            <label class="cu-field">工具调用上限<input class="input" id="cu-tools" type="number" min="1" required></label>
            <label class="cu-field">审计保留天数<input class="input" id="cu-retention" type="number" min="1" max="3650" required></label>
            <p class="cu-wide text-xs text-muted">预算耗尽后停止派发新操作，保留文件；不代表已有进程已经退出。审计默认保留当天及此前六个 UTC 日历日。</p>
            <div class="cu-wide flex gap-2"><button class="btn btn-primary btn-sm" type="submit">保存配置</button></div>
          </fieldset>
        </form>
      </details>
      <section class="card p-4">
        <div class="flex items-center justify-between gap-3 flex-wrap">
          <h2 class="font-semibold">项目与工作区</h2>
          <label class="flex items-center gap-2 text-sm"><input id="cu-show-archived" type="checkbox">显示归档</label>
        </div>
        <p class="text-xs text-muted mt-2">主项目须在 VM 项目根目录内且包含 .git 目录；任务始终使用独立 worktree，不复制主仓库未提交修改。归档仅隐藏记录，不删除代码。</p>
        <div id="cu-projects" class="cu-list mt-4"></div>
        <details class="mt-4" id="cu-project-editor">
          <summary class="font-semibold">注册 / 更新项目</summary>
          <form id="cu-project-form" class="mt-4">
            <fieldset id="cu-project-fields" class="cu-fields">
              <label class="cu-field">项目 ID<input class="input" id="cu-project-id" required pattern="[A-Za-z0-9_-]+" maxlength="128" autocomplete="off"></label>
              <label class="cu-field">VM 主仓库绝对路径<input class="input" id="cu-project-path" required placeholder="C:\\Projects\\example" autocomplete="off"></label>
              <label class="cu-field">远端 Git 仓库（可选）<input class="input" id="cu-project-remote" placeholder="使用 VM 本地 Git 凭据，不在 URL 中填写令牌" autocomplete="off"></label>
              <label class="cu-field">基准分支 / 提交<input class="input" id="cu-project-baseline" required placeholder="main" autocomplete="off"></label>
              <label class="cu-field cu-wide">PowerShell 可执行文件绝对路径<input class="input" id="cu-project-shell" required placeholder="C:\\Program Files\\PowerShell\\7\\pwsh.exe" autocomplete="off"></label>
              <label class="cu-field cu-wide">工具链环境变量（JSON）
                <textarea class="textarea font-mono" id="cu-project-env" rows="3" spellcheck="false" placeholder="{}"></textarea>
              </label>
              <p class="cu-wide text-xs text-muted">版本由实际可执行路径和环境固定；修改默认值只影响未来任务。不要在这里保存密码、令牌或私钥。</p>
              <div class="cu-wide flex gap-2"><button class="btn btn-primary btn-sm" type="submit">保存项目</button><button class="btn btn-ghost btn-sm" id="cu-project-new" type="button">清空表单</button></div>
            </fieldset>
          </form>
        </details>
      </section>
      <section class="card p-4">
        <h2 class="font-semibold" id="cu-task-form-title">新建独立任务</h2>
        <form id="cu-task-form" class="mt-4">
          <fieldset id="cu-task-fields" class="cu-fields">
            <label class="cu-field cu-wide">项目<select class="select" id="cu-task-project" required></select></label>
            <label class="cu-field cu-wide">任务指令<textarea class="textarea" id="cu-instruction" rows="4" required maxlength="32768" placeholder="只描述本次授权任务。不会自动带入普通聊天或群记忆。"></textarea></label>
            <label class="cu-wide flex items-center gap-2" id="cu-unknown-confirm" hidden><input id="cu-acknowledge-unknown" type="checkbox">已人工核对未知操作，继续不会重放旧操作</label>
            <p class="cu-wide text-xs text-muted">默认保留本地结果。任务指令或 AGENTS.md 要求时可提交 / 推送。重启后不自动续跑，继续任务需重新明确指令。</p>
            <div class="cu-wide flex gap-2"><button class="btn btn-primary btn-sm" id="cu-start" type="submit">开始任务</button><button class="btn btn-ghost btn-sm" id="cu-cancel-resume" type="button" hidden>取消继续</button></div>
          </fieldset>
        </form>
      </section>
      <section class="card p-4">
        <h2 class="font-semibold">任务</h2>
        <p class="text-xs text-muted mt-2">暂停单个任务不能保证其他任务停止输入。人工接管请使用页首“暂停整机桌面”，并等待桥确认。暂停不表示终端进程已结束。</p>
        <div id="cu-tasks" class="cu-list mt-4"></div>
      </section>
      <section id="cu-content-panel" class="card p-4" hidden>
        <div class="flex items-center justify-between gap-3"><h2 class="font-semibold">临时输出 / 截图</h2><button class="btn btn-ghost btn-sm" id="cu-content-close">清除预览</button></div>
        <p id="cu-content-status" class="text-xs text-muted mt-2"></p>
        <pre id="cu-content-text" class="cu-output mt-3"></pre>
        <img id="cu-content-image" class="cu-screenshot mt-3" alt="专用 VM 的临时桌面截图" hidden>
      </section>
    </div>`;

  const el = <T extends HTMLElement>(id: string) =>
    container.querySelector<T>('#' + id)!;
  const input = (id: string) => el<HTMLInputElement>(id);
  const feedback = (text: string, error = false) => {
    if (lifecycle.signal.aborted) return;
    el('cu-feedback').textContent = text;
    el('cu-feedback').classList.toggle('text-danger', error);
  };
  const on = (id: string, event: string, action: (event: Event) => void) =>
    el(id).addEventListener(event, action, { signal: lifecycle.signal });

  function controls() {
    if (lifecycle.signal.aborted) return;
    for (const id of [
      'cu-config-fields',
      'cu-project-fields',
      'cu-task-fields',
    ])
      el<HTMLFieldSetElement>(id).disabled = busy || !snapshot;
    input('cu-refresh').disabled = busy;
    input('cu-sync').disabled = busy || !snapshot?.config.enabled;
    input('cu-takeover').disabled = takeoverBusy || !snapshot;
    input('cu-desktop-resume').disabled = busy || !snapshot?.desktop_paused;
    input('cu-start').disabled =
      busy ||
      !snapshot?.config.enabled ||
      (!!resumingTask &&
        !snapshot.tasks.find((task) => task.id === resumingTask)?.bridge_url);
    input('cu-token').disabled = input('cu-clear-token').checked;
    input('cu-adapter-token').disabled = input(
      'cu-clear-adapter-token',
    ).checked;
    input('cu-project-id').readOnly = !!editingProject;
    el<HTMLSelectElement>('cu-task-project').disabled = !!resumingTask;
    for (const button of container.querySelectorAll<HTMLButtonElement>(
      '[data-cu-action]',
    ))
      button.disabled = busy;
  }

  function fillConfiguration() {
    if (!snapshot) return;
    const config = snapshot.config;
    input('cu-enabled').checked = config.enabled;
    input('cu-bridge-url').value = config.bridge_url;
    input('cu-token').value = '';
    input('cu-adapter-token').value = '';
    input('cu-clear-token').checked = false;
    input('cu-clear-adapter-token').checked = false;
    el<HTMLTextAreaElement>('cu-ca').value = config.ca_pem;
    input('cu-concurrent').value = String(config.max_concurrent);
    input('cu-seconds').value = String(config.max_seconds);
    input('cu-rounds').value = String(config.max_model_rounds);
    input('cu-tools').value = String(config.max_tool_calls);
    input('cu-retention').value = String(config.audit_retention_days);
  }

  function render() {
    if (!snapshot || lifecycle.signal.aborted) return;
    el('cu-token-status').textContent = snapshot.config.token_configured
      ? '已配置；保存时留空不会修改'
      : '未配置';
    el('cu-adapter-token-status').textContent = snapshot.config
      .adapter_token_configured
      ? '已配置；保存时留空不会修改'
      : '未配置；仅管理工作台可发起 Computer Use';
    el('cu-disclosure').textContent = snapshot.disclosure || '';
    el('cu-model').textContent = '当前模型：' + (snapshot.model || '未配置');
    el('cu-desktop-state').textContent = !snapshot.desktop_confirmed
      ? '桌面状态尚未得到当前桥确认；缓存记录不能作为接管依据。'
      : snapshot.desktop_paused
        ? '最近桥确认记录：整机桌面自动输入已锁定。人工操作前请重新请求接管确认；恢复桌面权限不会自动继续任务。'
        : '最近桥确认记录：整机桌面未锁定。人工操作前须请求接管并等待停止确认。';
    if (
      confirmedTakeoverGeneration !== undefined &&
      (!snapshot.desktop_confirmed ||
        !snapshot.desktop_paused ||
        snapshot.desktop_generation !== confirmedTakeoverGeneration)
    ) {
      confirmedTakeoverGeneration = undefined;
      feedback(
        '先前的整机接管确认已失效，请勿继续依据旧确认操作；请重新核对桌面状态。',
        true,
      );
    }
    const projects = snapshot.projects.filter(
      (project) => !project.archived || input('cu-show-archived').checked,
    );
    el('cu-projects').innerHTML =
      projects
        .map(
          (project) => `<article class="cu-item">
            <div class="flex items-center justify-between gap-3 flex-wrap">
              <h3 class="font-semibold break-words">${escapeHtml(project.id)}${project.archived ? '（已归档）' : ''}</h3>
              <div class="flex gap-2"><button class="btn btn-outline btn-sm" data-cu-action="edit" data-id="${escapeHtml(project.id)}">编辑</button><button class="btn btn-ghost btn-sm" data-cu-action="archive" data-id="${escapeHtml(project.id)}">${project.archived ? '取消归档' : '归档'}</button></div>
            </div>
            <p class="text-sm break-words mt-2">${escapeHtml(project.path)}</p>
            <p class="text-xs text-muted break-words">基准：${escapeHtml(project.baseline)} · Shell：${escapeHtml(project.shell)}</p>
            ${project.remote ? `<p class="text-xs text-muted break-words">Remote：${escapeHtml(project.remote)}</p>` : ''}
            ${snapshot!.workspaces
              .filter((workspace) => workspace.project_id === project.id)
              .map(
                (workspace) => `<div class="cu-workspace">
                  <p class="text-sm break-words">子工作区 · ${escapeHtml(workspace.task_id)}${workspace.paused ? ' · 已暂停' : ''}</p>
                  <p class="text-xs text-muted break-words">${escapeHtml(workspace.path)}</p>
                  <p class="text-xs text-muted break-words">${escapeHtml(workspace.branch)} · ${escapeHtml(workspace.shell)}</p>
                </div>`,
              )
              .join('')}
          </article>`,
        )
        .join('') ||
      '<p class="text-sm text-muted">暂无可见项目。注册 VM 内的主仓库，或显式连接 VM 同步记录。</p>';
    const selectedProject = el<HTMLSelectElement>('cu-task-project').value;
    el('cu-task-project').innerHTML =
      '<option value="">选择项目</option>' +
      snapshot.projects
        .filter((project) => !project.archived)
        .map(
          (project) =>
            `<option value="${escapeHtml(project.id)}">${escapeHtml(project.id)}</option>`,
        )
        .join('');
    el<HTMLSelectElement>('cu-task-project').value = selectedProject;
    el('cu-tasks').innerHTML =
      snapshot.tasks
        .map(
          (task) => `<article class="cu-item">
            <div class="flex items-center justify-between gap-3 flex-wrap">
              <h3 class="font-semibold break-words">${escapeHtml(task.id)}</h3>
              <span class="badge badge-outline">${escapeHtml(taskStateLabel(task.state))}</span>
            </div>
            <p class="text-xs text-muted mt-2 break-words">项目 ${escapeHtml(task.project_id)} · Generation ${escapeHtml(String(task.generation))} · 模型轮数 ${escapeHtml(String(task.model_rounds))} · 工具调用 ${escapeHtml(String(task.tool_calls))}</p>
            <p class="text-sm break-words">${escapeHtml(task.reason || '')}</p>
            ${
              !task.bridge_url
                ? `<p class="text-sm mt-2">${restoredTaskNotice}</p>`
                : `<p class="text-xs mt-2">${task.gui_stopped ? '上次确认该任务 GUI 输入已停止；接管前请重新请求确认，终端进程状态须另行核对。' : '尚未确认 GUI 输入停止，请勿接管桌面。'}</p>
            <div class="flex gap-2 flex-wrap mt-3">
              <button class="btn btn-outline btn-sm" data-cu-action="pause" data-id="${escapeHtml(task.id)}">暂停任务</button>
              <button class="btn btn-outline btn-sm" data-cu-action="reconcile" data-id="${escapeHtml(task.id)}">核对状态</button>
              ${['paused', 'failed', 'unknown'].includes(task.state) ? `<button class="btn btn-outline btn-sm" data-cu-action="resume" data-id="${escapeHtml(task.id)}">明确指令后继续</button>` : ''}
              <button class="btn btn-ghost btn-sm" data-cu-action="content" data-id="${escapeHtml(task.id)}">查看临时内容</button>
            </div>`
            }
          </article>`,
        )
        .join('') || '<p class="text-sm text-muted">暂无任务。</p>';
    controls();
  }

  async function reload(fill = false) {
    const sequence = ++snapshotSequence;
    const result = await api.snapshot();
    if (lifecycle.signal.aborted || sequence !== snapshotSequence) return;
    snapshot = result;
    if (fill) fillConfiguration();
    render();
    return result;
  }

  async function perform(action: () => Promise<void>) {
    if (busy || lifecycle.signal.aborted) return;
    snapshotSequence++;
    busy = true;
    controls();
    try {
      await action();
    } catch (error) {
      feedback(
        (error instanceof Error ? error.message : '请求失败') +
          ' 请刷新并核对状态；页面不会自动重放操作。',
        true,
      );
    } finally {
      busy = false;
      controls();
    }
  }

  function clearContent(message = '') {
    clearTimeout(contentTimer);
    contentTimer = undefined;
    el('cu-content-text').textContent = '';
    const image = el<HTMLImageElement>('cu-content-image');
    image.removeAttribute('src');
    image.hidden = true;
    el('cu-content-status').textContent = message;
    el('cu-content-panel').hidden = !message;
  }

  function resetTaskForm() {
    resumingTask = '';
    el('cu-task-form-title').textContent = '新建独立任务';
    el('cu-start').textContent = '开始任务';
    el('cu-cancel-resume').hidden = true;
    el('cu-unknown-confirm').hidden = true;
    input('cu-acknowledge-unknown').checked = false;
    input('cu-acknowledge-unknown').required = false;
    el<HTMLTextAreaElement>('cu-instruction').value = '';
    controls();
  }

  on(
    'cu-refresh',
    'click',
    () =>
      void perform(async () => {
        await reload();
        feedback('已刷新本地状态；未向 VM 派发新操作。');
      }),
  );
  on(
    'cu-sync',
    'click',
    () =>
      void perform(async () => {
        await api.sync();
        if (lifecycle.signal.aborted) return;
        await reload();
        feedback('已同步 VM 项目与工作区记录。');
      }),
  );
  on('cu-show-archived', 'change', render);
  on('cu-takeover', 'click', () => {
    if (takeoverBusy || lifecycle.signal.aborted) return;
    takeoverBusy = true;
    confirmedTakeoverGeneration = undefined;
    const generation = ++desktopActionGeneration;
    snapshotSequence++;
    controls();
    feedback('正在请求停止整机 GUI 输入；确认前请勿接管桌面。');
    void (async () => {
      try {
        const result = await api.takeover();
        if (lifecycle.signal.aborted || generation !== desktopActionGeneration)
          return;
        const observed = await reload();
        if (generation !== desktopActionGeneration) return;
        const confirmed =
          result.gui_stopped &&
          result.desktop_paused &&
          observed?.desktop_confirmed &&
          observed.desktop_paused &&
          observed.desktop_generation === result.desktop_generation;
        if (confirmed) confirmedTakeoverGeneration = result.desktop_generation;
        feedback(
          confirmed
            ? '桥已确认整机 GUI 输入停止并锁定，可以人工接管。终端进程可能仍在运行；请另行核对。'
            : '尚未确认整机 GUI 输入停止并锁定，请勿接管桌面。请核对连接与状态。',
          !confirmed,
        );
      } catch {
        feedback(
          '整机接管尚未得到停止确认，请勿接管桌面。请核对桥连接后显式重试。',
          true,
        );
      } finally {
        takeoverBusy = false;
        controls();
      }
    })();
  });
  on(
    'cu-desktop-resume',
    'click',
    () =>
      void perform(async () => {
        const generation = ++desktopActionGeneration;
        confirmedTakeoverGeneration = undefined;
        const result = await api.resumeDesktop();
        if (lifecycle.signal.aborted) return;
        const observed = await reload();
        if (generation !== desktopActionGeneration) return;
        if (
          !observed?.desktop_confirmed ||
          observed.desktop_paused ||
          result.desktop_paused ||
          observed.desktop_generation !== result.desktop_generation
        ) {
          feedback(
            '桌面恢复结果尚未确认，或已被更新的控制请求替代；请核对当前桌面状态。',
            true,
          );
          return;
        }
        feedback(
          '已解除人工桌面锁；未自动继续任何任务。继续任务仍需明确指令。',
        );
      }),
  );
  on('cu-content-close', 'click', () => clearContent());
  on('cu-cancel-resume', 'click', resetTaskForm);
  on('cu-clear-token', 'change', () => {
    input('cu-token').disabled = input('cu-clear-token').checked;
    if (input('cu-clear-token').checked) input('cu-token').value = '';
  });
  on('cu-clear-adapter-token', 'change', () => {
    input('cu-adapter-token').disabled = input(
      'cu-clear-adapter-token',
    ).checked;
    if (input('cu-clear-adapter-token').checked)
      input('cu-adapter-token').value = '';
  });
  on('cu-config-form', 'submit', (event) => {
    event.preventDefault();
    if (!el<HTMLFormElement>('cu-config-form').reportValidity()) return;
    void perform(async () => {
      const config: ComputerConfigUpdate = {
        enabled: input('cu-enabled').checked,
        bridge_url: input('cu-bridge-url').value.trim(),
        ca_pem: el<HTMLTextAreaElement>('cu-ca').value.trim(),
        max_concurrent: Number(input('cu-concurrent').value),
        max_seconds: Number(input('cu-seconds').value),
        max_model_rounds: Number(input('cu-rounds').value),
        max_tool_calls: Number(input('cu-tools').value),
        audit_retention_days: Number(input('cu-retention').value),
      };
      if (config.bridge_url && new URL(config.bridge_url).protocol !== 'https:')
        throw new Error('桥地址必须使用 HTTPS');
      await api.configure(
        withTokenUpdate(
          config,
          input('cu-token').value,
          input('cu-clear-token').checked,
          input('cu-adapter-token').value,
          input('cu-clear-adapter-token').checked,
        ),
      );
      if (lifecycle.signal.aborted) return;
      input('cu-token').value = '';
      input('cu-adapter-token').value = '';
      await reload(true);
      feedback('配置已保存。重启或恢复连接不会自动继续旧任务。');
    });
  });
  on('cu-project-new', 'click', () => {
    editingProject = undefined;
    el<HTMLFormElement>('cu-project-form').reset();
    controls();
  });
  on('cu-project-form', 'submit', (event) => {
    event.preventDefault();
    if (!el<HTMLFormElement>('cu-project-form').reportValidity()) return;
    void perform(async () => {
      await api.registerProject({
        id: input('cu-project-id').value.trim(),
        path: input('cu-project-path').value.trim(),
        remote: input('cu-project-remote').value.trim(),
        baseline: input('cu-project-baseline').value.trim(),
        shell: input('cu-project-shell').value.trim(),
        environment: parseProjectEnvironment(
          el<HTMLTextAreaElement>('cu-project-env').value,
        ),
        archived: editingProject?.archived ?? false,
      });
      if (lifecycle.signal.aborted) return;
      editingProject = undefined;
      el<HTMLFormElement>('cu-project-form').reset();
      await reload();
      feedback('项目已保存；已有任务固定的工具链环境不会改变。');
    });
  });
  on('cu-task-form', 'submit', (event) => {
    event.preventDefault();
    if (
      resumingTask &&
      !snapshot?.tasks.find((task) => task.id === resumingTask)?.bridge_url
    ) {
      feedback(restoredTaskNotice, true);
      return;
    }
    if (!el<HTMLFormElement>('cu-task-form').reportValidity()) return;
    const instruction = el<HTMLTextAreaElement>('cu-instruction').value.trim();
    if (!instruction) {
      feedback('请填写本次任务的明确指令。', true);
      return;
    }
    void perform(async () => {
      if (resumingTask)
        await api.resume(
          resumingTask,
          instruction,
          input('cu-acknowledge-unknown').checked,
        );
      else
        await api.start(
          el<HTMLSelectElement>('cu-task-project').value,
          instruction,
        );
      if (lifecycle.signal.aborted) return;
      resetTaskForm();
      await reload();
      feedback('请求已接受。任务在独立会话中执行；可随时请求暂停。');
    });
  });

  container.addEventListener(
    'click',
    (event) => {
      const target = event.target as Element;
      const button = target.closest<HTMLButtonElement>(
        'button[data-cu-action]',
      );
      if (!button || button.disabled || busy || !snapshot) return;
      const id = button.dataset.id!;
      const action = button.dataset.cuAction;
      if (
        action === 'pause' ||
        action === 'resume' ||
        action === 'reconcile' ||
        action === 'content'
      ) {
        const task = snapshot.tasks.find((entry) => entry.id === id);
        if (!task) return;
        if (!task.bridge_url) {
          feedback(restoredTaskNotice, true);
          return;
        }
      }
      if (action === 'edit') {
        editingProject = snapshot.projects.find((project) => project.id === id);
        if (!editingProject) return;
        for (const key of [
          'id',
          'path',
          'remote',
          'baseline',
          'shell',
        ] as const)
          input('cu-project-' + key).value = editingProject[key];
        el<HTMLTextAreaElement>('cu-project-env').value = JSON.stringify(
          editingProject.environment || {},
          null,
          2,
        );
        el<HTMLDetailsElement>('cu-project-editor').open = true;
        controls();
        input('cu-project-path').focus();
        return;
      }
      if (action === 'resume') {
        const task = snapshot.tasks.find((entry) => entry.id === id);
        if (!task) return;
        resumingTask = id;
        el('cu-task-form-title').textContent = '继续任务：' + id;
        el('cu-start').textContent = '明确继续任务';
        el('cu-cancel-resume').hidden = false;
        el('cu-unknown-confirm').hidden = !task.reconcile_required;
        input('cu-acknowledge-unknown').checked = false;
        input('cu-acknowledge-unknown').required = task.reconcile_required;
        el<HTMLSelectElement>('cu-task-project').value = task.project_id;
        el<HTMLTextAreaElement>('cu-instruction').value = '';
        controls();
        el<HTMLTextAreaElement>('cu-instruction').focus();
        return;
      }
      void perform(async () => {
        if (action === 'archive') {
          const project = snapshot!.projects.find((entry) => entry.id === id);
          if (!project) return;
          await api.archiveProject(id, !project.archived);
          if (lifecycle.signal.aborted) return;
          await reload();
          feedback('归档状态已更新；VM 代码、worktree 和工具链未删除。');
        } else if (action === 'pause') {
          const task = await api.pause(id);
          if (lifecycle.signal.aborted) return;
          await reload();
          feedback(
            task.gui_stopped
              ? '桥已确认该任务 GUI 输入停止。其他任务可能仍在输入；人工接管请使用“暂停整机桌面”。终端进程状态另行核对。'
              : '已请求暂停，但尚未确认 GUI 输入停止。请勿接管桌面，继续刷新核对。',
          );
        } else if (action === 'reconcile') {
          await api.reconcile(id);
          if (lifecycle.signal.aborted) return;
          await reload();
          feedback(
            '已请求核对桥上的操作事实；不会重放旧操作，也不会自动继续任务。',
          );
        } else if (action === 'content') {
          clearContent();
          const content = await api.content(id);
          if (lifecycle.signal.aborted) return;
          const preview = validatedPreview(content);
          el('cu-content-text').textContent = preview.text;
          const image = el<HTMLImageElement>('cu-content-image');
          if (preview.image) {
            image.src = preview.image;
            image.hidden = false;
          }
          el('cu-content-status').textContent =
            '临时内容不会保存到浏览器存储。过期后不重放操作以重新获取内容。';
          el('cu-content-panel').hidden = false;
          contentTimer = setTimeout(
            () => clearContent('临时内容已过期，不会重新执行操作。'),
            Math.min(preview.expires - Date.now(), 15 * 60_000),
          );
        }
      });
    },
    { signal: lifecycle.signal },
  );

  function schedulePoll() {
    pollTimer = setTimeout(() => {
      if (lifecycle.signal.aborted) return;
      const poll = busy
        ? Promise.resolve()
        : reload().catch(() => {
            feedback(
              '状态暂时无法刷新；保留上次记录。请勿把旧状态视为接管确认。',
              true,
            );
          });
      void poll.finally(() => {
        if (!lifecycle.signal.aborted) schedulePoll();
      });
    }, 5000);
  }

  void perform(async () => {
    await reload(true);
    feedback('本地状态已加载。只有显式同步、保存项目或启动任务才会请求 VM。');
  });
  schedulePoll();
  return () => {
    lifecycle.abort();
    api.dispose();
    clearTimeout(pollTimer);
    clearContent();
    input('cu-token').value = '';
    input('cu-adapter-token').value = '';
    el<HTMLTextAreaElement>('cu-instruction').value = '';
  };
}
