import type { Api } from './api';
import type { FileEntry } from './types';
import { element, joinPath, messageOf } from './utils';

interface TreeNode {
  children?: FileEntry[];
  loading: boolean;
}

export class RemoteTree {
  private sessionId?: string;
  private currentPath = '/';
  private readonly expanded = new Set<string>(['/']);
  private readonly nodes = new Map<string, TreeNode>();
  private readonly container = element<HTMLDivElement>('remote-tree');

  constructor(private readonly api: Api, private readonly onSelect: (path: string) => void, private readonly onError: (message: string) => void) {
    element<HTMLButtonElement>('tree-refresh').addEventListener('click', () => void this.refresh());
  }

  async setSession(sessionId?: string): Promise<void> {
    this.sessionId = sessionId;
    this.expanded.clear();
    this.expanded.add('/');
    this.nodes.clear();
    this.currentPath = '/';
    this.container.replaceChildren();
    if (sessionId) await this.load('/');
    this.render();
  }

  setCurrent(path: string): void {
    this.currentPath = path;
    const parts = path.split('/');
    let current = '';
    for (const part of parts) {
      if (!part) continue;
      current += `/${part}`;
      this.expanded.add(current);
      const node = this.nodes.get(current);
      if (!node?.children && !node?.loading) {
        void this.load(current).then(() => {
          if (this.currentPath === path) this.render();
        });
      }
    }
    this.render();
  }

  async refresh(): Promise<void> {
    this.nodes.clear();
    if (this.sessionId) {
      for (const path of this.expanded) await this.load(path);
    }
    this.render();
  }

  private async load(path: string): Promise<void> {
    if (!this.sessionId) return;
    const node = this.nodes.get(path) ?? { loading: false };
    node.loading = true;
    this.nodes.set(path, node);
    try {
      const entries = await this.api.listFiles(this.sessionId, path);
      node.children = entries.filter((entry) => entry.type === 'directory');
    } catch (error) {
      node.children = [];
      this.onError(`${path}：${messageOf(error)}`);
    } finally {
      node.loading = false;
    }
  }

  private render(): void {
    this.container.replaceChildren();
    if (!this.sessionId) {
      this.container.textContent = '连接后显示远程目录';
      return;
    }
    this.renderNode('/', '/', 0);
  }

  private renderNode(path: string, name: string, depth: number): void {
    const row = document.createElement('div');
    row.className = `tree-row ${this.currentPath === path ? 'selected' : ''}`;
    row.style.paddingLeft = `${6 + depth * 14}px`;
    const toggle = document.createElement('button');
    toggle.className = 'tree-toggle';
    toggle.textContent = this.expanded.has(path) ? '▾' : '▸';
    toggle.addEventListener('click', () => void this.toggle(path));
    const label = document.createElement('button');
    label.className = 'tree-label';
    label.textContent = name;
    label.title = path;
    label.addEventListener('click', () => this.onSelect(path));
    row.append(toggle, label);
    this.container.append(row);
    if (!this.expanded.has(path)) return;
    const node = this.nodes.get(path);
    if (node?.loading) {
      const loading = document.createElement('div');
      loading.className = 'tree-message';
      loading.style.paddingLeft = `${26 + depth * 14}px`;
      loading.textContent = '读取中…';
      this.container.append(loading);
      return;
    }
    for (const child of node?.children ?? []) this.renderNode(joinPath(path, child.name), child.name, depth + 1);
  }

  private async toggle(path: string): Promise<void> {
    if (this.expanded.has(path)) {
      this.expanded.delete(path);
    } else {
      this.expanded.add(path);
      if (!this.nodes.get(path)?.children) await this.load(path);
    }
    this.render();
  }
}
