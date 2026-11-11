import { FitAddon } from '@xterm/addon-fit';
import { WebglAddon } from '@xterm/addon-webgl';
import { Terminal } from '@xterm/xterm';
import type { Api } from './api';
import { encodeShortcut, shortcutFromEvent } from './shortcuts';
import { terminalThemes } from './terminal-themes';
import type { AppSettings, DisplayStatus, ResourceMetrics, SessionInfo, SessionStatus, ShortcutMapping, TransferState } from './types';

const encoder = new TextEncoder();

export class TerminalView {
  readonly element = document.createElement('div');
  readonly terminal: Terminal;
  status: DisplayStatus = 'connecting';

  private readonly fitAddon = new FitAddon();
  private readonly resizeObserver: ResizeObserver;
  private websocket?: WebSocket;
  private disposed = false;
  private resizeFrame = 0;
  private readonly shortcutMap: Map<string, string>;

  constructor(
    readonly session: SessionInfo,
    private readonly api: Api,
    private readonly onStatus: (status: DisplayStatus) => void,
    private readonly onError: (message: string) => void,
    private readonly onTransfer: (transfer: TransferState) => void,
    private readonly onMetrics: (metrics: ResourceMetrics) => void,
    private readonly onCwd: (path: string) => void,
    settings: AppSettings,
    shortcuts: ShortcutMapping[] = [],
  ) {
    this.shortcutMap = new Map(shortcuts.map((shortcut) => [shortcut.from, shortcut.to]));
    this.element.className = 'terminal-view';
    this.element.dataset.sessionId = session.sessionId;
    this.terminal = new Terminal({
      allowProposedApi: true,
      cursorBlink: settings.cursorBlink,
      fontFamily: '"JetBrains Mono", "SF Mono", Menlo, monospace',
      fontSize: settings.fontSize,
      lineHeight: 1.35,
      scrollback: settings.scrollback,
      theme: terminalThemes[settings.theme],
    });
    this.terminal.loadAddon(this.fitAddon);
    this.terminal.open(this.element);
    try {
      const webgl = new WebglAddon();
      webgl.onContextLoss(() => webgl.dispose());
      this.terminal.loadAddon(webgl);
    } catch {
      this.onError('WebGL 不可用，已使用 xterm 普通渲染模式');
    }
    this.terminal.onData((data) => this.sendInput(encoder.encode(data)));
    this.terminal.onBinary((data) => {
      const bytes = new Uint8Array(data.length);
      for (let index = 0; index < data.length; index += 1) bytes[index] = data.charCodeAt(index);
      this.sendInput(bytes);
    });
    this.terminal.attachCustomKeyEventHandler((event) => {
      if (event.type === 'keydown' && this.handlesShortcut(event)) {
        event.preventDefault();
        event.stopPropagation();
        const mapped = this.shortcutMap.get(shortcutFromEvent(event));
        if (mapped) {
          const data = encodeShortcut(mapped);
          if (data.length > 0) this.sendInput(data);
        }
        return false;
      }
      if (event.type !== 'keydown' || !event.metaKey) return true;
      if (event.key.toLowerCase() === 'c' && this.terminal.hasSelection()) {
        event.preventDefault();
        event.stopPropagation();
        void navigator.clipboard.writeText(this.terminal.getSelection());
        return false;
      }
      if (event.key.toLowerCase() === 'v') {
        event.preventDefault();
        event.stopPropagation();
        void navigator.clipboard.readText().then((text) => this.terminal.paste(text)).catch(() => undefined);
        return false;
      }
      return true;
    });
    this.resizeObserver = new ResizeObserver(() => this.scheduleFit());
    this.resizeObserver.observe(this.element);
    this.element.addEventListener('scroll', (event) => {
      if (event.target instanceof HTMLElement) event.target.scrollLeft = 0;
      this.element.scrollLeft = 0;
    }, true);
  }

  async connect(): Promise<void> {
    if (this.disposed) return;
    this.websocket?.close();
    const url = await this.api.terminalUrl(this.session.sessionId);
    if (this.disposed) return;
    const websocket = new WebSocket(url);
    websocket.binaryType = 'arraybuffer';
    this.websocket = websocket;
    websocket.onmessage = (event) => {
      if (typeof event.data !== 'string') {
        this.terminal.write(new Uint8Array(event.data as ArrayBuffer));
        return;
      }
      const control = JSON.parse(event.data) as { type: string; status?: SessionStatus; message?: string; transfer?: TransferState; metrics?: ResourceMetrics; path?: string };
      if (control.type === 'status' && control.status) {
        this.setStatus(control.status);
      } else if (control.type === 'exit') {
        this.setStatus('closed');
      } else if (control.type === 'error' || control.type === 'sftp') {
        this.onError(control.message ?? '终端错误');
      } else if (control.type === 'transfer' && control.transfer) {
        this.onTransfer(control.transfer);
      } else if (control.type === 'metrics' && control.metrics) {
        this.onMetrics(control.metrics);
      } else if (control.type === 'cwd' && control.path) {
        this.onCwd(control.path);
      }
    };
    websocket.onclose = () => {
      if (!this.disposed && this.websocket === websocket && this.status !== 'closed') {
        this.setStatus('detached');
      }
    };
    websocket.onerror = () => this.onError('本地终端通道连接失败');
  }

  activate(): void {
    this.element.hidden = false;
    this.terminal.scrollToBottom();
    this.scheduleFit();
    this.terminal.focus();
  }

  deactivate(): void {
    this.element.hidden = true;
  }

  focus(): void {
    this.terminal.focus();
  }

  applySettings(settings: AppSettings): void {
    this.terminal.options.theme = terminalThemes[settings.theme];
    this.terminal.options.fontSize = settings.fontSize;
    this.terminal.options.cursorBlink = settings.cursorBlink;
    this.terminal.options.scrollback = settings.scrollback;
    this.scheduleFit();
  }

  getShortcuts(): ShortcutMapping[] {
    return Array.from(this.shortcutMap, ([from, to]) => ({ from, to }));
  }

  setShortcuts(shortcuts: ShortcutMapping[]): void {
    this.shortcutMap.clear();
    for (const shortcut of shortcuts) this.shortcutMap.set(shortcut.from, shortcut.to);
  }

  handlesShortcut(event: KeyboardEvent): boolean {
    return event.type === 'keydown' && !event.isComposing && this.shortcutMap.has(shortcutFromEvent(event));
  }

  async reconnect(): Promise<void> {
    if (this.status === 'detached') {
      await this.connect();
      return;
    }
    await this.api.reconnect(this.session.sessionId);
  }

  dispose(): void {
    this.disposed = true;
    cancelAnimationFrame(this.resizeFrame);
    this.resizeObserver.disconnect();
    this.websocket?.close();
    try {
      this.terminal.dispose();
    } finally {
      this.element.remove();
    }
  }

  private sendInput(data: Uint8Array): void {
    if (this.status !== 'ready' || this.websocket?.readyState !== WebSocket.OPEN) return;
    this.websocket.send(data);
  }

  private setStatus(status: DisplayStatus): void {
    if (status === 'reconnecting' && this.status !== 'reconnecting') {
      const buffer = this.terminal.buffer.active;
      const currentLine = buffer.getLine(buffer.baseY + buffer.cursorY)?.translateToString(true) ?? '';
      if (buffer.cursorX > 0 || currentLine.length > 0) this.terminal.write('\r\n');
    }
    this.status = status;
    this.session.status = status === 'detached' ? 'disconnected' : status;
    this.onStatus(status);
    if (status === 'ready') this.scheduleFit();
  }

  private scheduleFit(): void {
    cancelAnimationFrame(this.resizeFrame);
    this.resizeFrame = requestAnimationFrame(() => {
      if (this.disposed || this.element.hidden || this.element.clientWidth === 0) return;
      this.fitAddon.fit();
      const { cols, rows } = this.terminal;
      if (this.status === 'ready' && this.websocket?.readyState === WebSocket.OPEN) {
        this.websocket.send(JSON.stringify({ type: 'resize', cols, rows }));
      }
    });
  }
}
