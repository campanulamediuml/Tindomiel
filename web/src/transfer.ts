import type { TransferState } from './types';
import { element, formatSize } from './utils';

export interface UploadProgress {
  update(loaded: number, total: number): void;
  finish(error?: unknown): void;
}

export class TransferCenter {
  private readonly items = new Map<string, TransferState>();
  private readonly popover = element<HTMLElement>('transfer-popover');
  private readonly list = element<HTMLDivElement>('global-transfer-list');
  private readonly count = element<HTMLSpanElement>('transfer-count');

  constructor() {
    element<HTMLButtonElement>('transfers-button').addEventListener('click', (event) => {
      event.stopPropagation();
      this.popover.hidden = !this.popover.hidden;
    });
    this.popover.addEventListener('click', (event) => event.stopPropagation());
    document.addEventListener('click', () => { this.popover.hidden = true; });
    element<HTMLButtonElement>('clear-transfers').addEventListener('click', () => {
      for (const [id, item] of this.items) if (item.status !== 'running') this.items.delete(id);
      this.render();
    });
    this.render();
  }

  update(state: TransferState): void {
    this.items.set(state.id, state);
    if (this.items.size > 60) {
      const oldest = this.items.keys().next().value;
      if (oldest) this.items.delete(oldest);
    }
    this.render();
  }

  beginUpload(name: string, remotePath: string, total: number): UploadProgress {
    const id = `upload-${crypto.randomUUID()}`;
    const state: TransferState = { id, name, direction: 'upload', path: remotePath, loaded: 0, total, status: 'running' };
    this.update(state);
    return {
      update: (loaded, updatedTotal) => {
        state.loaded = loaded;
        state.total = updatedTotal;
        this.render();
      },
      finish: (error) => {
        state.status = error ? 'failed' : 'complete';
        state.loaded = error ? state.loaded : total;
        if (error) state.error = error instanceof Error ? error.message : String(error);
        this.render();
      },
    };
  }

  private render(): void {
    const running = Array.from(this.items.values()).filter((item) => item.status === 'running').length;
    this.count.hidden = running === 0;
    this.count.textContent = String(running);
    this.list.replaceChildren();
    if (this.items.size === 0) {
      this.list.textContent = '暂无传输任务';
      this.list.className = 'empty-state';
      return;
    }
    this.list.className = '';
    for (const item of Array.from(this.items.values()).reverse()) {
      const row = document.createElement('div');
      row.className = `transfer-row ${item.status}`;
      const title = document.createElement('div');
      title.className = 'transfer-title';
      const direction = document.createElement('span');
      direction.textContent = item.direction === 'upload' ? '↑' : '↓';
      const name = document.createElement('strong');
      name.textContent = item.name;
      name.title = item.path;
      const status = document.createElement('span');
      status.textContent = item.status === 'running' ? `${Math.min(100, Math.round((item.loaded / Math.max(item.total, 1)) * 100))}%` : item.status === 'complete' ? '已完成' : '失败';
      title.append(direction, name, status);
      const progress = document.createElement('progress');
      progress.max = Math.max(item.total, 1);
      progress.value = item.total > 0 ? Math.min(item.loaded, item.total) : 0;
      const details = document.createElement('small');
      details.className = 'transfer-details';
      if (item.status === 'running') {
        const current = document.createElement('span');
        const currentPath = item.currentPath || item.path;
        current.className = 'transfer-current';
        current.textContent = `当前：${currentPath}`;
        current.title = currentPath;
        details.append(current);
      }
      const size = document.createElement('span');
      size.textContent = item.error ?? `${formatSize(item.loaded)} / ${item.total > 0 ? formatSize(item.total) : '未知'}`;
      details.append(size);
      row.append(title, progress, details);
      this.list.append(row);
    }
  }
}
