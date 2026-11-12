import type { Api } from './api';
import type { FileEntry } from './types';
import { element, joinPath, messageOf } from './utils';

interface TreeNode {
  children?: FileEntry[];
  loading: boolean;
  request: number;
  error?: string;
}

interface TreeSnapshot {
  expanded: Set<string>;
  nodes: Map<string, TreeNode>;
  currentPath: string;
}

export class RemoteTree {
  private sessionId?: string;
  private state?: TreeSnapshot;
  private readonly snapshots = new Map<string, TreeSnapshot>();
  private readonly container = element<HTMLDivElement>('remote-tree');

  constructor(private readonly api: Api, private readonly onSelect: (path: string) => void, private readonly onError: (message: string) => void) {
    element<HTMLButtonElement>('tree-refresh').addEventListener('click', () => void this.refresh());
  }

  removeSession(sessionId: string): void {
    this.snapshots.delete(sessionId);
  }

  async setSession(sessionId?: string): Promise<void> {
    this.sessionId = sessionId;
    if (!sessionId) {
      this.state = undefined;
      this.render();
      return;
    }
    let state = this.snapshots.get(sessionId);
    const isNew = !state;
    if (!state) {
      state = { expanded: new Set(['/']), nodes: new Map(), currentPath: '/' };
      this.snapshots.set(sessionId, state);
    }
    this.state = state;
    this.render();
    if (isNew) {
      void this.load('/', sessionId, state).then(() => {
        if (this.sessionId === sessionId && this.state === state) this.render();
      });
    }
  }

  setDirectory(sessionId: string, path: string, entries: FileEntry[]): void {
    const state = this.snapshots.get(sessionId);
    if (!state) return;
    const node = state.nodes.get(path) ?? { loading: false, request: 0 };
    node.children = entries.filter((entry) => entry.type === 'directory');
    node.loading = false;
    node.error = undefined;
    node.request += 1;
    state.nodes.set(path, node);
  }

  setCurrent(path: string): void {
    if (!this.state) return;
    this.state.currentPath = path;
    this.render();
  }

  async refresh(): Promise<void> {
    const sessionId = this.sessionId;
    const state = this.state;
    if (!sessionId || !state) return;
    state.nodes.clear();
    this.render();
    await Promise.all(Array.from(state.expanded, (path) => this.load(path, sessionId, state)));
    if (this.sessionId === sessionId && this.state === state) this.render();
  }

  private async load(path: string, sessionId = this.sessionId, state = this.state): Promise<void> {
    if (!sessionId || !state) return;
    const node = state.nodes.get(path) ?? { loading: false, request: 0 };
    const request = node.request + 1;
    node.request = request;
    node.loading = true;
    node.error = undefined;
    state.nodes.set(path, node);
    const isCurrentRequest = () => this.snapshots.get(sessionId) === state
      && state.nodes.get(path) === node
      && node.request === request;
    try {
      const entries = await this.api.listFiles(sessionId, path);
      if (!isCurrentRequest()) return;
      node.children = entries.filter((entry) => entry.type === 'directory');
    } catch (error) {
      if (!isCurrentRequest()) return;
      node.error = messageOf(error);
      if (this.sessionId === sessionId && this.state === state) this.onError(`${path}：${node.error}`);
    } finally {
      if (isCurrentRequest()) node.loading = false;
    }
  }

  private render(): void {
    this.container.replaceChildren();
    if (!this.sessionId || !this.state) {
      this.container.textContent = '连接后显示远程目录';
      return;
    }
    this.renderNode('/', '/', 0);
  }

  private renderNode(path: string, name: string, depth: number): void {
    const state = this.state!;
    const node = state.nodes.get(path);
    const row = document.createElement('div');
    const unloaded = !node?.children;
    row.className = `tree-row ${state.currentPath === path ? 'selected' : ''} ${unloaded ? 'unloaded' : ''}`;
    row.style.paddingLeft = `${6 + depth * 14}px`;
    const toggle = document.createElement('button');
    toggle.className = 'tree-toggle';
    toggle.textContent = node?.loading ? '…' : state.expanded.has(path) ? '▾' : '▸';
    toggle.title = node?.loading ? '正在后台加载' : unloaded ? '点击加载子目录' : '展开或折叠';
    toggle.addEventListener('click', () => void this.toggle(path));
    const label = document.createElement('button');
    label.className = 'tree-label';
    label.textContent = name;
    label.title = path;
    label.addEventListener('click', () => this.onSelect(path));
    row.append(toggle, label);
    this.container.append(row);
    if (!state.expanded.has(path)) return;
    if (node?.loading || node?.error) {
      const message = document.createElement('div');
      message.className = 'tree-message';
      message.style.paddingLeft = `${26 + depth * 14}px`;
      message.textContent = node.loading ? '读取中…' : '加载失败，折叠后重新展开可重试';
      this.container.append(message);
      return;
    }
    for (const child of node?.children ?? []) this.renderNode(joinPath(path, child.name), child.name, depth + 1);
  }

  private async toggle(path: string): Promise<void> {
    const sessionId = this.sessionId;
    const state = this.state;
    if (!sessionId || !state) return;
    const expanding = !state.expanded.has(path);
    if (expanding) state.expanded.add(path);
    else state.expanded.delete(path);
    this.render();
    const node = state.nodes.get(path);
    if (expanding && !node?.children && !node?.loading) await this.load(path, sessionId, state);
    if (this.sessionId === sessionId && this.state === state) this.render();
  }
}
