import '@xterm/xterm/css/xterm.css';
import './style.css';
import { Api } from './api';
import { ConnectionManager } from './connection';
import { CustomSelect } from './custom-select';
import { LogCenter, recordFrontendLog } from './logs';
import { setupResizing } from './resize';
import { MetricsPanel } from './metrics';
import { SftpPanel } from './sftp';
import { TerminalView } from './terminal';
import { TransferCenter } from './transfer';
import { ApiError, type DisplayStatus, type HostKeyDetails, type Profile, type SessionInfo } from './types';
import { element, messageOf } from './utils';

interface Tab {
  profile: Profile;
  terminal: TerminalView;
  status: DisplayStatus;
  pending: boolean;
  cancelled: boolean;
  button?: HTMLElement;
}

const tabs = new Map<string, Tab>();
let activeId: string | undefined;
let tabContextId = '';
let draggedTabId = '';
let connections: ConnectionManager;
let metrics: MetricsPanel;
let sftp: SftpPanel;
let transfers: TransferCenter;
let apiClient: Api;
let toastTimer = 0;

const tabsElement = element<HTMLDivElement>('tabs');
const terminalArea = element<HTMLElement>('terminal-area');
const welcome = element<HTMLDivElement>('welcome');
const statusElement = element<HTMLSpanElement>('connection-status');
const reconnectButton = element<HTMLButtonElement>('reconnect');

async function start(): Promise<void> {
  try {
    const api = await Api.create();
    apiClient = api;
    for (const id of ['network-interface', 'auth-method', 'profile-folder']) {
      new CustomSelect(element<HTMLSelectElement>(id));
    }
    transfers = new TransferCenter();
    new LogCenter(api);
    void api.defaultDownloadPath().then(({ path }) => updateDownloadPath(path)).catch((error) => showToast(messageOf(error)));
    metrics = new MetricsPanel(api, showToast);
    sftp = new SftpPanel(api, transfers, showToast);
    connections = new ConnectionManager(api, (profile) => void connect(profile), showToast);
    bindEvents();
  } catch (error) {
    showToast(messageOf(error));
  }
}

function updateDownloadPath(path: string): void {
  const label = element<HTMLButtonElement>('download-path');
  label.textContent = `下载：${path}`;
  label.title = `在 Finder 中打开：${path}`;
}

function bindEvents(): void {
  document.addEventListener('contextmenu', (event) => event.preventDefault());
  document.addEventListener('scroll', () => {
    document.documentElement.scrollLeft = 0;
    document.body.scrollLeft = 0;
  }, true);
  enableTabDragScroll();
  setupResizing();
  const tabMenu = element<HTMLDivElement>('tab-context-menu');
  document.addEventListener('click', () => { tabMenu.hidden = true; });
  element<HTMLButtonElement>('tab-reconnect').addEventListener('click', () => {
    const tab = tabs.get(tabContextId);
    tabMenu.hidden = true;
    if (!tab) return;
    if (tab.pending) {
      if (tab.status !== 'error') return;
      const profile = tab.profile;
      closeTab(tabContextId);
      void connect(profile);
      return;
    }
    void tab.terminal.reconnect().catch((error) => showToast(messageOf(error)));
  });
  for (const id of ['new-tab', 'welcome-connect']) {
    element<HTMLButtonElement>(id).addEventListener('click', () => void connections.open());
  }
  const connectionMenu = element<HTMLDivElement>('connection-menu');
  element<HTMLButtonElement>('new-connection').addEventListener('click', (event) => {
    event.stopPropagation();
    connectionMenu.hidden = !connectionMenu.hidden;
  });
  connectionMenu.addEventListener('click', (event) => event.stopPropagation());
  document.addEventListener('click', () => { connectionMenu.hidden = true; });
  element<HTMLButtonElement>('menu-new-profile').addEventListener('click', () => {
    connectionMenu.hidden = true;
    void connections.newProfile().catch((error) => showToast(messageOf(error)));
  });
  element<HTMLButtonElement>('menu-open-manager').addEventListener('click', () => {
    connectionMenu.hidden = true;
    void connections.open();
  });
  element<HTMLButtonElement>('menu-new-folder').addEventListener('click', () => {
    connectionMenu.hidden = true;
    void connections.open().then(() => element<HTMLButtonElement>('add-folder').click());
  });
  reconnectButton.addEventListener('click', () => {
    void activeTerminal()?.reconnect().catch((error) => showToast(messageOf(error)));
  });
  element<HTMLButtonElement>('download-path').addEventListener('click', () => {
    void apiClient.openDownloadDirectory().catch((error) => showToast(messageOf(error)));
  });
  element<HTMLButtonElement>('choose-download-path').addEventListener('click', () => {
    void apiClient.chooseDownloadDirectory().then((path) => {
      if (!path) return;
      updateDownloadPath(path);
      showToast(`下载目录已更改为：${path}`);
    }).catch((error) => showToast(messageOf(error)));
  });
}

function enableTabDragScroll(): void {
  let startX = 0;
  let startScroll = 0;
  let dragging = false;
  let moved = false;
  let suppressClick = false;
  tabsElement.addEventListener('pointerdown', (event) => {
    if (event.button !== 0 || ((event.target as HTMLElement).closest('.tab') && !event.shiftKey)) return;
    dragging = true;
    moved = false;
    startX = event.clientX;
    startScroll = tabsElement.scrollLeft;
    tabsElement.setPointerCapture(event.pointerId);
    tabsElement.classList.add('dragging');
  });
  tabsElement.addEventListener('pointermove', (event) => {
    if (!dragging) return;
    const delta = event.clientX - startX;
    if (Math.abs(delta) > 4) moved = true;
    tabsElement.scrollLeft = startScroll - delta;
  });
  const finish = (event: PointerEvent) => {
    if (!dragging) return;
    dragging = false;
    if (moved) suppressClick = true;
    if (tabsElement.hasPointerCapture(event.pointerId)) tabsElement.releasePointerCapture(event.pointerId);
    tabsElement.classList.remove('dragging');
  };
  tabsElement.addEventListener('pointerup', finish);
  tabsElement.addEventListener('pointercancel', finish);
  tabsElement.addEventListener('click', (event) => {
    if (!suppressClick) return;
    event.preventDefault();
    event.stopPropagation();
    suppressClick = false;
  }, true);
}

function reorderTabs(draggedId: string, targetId: string, insertAfter: boolean): void {
  if (draggedId === targetId || !tabs.has(draggedId)) return;
  const dragged = tabs.get(draggedId)!;
  const entries = Array.from(tabs.entries()).filter(([id]) => id !== draggedId);
  const targetIndex = entries.findIndex(([id]) => id === targetId);
  if (targetIndex < 0) return;
  entries.splice(targetIndex + (insertAfter ? 1 : 0), 0, [draggedId, dragged]);
  tabs.clear();
  for (const [id, tab] of entries) tabs.set(id, tab);
  renderTabs();
}

async function connect(profile: Profile): Promise<void> {
  const api = apiClient;
  const session: SessionInfo = {
    sessionId: `pending-${crypto.randomUUID()}`,
    profileId: profile.id,
    status: 'connecting',
    openedAt: Math.floor(Date.now() / 1000),
  };
  const terminal = new TerminalView(
    session,
    api,
    (status) => updateStatus(session.sessionId, status),
    showToast,
    (transfer) => transfers.update(transfer),
    (snapshot) => metrics.receive(session.sessionId, snapshot),
    (path) => {
      if (activeId === session.sessionId) void sftp.load(path).catch((error) => showToast(messageOf(error)));
    },
  );
  const tab: Tab = { profile, terminal, status: 'connecting', pending: true, cancelled: false };
  tabs.set(session.sessionId, tab);
  terminalArea.append(terminal.element);
  terminal.terminal.write(`正在连接 ${profile.name}…\r\n`);
  renderTabs();
  activate(session.sessionId);
  const pendingId = session.sessionId;
  try {
    const created = await createSession(api, profile.id);
    if (tab.cancelled || !tabs.has(pendingId)) {
      void api.deleteSession(created.sessionId).catch(() => undefined);
      return;
    }
    tabs.delete(pendingId);
    Object.assign(session, created);
    tab.pending = false;
    tabs.set(session.sessionId, tab);
    if (activeId === pendingId) {
      activeId = session.sessionId;
      renderTabs();
      activate(session.sessionId);
    } else {
      renderTabs();
    }
    await terminal.connect();
  } catch (error) {
    if (tab.cancelled || !tabs.has(pendingId)) return;
    tab.status = 'error';
    terminal.terminal.write(`连接失败：${messageOf(error)}\r\n`);
    if (tab.button) {
      tab.button.dataset.status = 'error';
      tab.button.title = `${profile.name} · 错误`;
    }
    if (activeId === pendingId) updateToolbar('error');
    showToast(messageOf(error));
  }
}

async function createSession(api: Api, profileId: string) {
  try {
    return await api.createSession(profileId);
  } catch (error) {
    if (!(error instanceof ApiError) || error.code !== 'HOST_KEY_UNKNOWN') throw error;
    const details = error.details as HostKeyDetails;
    return confirmHostKey(api, profileId, details);
  }
}

function confirmHostKey(api: Api, profileId: string, details: HostKeyDetails): Promise<SessionInfo> {
  element<HTMLElement>('host-key-address').textContent = `${details.host}:${details.port}`;
  element<HTMLElement>('host-key-type').textContent = details.keyType;
  element<HTMLElement>('host-key-fingerprint').textContent = details.fingerprint;
  const dialog = element<HTMLDialogElement>('host-key-dialog');
  dialog.showModal();
  return new Promise((resolve, reject) => {
    let settled = false;
    element<HTMLButtonElement>('host-key-accept').onclick = async () => {
      if (settled) return;
      settled = true;
      try {
        await api.acceptHostKey(details.verificationId, details.fingerprint);
        dialog.close();
        resolve(api.createSession(profileId));
      } catch (error) {
        reject(error);
      }
    };
    element<HTMLButtonElement>('host-key-cancel').onclick = () => {
      if (settled) return;
      settled = true;
      dialog.close();
      reject(new Error('已取消未知主机连接'));
    };
    dialog.addEventListener('close', () => {
      if (!settled) reject(new Error('已取消未知主机连接'));
    }, { once: true });
  });
}

function activate(sessionId: string): void {
  const next = tabs.get(sessionId);
  if (!next) return;
  for (const [id, tab] of tabs) {
    if (id !== sessionId) tab.terminal.deactivate();
    if (tab.button) tab.button.classList.toggle('active', id === sessionId);
  }
  activeId = sessionId;
  next.terminal.activate();
  welcome.hidden = true;
  updateToolbar(next.status);
  if (next.pending) {
    metrics.setSession(undefined);
    void sftp.setSession(undefined).catch((error) => showToast(messageOf(error)));
  } else {
    metrics.setSession(sessionId, next.profile.options.host, next.status);
    void sftp.setSession(sessionId).catch((error) => showToast(messageOf(error)));
  }
}

function updateStatus(sessionId: string, status: DisplayStatus): void {
  const tab = tabs.get(sessionId);
  if (!tab) return;
  if (tab.status !== status) recordFrontendLog(`${tab.profile.name}: ${statusText(status)}`);
  tab.status = status;
  if (tab.button) {
    tab.button.dataset.status = status;
    tab.button.title = `${tab.profile.name} · ${statusText(status)}`;
  }
  if (activeId === sessionId) {
    updateToolbar(status);
    metrics.setStatus(status);
  }
}

function updateToolbar(status: DisplayStatus): void {
  statusElement.textContent = statusText(status);
  statusElement.className = `status ${status}`;
  reconnectButton.hidden = status !== 'disconnected' && status !== 'detached';
}

function closeTab(sessionId: string): void {
  const tab = tabs.get(sessionId);
  if (!tab) return;
  recordFrontendLog(`关闭标签 ${tab.profile.name} (${sessionId})`);
  tab.cancelled = true;
  tabs.delete(sessionId);
  metrics.removeSession(sessionId);
  try {
    tab.terminal.dispose();
  } catch (error) {
    recordFrontendLog(`销毁终端失败：${messageOf(error)}`, 'error');
  }
  if (!tab.pending) void apiClient.deleteSession(sessionId).catch((error) => showToast(messageOf(error)));
  if (activeId === sessionId) {
    activeId = undefined;
    const next = Array.from(tabs.keys()).at(-1);
    if (next) {
      activate(next);
    } else {
      welcome.hidden = false;
      reconnectButton.hidden = true;
      statusElement.textContent = '未连接';
      statusElement.className = 'status offline';
      metrics.setSession(undefined);
      void sftp.setSession(undefined).catch((error) => showToast(messageOf(error)));
    }
  }
  renderTabs();
}

function renderTabs(): void {
  tabsElement.replaceChildren();
  for (const [sessionId, tab] of tabs) {
    const button = document.createElement('div');
    button.className = 'tab';
    button.dataset.status = tab.status;
    button.tabIndex = 0;
    const state = document.createElement('span');
    state.className = 'status-dot';
    const name = document.createElement('span');
    name.textContent = tab.profile.name;
    const close = document.createElement('button');
    close.type = 'button';
    close.className = 'tab-close';
    close.textContent = '×';
    close.title = '关闭会话';
    close.addEventListener('click', (event) => {
      event.stopPropagation();
      void closeTab(sessionId);
    });
    button.append(state, name, close);
    button.draggable = true;
    button.addEventListener('click', () => activate(sessionId));
    button.addEventListener('dragstart', (event) => {
      draggedTabId = sessionId;
      if (event.dataTransfer) {
        event.dataTransfer.effectAllowed = 'move';
        event.dataTransfer.setData('text/plain', sessionId);
      }
    });
    button.addEventListener('dragover', (event) => {
      event.preventDefault();
      if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
    });
    button.addEventListener('drop', (event) => {
      event.preventDefault();
      const bounds = button.getBoundingClientRect();
      reorderTabs(draggedTabId, sessionId, event.clientX > bounds.left + bounds.width / 2);
      draggedTabId = '';
    });
    button.addEventListener('dragend', () => { draggedTabId = ''; });
    button.addEventListener('contextmenu', (event) => {
      event.preventDefault();
      tabContextId = sessionId;
      const menu = element<HTMLDivElement>('tab-context-menu');
      const reconnect = element<HTMLButtonElement>('tab-reconnect');
      reconnect.disabled = tab.pending ? tab.status !== 'error' : tab.status !== 'disconnected' && tab.status !== 'detached';
      menu.style.left = `${Math.min(event.clientX, window.innerWidth - 190)}px`;
      menu.style.top = `${Math.min(event.clientY, window.innerHeight - 60)}px`;
      menu.hidden = false;
    });
    button.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') activate(sessionId);
    });
    tab.button = button;
    tabsElement.append(button);
  }
  if (activeId) tabs.get(activeId)?.button?.classList.add('active');
}

function activeTerminal(): TerminalView | undefined {
  return activeId ? tabs.get(activeId)?.terminal : undefined;
}

function showToast(message: string): void {
  recordFrontendLog(message);
  const toast = element<HTMLDivElement>('toast');
  window.clearTimeout(toastTimer);
  toast.textContent = message;
  toast.classList.add('visible');
  toastTimer = window.setTimeout(() => toast.classList.remove('visible'), 5000);
}

function statusText(status: DisplayStatus): string {
  const labels: Record<DisplayStatus, string> = {
    connecting: '连接中',
    ready: '已连接',
    disconnected: '已断开',
    reconnecting: '重连中',
    closed: '已关闭',
    error: '错误',
    detached: '终端通道已断开',
  };
  return labels[status];
}

void start();
