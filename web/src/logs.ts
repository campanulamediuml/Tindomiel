import type { Api } from './api';
import { element, messageOf } from './utils';

interface FrontendLogEntry {
  time: number;
  level: string;
  message: string;
}

const storageKey = 'tindomiel.frontendLogs';
const retentionMs = 3 * 24 * 60 * 60 * 1000;

export class LogCenter {
  private activeTab: 'backend' | 'frontend' = 'backend';

  constructor(private readonly api: Api) {
    element<HTMLButtonElement>('logs-button').addEventListener('click', () => void this.open());
    element<HTMLButtonElement>('refresh-logs').addEventListener('click', () => void this.load());
    element<HTMLButtonElement>('backend-log-tab').addEventListener('click', () => this.selectTab('backend'));
    element<HTMLButtonElement>('frontend-log-tab').addEventListener('click', () => this.selectTab('frontend'));
    document.addEventListener('tindomiel:frontend-log', () => {
      if (this.activeTab === 'frontend') void this.load();
    });
  }

  private async open(): Promise<void> {
    element<HTMLDialogElement>('logs-dialog').showModal();
    await this.load();
  }

  private selectTab(tab: 'backend' | 'frontend'): void {
    this.activeTab = tab;
    element<HTMLButtonElement>('backend-log-tab').classList.toggle('active', tab === 'backend');
    element<HTMLButtonElement>('frontend-log-tab').classList.toggle('active', tab === 'frontend');
    void this.load();
  }

  private async load(): Promise<void> {
    const content = element<HTMLPreElement>('log-content');
    content.textContent = '读取中…';
    try {
      if (this.activeTab === 'backend') {
        const result = await this.api.recentLogs(300);
        element<HTMLDivElement>('log-path').textContent = result.path;
        content.textContent = result.log || '暂无日志';
      } else {
        const entries = readFrontendLogs().reverse();
        element<HTMLDivElement>('log-path').textContent = '界面事件保留最近 3 天';
        content.textContent = entries.length
          ? entries.map((entry) => `${new Date(entry.time).toISOString()} [${entry.level}] ${entry.message}`).join('\n')
          : '暂无日志';
      }
    } catch (error) {
      content.textContent = messageOf(error);
    }
  }
}

export function recordFrontendLog(message: string, level = 'info'): void {
  const entries = readFrontendLogs();
  entries.push({ time: Date.now(), level, message });
  const cutoff = Date.now() - retentionMs;
  const retained = entries.filter((entry) => entry.time >= cutoff).slice(-300);
  try {
    localStorage.setItem(storageKey, JSON.stringify(retained));
  } catch {
    return;
  }
  document.dispatchEvent(new CustomEvent('tindomiel:frontend-log'));
}

function readFrontendLogs(): FrontendLogEntry[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(storageKey) ?? '[]') as FrontendLogEntry[];
    const cutoff = Date.now() - retentionMs;
    return Array.isArray(parsed) ? parsed.filter((entry) => entry.time >= cutoff) : [];
  } catch {
    return [];
  }
}
