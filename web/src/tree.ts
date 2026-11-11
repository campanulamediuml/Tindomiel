import type { Api } from './api';
import type { FileEntry } from './types';
import { element, joinPath, messageOf } from './utils';

interface TreeNode {
  children?: FileEntry[];
  loading: boolean;
}

interface TreeSnapshot {
  expanded: Set<string>;
  nodes: Map<string, TreeNode>;
  currentPath: string;
}

export class RemoteTree {
  private sessionId?: string;
  private currentPath = '/';
  private expanded = new Set<string>(['/']);
  private nodes = new Map<string, TreeNode>();
  private readonly snapshots = new Map<string, TreeSnapshot>();
  private readonly container = element<HTMLDivElement>('remote-tree');

  constructor(private readonly api: Api, private readonly onSelect: (path: string) => void, private readonly onError: (message: string) => void) {
    element<HTMLButtonElement>('tree-refresh').addEventListener('click', () => void this.refresh());
  }

  removeSession(sessionId: string): void {
    this.snapshots.delete(sessionId);
  }

  async setSession(sessionId?: string): Promise<void> {
    if (this.sessionId) this.snapshots.set(this.sessionId, { expanded: this.expanded, nodes: this.nodes, currentPath: this.currentPath });
    this.sessionId = sessionId;
    const cached = sessionId ? this.snapshots.get(sessionId) : undefined;
    if (cached) {
      this.expanded = new Set(cached.expanded);
      this.nodes = new Map(cached.nodes);
      this.currentPath = cached.currentPath;
      this.container.replaceChildren();
      this.render();
      return;
    }
    this.expanded = new Set(['/']);
    this.nodes = new Map();
    this.currentPath = '/';
    this.container.replaceChildren();
    if (sessionId) await this.load('/');
    this.render();
  }

  setDirectory(path: string, entries: FileEntry[]): void {
    const node = this.nodes.get(path) ?? { loading: false };
    node.children = entries.filter((entry) => entry.type === 'directory');
    node.loading = false;
    this.nodes.set(path, node);
  }

  setCurrent(path: string): void {
    const sessionId = this.sessionId;
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
          if (this.sessionId === sessionId && this.currentPath === path) this.render();
        });
      }
    }
    this.render();
  }

  async refresh(): Promise<void> {
    this.nodes.clear();
    if (this.sessionId) {
      await Promise.all(Array.from(this.expanded, (path) => this.load(path)));
    }
    this.render();
  }

  private async load(path: string): Promise<void> {
    const sessionId = this.sessionId;
    if (!sessionId) return;
    const nodes = this.nodes;
    const node = nodes.get(path) ?? { loading: false };
    node.loading = true;
    nodes.set(path, node);
    try {
      const entries = await this.api.listFiles(sessionId, path);
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
