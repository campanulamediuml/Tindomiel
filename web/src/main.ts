import '@xterm/xterm/css/xterm.css';
import './style.css';
import { Api } from './api';
import { ConnectionManager } from './connection';
import { CustomSelect } from './custom-select';
import { LogCenter, recordFrontendLog } from './logs';
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
  button?: HTMLElement;
}

const tabs = new Map<string, Tab>();
let activeId: string | undefined;
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

async function connect(profile: Profile): Promise<void> {
  showToast(`正在连接 ${profile.name}…`);
  try {
    const api = apiClient;
    const session = await createSession(api, profile.id);
    const terminal = new TerminalView(
      session,
      api,
      (status) => updateStatus(session.sessionId, status),
      showToast,
      (transfer) => transfers.update(transfer),
      (path) => {
        if (activeId === session.sessionId) void sftp.load(path).catch((error) => showToast(messageOf(error)));
      },
    );
    tabs.set(session.sessionId, { profile, terminal, status: 'connecting' });
    terminalArea.append(terminal.element);
    renderTabs();
    activate(session.sessionId);
    await terminal.connect();
  } catch (error) {
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
  metrics.setSession(sessionId, next.profile.options.host, next.status);
  void sftp.setSession(sessionId).catch((error) => showToast(messageOf(error)));
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
  tabs.delete(sessionId);
  try {
    tab.terminal.dispose();
  } catch (error) {
    recordFrontendLog(`销毁终端失败：${messageOf(error)}`, 'error');
  }
  void apiClient.deleteSession(sessionId).catch((error) => showToast(messageOf(error)));
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
    button.addEventListener('click', () => activate(sessionId));
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
