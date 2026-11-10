import type { Api } from './api';
import { RemoteTree } from './tree';
import type { TransferCenter } from './transfer';
import type { FileEntry } from './types';
import { element, formatSize, joinPath, messageOf, parentPath } from './utils';

export class SftpPanel {
  private sessionId?: string;
  private currentPath = '/';
  private readonly paths = new Map<string, string>();
  private readonly selected = new Set<string>();
  private readonly fileTypes = new Map<string, FileEntry['type']>();
  private loadVersion = 0;
  private dragDepth = 0;
  private contextEntry?: FileEntry;
  private readonly tree: RemoteTree;

  private readonly pathInput = element<HTMLInputElement>('sftp-path');
  private readonly fileList = element<HTMLDivElement>('file-list');
  private readonly dropzone = element<HTMLDivElement>('sftp-dropzone');

  constructor(private readonly api: Api, private readonly transfers: TransferCenter, private readonly onError: (message: string) => void) {
    this.tree = new RemoteTree(api, (path) => void this.load(path), onError);
    element<HTMLButtonElement>('sftp-up').addEventListener('click', () => void this.load(parentPath(this.currentPath)));
    element<HTMLButtonElement>('sftp-refresh').addEventListener('click', () => void this.load());
    element<HTMLButtonElement>('copy-path').addEventListener('click', () => {
      void navigator.clipboard.writeText(this.currentPath).then(() => this.onError(`已复制 ${this.currentPath}`));
    });
    this.pathInput.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') void this.load(this.pathInput.value.trim() || '/');
    });
    element<HTMLButtonElement>('sftp-download').addEventListener('click', () => void this.downloadSelected());
    const uploadInput = element<HTMLInputElement>('upload-input');
    element<HTMLButtonElement>('sftp-upload').addEventListener('click', () => uploadInput.click());
    uploadInput.addEventListener('change', () => {
      const files = Array.from(uploadInput.files ?? []);
      if (files.length) void this.uploadFiles(files);
      uploadInput.value = '';
    });
    this.dropzone.addEventListener('dragenter', (event) => {
      event.preventDefault();
      this.dragDepth += 1;
      this.dropzone.classList.add('dragging');
    });
    this.dropzone.addEventListener('dragover', (event) => {
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
    });
    this.dropzone.addEventListener('dragleave', () => {
      this.dragDepth = Math.max(0, this.dragDepth - 1);
      if (this.dragDepth === 0) this.dropzone.classList.remove('dragging');
    });
    this.dropzone.addEventListener('drop', (event) => {
      event.preventDefault();
      this.dragDepth = 0;
      this.dropzone.classList.remove('dragging');
      const files = Array.from(event.dataTransfer?.files ?? []);
      if (files.length) void this.uploadFiles(files);
    });
    const menu = element<HTMLDivElement>('file-context-menu');
    document.addEventListener('click', () => { menu.hidden = true; });
    element<HTMLButtonElement>('context-open-default').addEventListener('click', () => {
      if (this.contextEntry) void this.open(this.contextEntry);
      menu.hidden = true;
    });
    element<HTMLButtonElement>('context-open-with').addEventListener('click', () => {
      const entry = this.contextEntry;
      menu.hidden = true;
      if (!entry) return;
      void this.api.chooseApplication().then((application) => {
        if (application) return this.open(entry, application);
        return undefined;
      }).catch((error) => this.onError(messageOf(error)));
    });
  }

  async setSession(sessionId?: string): Promise<void> {
    if (this.sessionId === sessionId) return;
    if (this.sessionId) this.paths.set(this.sessionId, this.currentPath);
    this.sessionId = sessionId;
    this.loadVersion += 1;
    this.selected.clear();
    this.fileList.replaceChildren();
    if (!sessionId) {
      this.currentPath = '/';
      this.pathInput.value = '/';
      await this.tree.setSession(undefined);
      return;
    }
    this.currentPath = this.paths.get(sessionId) ?? '.';
    await Promise.all([this.tree.setSession(sessionId), this.load(this.currentPath)]);
  }

  async load(remotePath = this.currentPath): Promise<void> {
    if (!this.sessionId) return;
    const sessionId = this.sessionId;
    const version = ++this.loadVersion;
    this.fileList.textContent = '读取中…';
    try {
      const absolute = await this.api.realpath(sessionId, remotePath);
      const entries = await this.api.listFiles(sessionId, absolute);
      if (this.sessionId !== sessionId || this.loadVersion !== version) return;
      this.currentPath = absolute;
      this.pathInput.value = absolute;
      this.selected.clear();
      this.render(entries);
      this.tree.setCurrent(absolute);
    } catch (error) {
      if (this.sessionId !== sessionId || this.loadVersion !== version) return;
      this.fileList.textContent = '目录读取失败';
      this.onError(messageOf(error));
    }
  }

  private render(entries: FileEntry[]): void {
    this.fileList.replaceChildren();
    this.fileTypes.clear();
    for (const entry of entries) this.fileTypes.set(entry.name, entry.type);
    this.updateSelection();
    if (entries.length === 0) {
      this.fileList.textContent = '空目录';
      return;
    }
    for (const entry of entries) {
      const row = document.createElement('div');
      row.className = 'file-row';
      row.tabIndex = 0;
      row.dataset.name = entry.name;
      const name = document.createElement('span');
      name.className = `file-name ${entry.type}`;
      name.textContent = `${entry.type === 'directory' ? '▸' : '·'} ${entry.name}`;
      name.title = joinPath(this.currentPath, entry.name);
      const size = document.createElement('span');
      size.textContent = entry.type === 'directory' ? '—' : formatSize(entry.size);
      const type = document.createElement('span');
      type.textContent = entry.type === 'directory' ? '文件夹' : entry.type === 'symlink' ? '链接' : '文件';
      const modified = document.createElement('span');
      modified.textContent = new Date(entry.mtime * 1000).toLocaleString('zh-CN', { hour12: false });
      const permissions = document.createElement('span');
      permissions.textContent = entry.permissions;
      row.append(name, size, type, modified, permissions);
      row.addEventListener('click', (event) => this.select(entry.name, event.metaKey || event.ctrlKey));
      row.addEventListener('dblclick', () => void this.open(entry));
      if (entry.type === 'file') {
        row.addEventListener('contextmenu', (event) => {
          event.preventDefault();
          this.contextEntry = entry;
          const menu = element<HTMLDivElement>('file-context-menu');
          menu.style.left = `${Math.min(event.clientX, window.innerWidth - 190)}px`;
          menu.style.top = `${Math.min(event.clientY, window.innerHeight - 90)}px`;
          menu.hidden = false;
        });
      }
      row.addEventListener('keydown', (event) => {
        if (event.key === 'Enter') void this.open(entry);
      });
      this.fileList.append(row);
    }
  }

  private select(name: string, toggle: boolean): void {
    if (!toggle) this.selected.clear();
    if (this.selected.has(name)) this.selected.delete(name);
    else this.selected.add(name);
    this.updateSelection();
  }

  private updateSelection(): void {
    for (const row of this.fileList.querySelectorAll<HTMLElement>('.file-row')) {
      row.classList.toggle('selected', this.selected.has(row.dataset.name ?? ''));
    }
    const hasDownload = Array.from(this.selected).some((name) => ['file', 'directory'].includes(this.fileTypes.get(name) ?? ''));
    element<HTMLButtonElement>('sftp-download').disabled = !hasDownload;
  }

  private async open(entry: FileEntry, application = ''): Promise<void> {
    if (!this.sessionId) return;
    const remotePath = joinPath(this.currentPath, entry.name);
    if (entry.type === 'directory') {
      await this.load(remotePath);
      return;
    }
    if (entry.type !== 'file') {
      this.onError('暂不支持编辑链接文件，请先进入其实际目录');
      return;
    }
    try {
      const result = await this.api.openRemote(this.sessionId, remotePath, application);
      this.onError(`已打开本地缓存，保存会自动回传：${result.cachePath}`);
    } catch (error) {
      this.onError(messageOf(error));
    }
  }

  private async downloadSelected(): Promise<void> {
    if (!this.sessionId) return;
    for (const name of this.selected) {
      const type = this.fileTypes.get(name);
      if (type !== 'file' && type !== 'directory') continue;
      const remotePath = joinPath(this.currentPath, name);
      try {
        if (type === 'directory') {
          const result = await this.api.downloadDirectory(this.sessionId, remotePath);
          this.onError(`已下载 ${result.files} 个文件到：${result.path}`);
        } else {
          const result = await this.api.download(this.sessionId, remotePath);
          this.onError(`已下载到：${result.path}`);
        }
      } catch (error) {
        this.onError(`${name}：${messageOf(error)}`);
      }
    }
  }

  private async uploadFiles(files: File[]): Promise<void> {
    if (!this.sessionId) return;
    for (const file of files) {
      const remotePath = joinPath(this.currentPath, file.name);
      const progress = this.transfers.beginUpload(file.name, remotePath, file.size);
      try {
        await this.api.upload(this.sessionId, this.currentPath, file, (loaded, total) => progress.update(loaded, total));
        progress.finish();
      } catch (error) {
        progress.finish(error);
        this.onError(`${file.name}：${messageOf(error)}`);
      }
    }
    await this.load();
  }
}
