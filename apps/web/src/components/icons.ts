export function icon(name: string, className = ''): string {
  return `<span class="icon ${className}" aria-hidden="true">${name}</span>`;
}
