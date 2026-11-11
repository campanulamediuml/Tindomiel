import type { TransferState } from './types';
import { element, formatSize } from './utils';

export interface UploadProgress {
  update(loaded: number, total: number): void;
  finish(error?: unknown): void;
}

interface TransferRow {
  root: HTMLDivElement;
  name: HTMLElement;
  status: HTMLSpanElement;
  progress: HTMLProgressElement;
  current: HTMLSpanElement;
  size: HTMLSpanElement;
}

export class TransferCenter {
  private readonly items = new Map<string, TransferState>();
  private readonly rows = new Map<string, TransferRow>();
  private readonly popover = element<HTMLElement>('transfer-popover');
  private readonly list = element<HTMLDivElement>('global-transfer-list');
  private readonly count = element<HTMLSpanElement>('transfer-count');
  private readonly empty = document.createElement('div');
  private animationFrame = 0;

  constructor() {
    element<HTMLButtonElement>('transfers-button').addEventListener('click', (event) => {
      event.stopPropagation();
      this.popover.hidden = !this.popover.hidden;
      if (!this.popover.hidden) this.flush();
    });
    this.popover.addEventListener('click', (event) => event.stopPropagation());
    document.addEventListener('click', () => { this.popover.hidden = true; });
    element<HTMLButtonElement>('clear-transfers').addEventListener('click', () => {
      for (const [id, item] of this.items) {
        if (item.status === 'running') continue;
        this.items.delete(id);
        this.rows.get(id)?.root.remove();
        this.rows.delete(id);
      }
      this.flush();
    });
    this.empty.className = 'empty-state';
    this.empty.textContent = '暂无传输任务';
    this.list.append(this.empty);
    this.render();
  }

  update(state: TransferState): void {
    this.items.set(state.id, state);
    if (this.items.size > 60) {
      const oldest = this.items.keys().next().value;
      if (oldest) {
        this.items.delete(oldest);
        this.rows.get(oldest)?.root.remove();
        this.rows.delete(oldest);
      }
    }
    this.scheduleRender();
  }

  beginUpload(name: string, remotePath: string, total: number): UploadProgress {
    const id = `upload-${crypto.randomUUID()}`;
    const state: TransferState = { id, name, direction: 'upload', path: remotePath, loaded: 0, total, status: 'running' };
    this.update(state);
    return {
      update: (loaded, updatedTotal) => {
        state.loaded = loaded;
        state.total = updatedTotal;
        this.scheduleRender();
      },
      finish: (error) => {
        state.status = error ? 'failed' : 'complete';
        state.loaded = error ? state.loaded : total;
        if (error) state.error = error instanceof Error ? error.message : String(error);
        this.flush();
      },
    };
  }

  private scheduleRender(): void {
    if (this.animationFrame) return;
    this.animationFrame = requestAnimationFrame(() => {
      this.animationFrame = 0;
      this.render();
    });
  }

  private flush(): void {
    cancelAnimationFrame(this.animationFrame);
    this.animationFrame = 0;
    this.render();
  }

  private render(): void {
    const running = Array.from(this.items.values()).filter((item) => item.status === 'running').length;
    this.count.hidden = running === 0;
    setText(this.count, String(running));
    if (this.popover.hidden) return;
    this.empty.hidden = this.items.size > 0;
    for (const item of this.items.values()) {
      let row = this.rows.get(item.id);
      if (!row) {
        row = createRow(item);
        this.rows.set(item.id, row);
        this.list.prepend(row.root);
      }
      updateRow(row, item);
    }
  }
}

function createRow(item: TransferState): TransferRow {
  const root = document.createElement('div');
  const title = document.createElement('div');
  title.className = 'transfer-title';
  const direction = document.createElement('span');
  direction.textContent = item.direction === 'upload' ? '↑' : '↓';
  const name = document.createElement('strong');
  const status = document.createElement('span');
  title.append(direction, name, status);
  const progress = document.createElement('progress');
  const details = document.createElement('small');
  details.className = 'transfer-details';
  const current = document.createElement('span');
  current.className = 'transfer-current';
  const size = document.createElement('span');
  details.append(current, size);
  root.append(title, progress, details);
  return { root, name, status, progress, current, size };
}

function updateRow(row: TransferRow, item: TransferState): void {
  row.root.className = `transfer-row ${item.status}`;
  setText(row.name, item.name);
  row.name.title = item.path;
  setText(row.status, item.status === 'running' ? `${Math.min(100, Math.round((item.loaded / Math.max(item.total, 1)) * 100))}%` : item.status === 'complete' ? '已完成' : '失败');
  row.progress.max = Math.max(item.total, 1);
  row.progress.value = item.total > 0 ? Math.min(item.loaded, item.total) : 0;
  const currentPath = item.currentPath || item.path;
  row.current.hidden = item.status !== 'running';
  setText(row.current, `当前：${currentPath}`);
  row.current.title = currentPath;
  setText(row.size, item.error ?? `${formatSize(item.loaded)} / ${item.total > 0 ? formatSize(item.total) : '未知'}`);
}

function setText(target: HTMLElement, value: string): void {
  if (target.textContent !== value) target.textContent = value;
}
