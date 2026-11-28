import { getControlToken } from '../api/control-auth';

export function mountManagementAuthNotice(container: HTMLElement): () => void {
  let rejected = false;
  const render = () => {
    container.hidden = !rejected && Boolean(getControlToken());
    container.innerHTML = container.hidden ? '' : `
      <div class="card p-4 m-4" role="alert">
        <p class="font-semibold text-sm">${rejected ? '管理凭据未通过验证' : '请先设置管理访问凭据'}</p>
        <p class="text-xs text-muted mt-1">所有管理访问（包括本机浏览器）均需要管理 Token。请在访问凭据中填写服务端已有的 Token。</p>
        <p class="text-xs text-muted mt-1">服务端尚未配置 Token 时，请由部署者通过本机 frostctl 初始化，或设置 FROSTAGENT_BOOTSTRAP_CONTROL_TOKEN 后重启服务。</p>
        <a class="btn btn-primary btn-sm mt-2" href="#/settings/credentials">前往访问凭据</a>
      </div>`;
  };
  const required = () => { rejected = true; render(); };
  const changed = () => { rejected = false; render(); };
  window.addEventListener('management-auth-required', required);
  window.addEventListener('control-credentials-changed', changed);
  window.addEventListener('storage', changed);
  render();
  return () => {
    window.removeEventListener('management-auth-required', required);
    window.removeEventListener('control-credentials-changed', changed);
    window.removeEventListener('storage', changed);
  };
}
