export type PageMountFn = (container: HTMLElement) => (() => void) | void;

export interface Route {
  path: string;
  title: string;
  mount: PageMountFn;
}

class Router {
  private routes = new Map<string, Route>();
  private currentCleanup: (() => void) | null = null;
  private currentPath = '';
  private container: HTMLElement | null = null;
  private onNavigateListeners = new Set<(path: string) => void>();

  init(container: HTMLElement): void {
    this.container = container;
    window.addEventListener('hashchange', () => this.handleHashChange());
    // Initial route
    this.handleHashChange();
  }

  register(path: string, title: string, mount: PageMountFn): void {
    this.routes.set(path, { path, title, mount });
  }

  navigate(path: string): void {
    window.location.hash = path.startsWith('/') ? `#${path}` : `#/${path}`;
  }

  getCurrentPath(): string {
    return this.currentPath;
  }

  onNavigate(listener: (path: string) => void): () => void {
    this.onNavigateListeners.add(listener);
    return () => this.onNavigateListeners.delete(listener);
  }

  private handleHashChange(): void {
    let hash = window.location.hash.slice(1);
    if (!hash || hash === '') {
      this.navigate('/overview');
      return;
    }

    if (!hash.startsWith('/')) {
      hash = `/${hash}`;
    }

    // Strip query parameters for route matching
    const [cleanPath] = hash.split('?');

    const route = this.routes.get(cleanPath) || this.routes.get('/overview');
    if (!route || !this.container) {
      return;
    }

    // Cleanup previous page
    if (this.currentCleanup) {
      try {
        this.currentCleanup();
      } catch (err) {
        console.error('Page cleanup error:', err);
      }
      this.currentCleanup = null;
    }

    this.currentPath = cleanPath;
    document.title = `${route.title} - FrostAgent`;

    // Clear and mount
    this.container.innerHTML = '';
    const cleanup = route.mount(this.container);
    if (typeof cleanup === 'function') {
      this.currentCleanup = cleanup;
    }

    for (const listener of this.onNavigateListeners) {
      listener(cleanPath);
    }
  }
}

export const router = new Router();
