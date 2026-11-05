import { getControlToken, setControlToken } from '../api/control-auth';
import { icon } from '../components/icons';
import { toast } from '../components/toast';

export function mountAccessCredentialsPage(container: HTMLElement): () => void {
  let unmounted = false;

  function render() {
    const currentToken = getControlToken();
    container.innerHTML = `
      <div class="page-container fade-in">
        <header class="flex items-center gap-2.5 pb-1">
          <a href="#/settings" class="btn btn-ghost btn-icon-sm" title="返回设置" style="text-decoration: none;">
            ${icon('arrow_left', 'w-4 h-4')}
          </a>
          <h1 class="page-title">访问凭据</h1>
        </header>

        <section class="card p-4 flex flex-col gap-4" style="max-width: 42rem;">
          <div>
            <h2 class="text-sm font-semibold text-foreground">API / 控制平面访问凭据</h2>
            <p class="text-xs text-muted mt-0.5">Docker 端口映射和远程管理需要控制令牌。Docker 首次部署请填写初始化时设置的令牌；已有部署填写服务端的管理控制令牌。</p>
            <p class="text-xs text-muted mt-1">凭据仅保存在当前浏览器，用于访问此站点的管理 API 和直接对话；此处不会修改服务端令牌。</p>
          </div>

          <div class="flex flex-col gap-2.5">
            <label for="control-token-input" class="text-xs font-medium">管理 Token</label>
            <div class="flex items-center gap-2">
              <input id="control-token-input" type="password" class="input text-xs flex-1 min-w-0"
                placeholder="输入管理 Token（留空清除）" autocomplete="off" spellcheck="false" />
              <button id="toggle-token-visibility" class="btn btn-secondary btn-sm" type="button" title="切换可见性" aria-label="显示或隐藏管理 Token" aria-pressed="false">
                ${icon('eye', 'w-3.5 h-3.5')}
              </button>
            </div>
            <div class="flex items-center justify-between gap-2 flex-wrap pt-1">
              <span id="control-token-status" class="text-xs text-muted" role="status">
                ${currentToken ? '<span class="text-emerald-500 font-medium">✓ 已配置 Token</span>' : '未设置 Token；Docker 端口映射访问仍需填写凭据'}
              </span>
              <div class="flex gap-2">
                ${currentToken ? '<button id="clear-token-btn" type="button" class="btn btn-ghost btn-sm text-destructive text-xs">清除</button>' : ''}
                <button id="save-token-btn" type="button" class="btn btn-primary btn-sm text-xs">保存凭据</button>
              </div>
            </div>
          </div>
        </section>
      </div>
    `;

    const tokenInput = container.querySelector<HTMLInputElement>('#control-token-input')!;
    tokenInput.value = currentToken;
    const toggleButton = container.querySelector<HTMLButtonElement>('#toggle-token-visibility')!;
    toggleButton.addEventListener('click', () => {
      if (unmounted) return;
      tokenInput.type = tokenInput.type === 'password' ? 'text' : 'password';
      toggleButton.setAttribute('aria-pressed', String(tokenInput.type === 'text'));
    });
    container.querySelector('#save-token-btn')!.addEventListener('click', () => {
      if (unmounted) return;
      setControlToken(tokenInput.value);
      toast.success('访问凭据已保存');
      render();
    });
    container.querySelector('#clear-token-btn')?.addEventListener('click', () => {
      if (unmounted) return;
      setControlToken('');
      toast.success('访问凭据已清除');
      render();
    });
  }

  render();
  return () => {
    unmounted = true;
    const tokenInput = container.querySelector<HTMLInputElement>('#control-token-input');
    if (tokenInput) {
      tokenInput.value = '';
      tokenInput.type = 'password';
    }
  };
}
